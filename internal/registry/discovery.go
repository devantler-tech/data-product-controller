package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxDiscoveryResponseBytes = 2 << 20
	maxDescriptorBytes        = 64 << 10
	maxCursorBytes            = 16 << 10
	maxNativeCursorBytes      = 8 << 10
	discoveryVersion          = "data-product-discovery/v1"
)

type discoveryPage struct {
	APIVersion string            `json:"apiVersion"`
	Products   []json.RawMessage `json:"products"`
	Continue   string            `json:"continue"`
	Rejected   int               `json:"rejected"`
}

type discoveryQuery struct {
	Namespace string `json:"namespace"`
	Limit     int64  `json:"limit"`
	Token     string `json:"token"`
}

// discoveryAllowed evaluates the release gate before validation or Kubernetes reads.
func (s *server) discoveryAllowed(writer http.ResponseWriter, request *http.Request) bool {
	writer.Header().Set("Cache-Control", "no-store")
	if s.discoveryEnabled == nil || !s.discoveryEnabled(request.Context()) {
		http.NotFound(writer, request)
		return false
	}
	if request.ContentLength > 0 || len(request.TransferEncoding) != 0 {
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-request",
			"Discovery requests do not accept a body.",
		)
		return false
	}
	return true
}

// discoveryProducts returns exactly one native Kubernetes page without changing snapshot ordering.
func (s *server) discoveryProducts(writer http.ResponseWriter, request *http.Request) {
	if !s.discoveryAllowed(writer, request) {
		return
	}
	query, err := parseDiscoveryQuery(request.URL.RawQuery)
	if err != nil {
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-query",
			"Use one limit from 1 to 100, an optional namespace, and the unchanged continuation.",
		)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	products := &datav1alpha1.DataProductList{}
	if err := s.reader.List(
		ctx,
		products,
		client.InNamespace(query.Namespace),
		client.Limit(query.Limit),
		client.Continue(query.Token),
	); err != nil {
		readFailure(writer, err)
		return
	}
	if err := ctx.Err(); err != nil {
		readFailure(writer, err)
		return
	}
	if int64(len(products.Items)) > query.Limit || len(products.Continue) > maxNativeCursorBytes {
		discoveryFailure(
			writer,
			http.StatusBadGateway,
			"invalid-backend-page",
			"The inventory reader did not honor the requested page bounds.",
		)
		return
	}
	page := discoveryPage{
		APIVersion: discoveryVersion,
		Products:   make([]json.RawMessage, 0, len(products.Items)),
	}
	retained := 0
	for index := range products.Items {
		product := &products.Items[index]
		if query.Namespace != "" && product.Namespace != query.Namespace {
			discoveryFailure(
				writer,
				http.StatusBadGateway,
				"invalid-backend-page",
				"The inventory reader did not honor the requested namespace.",
			)
			return
		}
		data, err := encodePortableDescriptor(product)
		if err != nil {
			page.Rejected++
			continue
		}
		retained += len(data)
		if retained > maxDiscoveryResponseBytes {
			descriptorFailure(writer, errDescriptorTooLarge)
			return
		}
		page.Products = append(page.Products, data)
	}
	if products.Continue != "" {
		query.Token = products.Continue
		data, err := json.Marshal(query)
		if err != nil {
			readFailure(writer, err)
			return
		}
		page.Continue = base64.RawURLEncoding.EncodeToString(data)
		if len(page.Continue) > maxCursorBytes {
			discoveryFailure(
				writer,
				http.StatusBadGateway,
				"invalid-backend-page",
				"The inventory continuation exceeds the public cursor bound.",
			)
			return
		}
	}
	data, err := json.Marshal(page)
	if err != nil {
		readFailure(writer, err)
		return
	}
	if len(data) > maxDiscoveryResponseBytes {
		descriptorFailure(writer, errDescriptorTooLarge)
		return
	}
	writeDiscoveryJSON(writer, data)
}

// discoveryProduct performs a bounded exact lookup; it never falls back to listing the inventory.
func (s *server) discoveryProduct(writer http.ResponseWriter, request *http.Request) {
	if !s.discoveryAllowed(writer, request) {
		return
	}
	key := client.ObjectKey{
		Namespace: request.PathValue("namespace"),
		Name:      request.PathValue("name"),
	}
	if len(validation.IsDNS1123Label(key.Namespace)) != 0 ||
		!validProductName(key.Name) ||
		request.URL.RawQuery != "" {
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-product-reference",
			"Use an exact Kubernetes namespace and product name without query parameters.",
		)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	product := &datav1alpha1.DataProduct{}
	if err := s.reader.Get(ctx, key, product); err != nil {
		readFailure(writer, err)
		return
	}
	if err := ctx.Err(); err != nil {
		readFailure(writer, err)
		return
	}
	if product.Namespace != key.Namespace || product.Name != key.Name {
		discoveryFailure(
			writer,
			http.StatusBadGateway,
			"invalid-backend-product",
			"The inventory reader returned another product.",
		)
		return
	}
	data, err := encodePortableDescriptor(product)
	if err != nil {
		descriptorFailure(writer, err)
		return
	}
	writeDiscoveryJSON(writer, data)
}

// parseDiscoveryQuery binds a bounded, replica-portable cursor to its original namespace and page size.
func parseDiscoveryQuery(raw string) (discoveryQuery, error) {
	query := discoveryQuery{Limit: 50}
	if len(raw) > maxCursorBytes+512 {
		return query, errors.New("query too long")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return query, err
	}
	for name, value := range values {
		if len(value) != 1 || (name != "namespace" && name != "limit" && name != "continue") {
			return query, errors.New("unsupported or duplicate parameter")
		}
	}
	query.Namespace = values.Get("namespace")
	if query.Namespace != "" && len(validation.IsDNS1123Label(query.Namespace)) != 0 {
		return query, errors.New("invalid namespace")
	}
	if values.Has("limit") {
		limit := values.Get("limit")
		query.Limit, err = strconv.ParseInt(limit, 10, 64)
		if err != nil || query.Limit < 1 || query.Limit > 100 ||
			strconv.FormatInt(query.Limit, 10) != limit {
			return query, errors.New("invalid limit")
		}
	}
	if cursor := values.Get("continue"); cursor != "" {
		if len(cursor) > maxCursorBytes {
			return query, errors.New("cursor too long")
		}
		data, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return query, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var saved discoveryQuery
		if err := decoder.Decode(&saved); err != nil {
			return query, err
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return query, errors.New("trailing cursor data")
		}
		if saved.Namespace != query.Namespace || saved.Limit != query.Limit || saved.Token == "" ||
			len(saved.Token) > maxNativeCursorBytes {
			return query, errors.New("cursor query mismatch")
		}
		canonical, err := json.Marshal(saved)
		if err != nil || !bytes.Equal(canonical, data) {
			return query, errors.New("noncanonical cursor")
		}
		query.Token = saved.Token
	}
	return query, nil
}

// writeDiscoveryJSON publishes only a fully validated and buffered success response.
func writeDiscoveryJSON(writer http.ResponseWriter, data []byte) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = writer.Write(data)
}

// discoveryFailure exposes a stable public failure code without raw backend errors or descriptor fragments.
func discoveryFailure(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{code, message})
}

// readFailure keeps missing, expired, canceled, timed-out and unavailable reads distinguishable to clients.
func readFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		discoveryFailure(
			writer,
			http.StatusRequestTimeout,
			"request-canceled",
			"The inventory request was canceled.",
		)
	case errors.Is(err, context.DeadlineExceeded),
		apierrors.IsTimeout(err),
		apierrors.IsServerTimeout(err):
		discoveryFailure(
			writer,
			http.StatusGatewayTimeout,
			"deadline-exceeded",
			"The inventory read exceeded its deadline.",
		)
	case apierrors.IsResourceExpired(err), apierrors.IsGone(err):
		discoveryFailure(
			writer,
			http.StatusGone,
			"continuation-expired",
			"The inventory snapshot expired. Restart from the first page.",
		)
	case apierrors.IsNotFound(err):
		discoveryFailure(
			writer,
			http.StatusNotFound,
			"product-not-found",
			"The selected product is no longer available.",
		)
	case apierrors.IsBadRequest(err):
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-continuation",
			"The inventory reader rejected the continuation. Restart from the first page.",
		)
	default:
		discoveryFailure(
			writer,
			http.StatusServiceUnavailable,
			"backend-unavailable",
			"The product inventory is temporarily unavailable.",
		)
	}
}

// descriptorFailure distinguishes unsupported published metadata from exceeded public response bounds.
func descriptorFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, errDescriptorTooLarge) {
		discoveryFailure(
			writer,
			http.StatusRequestEntityTooLarge,
			"descriptor-too-large",
			"Published metadata exceeds the discovery bounds. Reduce the page size or the product metadata.",
		)
		return
	}
	discoveryFailure(
		writer,
		http.StatusUnprocessableEntity,
		"invalid-descriptor",
		"Published metadata does not satisfy the portable descriptor contract.",
	)
}
