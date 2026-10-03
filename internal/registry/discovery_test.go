package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type discoveryReader struct {
	client.Reader
	list func(context.Context, client.ObjectList, ...client.ListOption) error
	get  func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
}

// discoveryProductList fails the test if the handler asks its reader for any other resource list.
func discoveryProductList(t *testing.T, out client.ObjectList) *datav1alpha1.DataProductList {
	t.Helper()
	products, ok := out.(*datav1alpha1.DataProductList)
	if !ok {
		t.Fatalf("unexpected list type %T", out)
	}
	return products
}

// discoveryProductObject fails the test if an exact read asks for a resource other than DataProduct.
func discoveryProductObject(t *testing.T, out client.Object) *datav1alpha1.DataProduct {
	t.Helper()
	product, ok := out.(*datav1alpha1.DataProduct)
	if !ok {
		t.Fatalf("unexpected object type %T", out)
	}
	return product
}

// List models only the external Kubernetes list boundary; the HTTP handler remains real.
func (r discoveryReader) List(
	ctx context.Context,
	out client.ObjectList,
	opts ...client.ListOption,
) error {
	if r.list == nil {
		return errors.New("unexpected list")
	}
	return r.list(ctx, out, opts...)
}

// Get models only the external exact-name read and never supplies a list fallback.
func (r discoveryReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	out client.Object,
	opts ...client.GetOption,
) error {
	if r.get == nil {
		return errors.New("unexpected get")
	}
	return r.get(ctx, key, out, opts...)
}

// discoveryHandler applies the explicit release policy to the real HTTP handler.
func discoveryHandler(reader client.Reader, enabled bool) http.Handler {
	return NewHandlerWithOptions(
		reader,
		HandlerOptions{DiscoveryEnabled: func(context.Context) bool { return enabled }},
	)
}

// discoveryRequest invokes the production router without starting an external listener.
func discoveryRequest(
	t *testing.T,
	handler http.Handler,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil),
	)
	return response
}

// TestDiscoveryGateNeverReads proves disabling the capability closes every v2 entry point.
func TestDiscoveryGateNeverReads(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(context.Context, client.ObjectList, ...client.ListOption) error {
			t.Fatal("disabled discovery read Kubernetes")
			return nil
		},
		get: func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
			t.Fatal("disabled discovery read Kubernetes")
			return nil
		},
	}
	for _, handler := range []http.Handler{NewHandler(reader), discoveryHandler(reader, false)} {
		for _, target := range []string{"/api/v2/products", "/api/v2/products/products/customer-catalog", "/api/v2/schema"} {
			if got := discoveryRequest(t, handler, target); got.Code != http.StatusNotFound {
				t.Fatalf("disabled %s status=%d, want 404", target, got.Code)
			}
		}
		got := discoveryRequest(t, handler, "/api/v1/ui-config")
		if !strings.Contains(got.Body.String(), `"discoveryEnabled":false`) {
			t.Fatalf("missing disabled capability: %s", got.Body.String())
		}
	}
}

// TestDiscoveryPaginationScopesAndBindsCursor catches unbounded reads and continuations reused under another query.
func TestDiscoveryPaginationScopesAndBindsCursor(t *testing.T) {
	t.Parallel()
	calls := 0
	reader := discoveryReader{
		list: func(ctx context.Context, out client.ObjectList, opts ...client.ListOption) error {
			calls++
			options := (&client.ListOptions{}).ApplyOptions(opts)
			if options.Limit != 1 || options.Namespace != "products" {
				t.Fatalf("unbounded or unscoped read: %+v", options)
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("read lacks bounded deadline")
			}
			result := discoveryProductList(t, out)
			product := registryProduct()
			if calls == 1 {
				if options.Continue != "" {
					t.Fatal("unexpected first continuation")
				}
				result.Continue = "kubernetes-opaque-token"
			} else {
				if options.Continue != "kubernetes-opaque-token" {
					t.Fatal("native continuation was not preserved")
				}
				product.Name = "second"
			}
			result.Items = []datav1alpha1.DataProduct{*product}
			return nil
		},
	}
	handler := discoveryHandler(reader, true)
	first := discoveryRequest(t, handler, "/api/v2/products?namespace=products&limit=1")
	var page struct {
		APIVersion string           `json:"apiVersion"`
		Products   []map[string]any `json:"products"`
		Continue   string           `json:"continue"`
	}
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &page) != nil {
		t.Fatalf("first page = %d %s", first.Code, first.Body.String())
	}
	if page.APIVersion != "data-product-discovery/v1" || len(page.Products) != 1 ||
		page.Continue == "" {
		t.Fatalf("missing portable page contract: %+v", page)
	}
	if page.Products[0]["apiVersion"] != "data-product-descriptor/v1" ||
		page.Products[0]["kind"] != "DataProduct" {
		t.Fatalf("unversioned descriptor: %+v", page.Products[0])
	}
	if strings.Contains(page.Continue, "kubernetes-opaque-token") {
		t.Fatal("cursor is not opaque")
	}
	for _, prefix := range []string{"namespace=another&limit=1", "namespace=products&limit=2"} {
		got := discoveryRequest(
			t,
			handler,
			"/api/v2/products?"+prefix+"&continue="+url.QueryEscape(page.Continue),
		)
		if got.Code != http.StatusBadRequest || calls != 1 {
			t.Fatalf("query-mismatched cursor accepted: %d, reads=%d", got.Code, calls)
		}
	}
	second := discoveryRequest(
		t,
		discoveryHandler(reader, true),
		"/api/v2/products?namespace=products&limit=1&continue="+url.QueryEscape(page.Continue),
	)
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &page) != nil ||
		len(page.Products) != 1 ||
		page.Products[0]["name"] != "second" ||
		page.Continue != "" {
		t.Fatalf("last page = %d %s", second.Code, second.Body.String())
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("snapshot response may be cached")
	}
}

// TestDiscoveryContinuesPastRejectedProducts prevents one publisher from blocking later native pages.
func TestDiscoveryContinuesPastRejectedProducts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*datav1alpha1.DataProduct)
		status int
	}{
		{"invalid", func(p *datav1alpha1.DataProduct) { p.Spec.Outputs[0].URL = "https://[fe80::1%25eth0]/data" }, http.StatusUnprocessableEntity},
		{"oversized", func(p *datav1alpha1.DataProduct) { p.Spec.Description = strings.Repeat("x", 16385) }, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rejected := registryProduct()
			test.mutate(rejected)
			reader := discoveryReader{
				list: func(_ context.Context, out client.ObjectList, opts ...client.ListOption) error {
					options := (&client.ListOptions{}).ApplyOptions(opts)
					page := discoveryProductList(t, out)
					if options.Continue == "" {
						page.Items = []datav1alpha1.DataProduct{*rejected}
						page.Continue = "next-native-page"
					} else {
						if options.Continue != "next-native-page" {
							t.Fatal("native continuation changed")
						}
						page.Items = []datav1alpha1.DataProduct{*registryProduct()}
					}
					return nil
				},
				get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
					rejected.DeepCopyInto(discoveryProductObject(t, out))
					return nil
				},
			}
			first := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products?limit=1")
			var page struct {
				Products []json.RawMessage `json:"products"`
				Continue string            `json:"continue"`
				Rejected int               `json:"rejected"`
			}
			if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &page) != nil ||
				len(page.Products) != 0 || page.Rejected != 1 || page.Continue == "" {
				t.Fatalf("rejected item blocked discovery: %d %s", first.Code, first.Body.String())
			}
			second := discoveryRequest(
				t,
				discoveryHandler(reader, true),
				"/api/v2/products?limit=1&continue="+url.QueryEscape(page.Continue),
			)
			if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &page) != nil ||
				len(page.Products) != 1 || page.Rejected != 0 || page.Continue != "" {
				t.Fatalf("later product was unreachable: %d %s", second.Code, second.Body.String())
			}
			exact := discoveryRequest(
				t,
				discoveryHandler(reader, true),
				"/api/v2/products/products/customer-catalog",
			)
			if exact.Code != test.status {
				t.Fatalf("exact invalid metadata status=%d, want %d", exact.Code, test.status)
			}
		})
	}
}

// TestDiscoveryRejectsInvalidQueriesBeforeReads keeps malformed scope and unsupported selectors out of the Kubernetes request.
func TestDiscoveryRejectsInvalidQueriesBeforeReads(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(context.Context, client.ObjectList, ...client.ListOption) error {
			t.Fatal("invalid query read Kubernetes")
			return nil
		},
	}
	handler := discoveryHandler(reader, true)
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=no", "limit=01", "limit=1&limit=2", "namespace=Bad", "namespace=a.b", "namespace=x&namespace=y", "owner=team", "continue=garbage", "namespace=%ZZ"} {
		got := discoveryRequest(t, handler, "/api/v2/products?"+query)
		if got.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", query, got.Code, got.Body.String())
		}
	}
}

// TestDiscoveryExactLookup never reads the entire inventory and preserves explicit missing/backend outcomes.
func TestDiscoveryExactLookup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"found", nil, 200},
		{"missing", apierrors.NewNotFound(schema.GroupResource{Group: "data.devantler.tech", Resource: "dataproducts"}, "customer-catalog"), 404},
		{"backend", errors.New("private-cluster-sentinel"), 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := discoveryReader{
				get: func(ctx context.Context, key client.ObjectKey, out client.Object, _ ...client.GetOption) error {
					if key != (client.ObjectKey{Namespace: "products", Name: "customer-catalog"}) {
						t.Fatalf("wrong exact read: %+v", key)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("exact read is unbounded")
					}
					registryProduct().DeepCopyInto(discoveryProductObject(t, out))
					return test.err
				},
			}
			got := discoveryRequest(
				t,
				discoveryHandler(reader, true),
				"/api/v2/products/products/customer-catalog",
			)
			if got.Code != test.status ||
				strings.Contains(got.Body.String(), "private-cluster-sentinel") {
				t.Fatalf("lookup = %d %s", got.Code, got.Body.String())
			}
		})
	}
	for _, target := range []string{"/api/v2/products/Bad/product", "/api/v2/products/products/Bad", "/api/v2/products/products/name?limit=1"} {
		got := discoveryRequest(t, discoveryHandler(discoveryReader{}, true), target)
		if got.Code != 400 {
			t.Fatalf("invalid exact lookup = %d %s", got.Code, got.Body.String())
		}
	}
}

// TestDiscoveryFailureClasses prevents backend failure, deadline, cancellation and expiry from looking like an empty final page.
func TestDiscoveryFailureClasses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"backend", errors.New("private-error-sentinel"), 503, "backend-unavailable"},
		{"deadline", context.DeadlineExceeded, 504, "deadline-exceeded"},
		{"canceled", context.Canceled, 408, "request-canceled"},
		{"expired", apierrors.NewResourceExpired("private-error-sentinel"), 410, "continuation-expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := discoveryReader{
				list: func(context.Context, client.ObjectList, ...client.ListOption) error { return test.err },
			}
			got := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products")
			if got.Code != test.status || !strings.Contains(got.Body.String(), test.code) ||
				strings.Contains(got.Body.String(), "private-error-sentinel") ||
				strings.Contains(got.Body.String(), `"products"`) {
				t.Fatalf("wrong failure: %d %s", got.Code, got.Body.String())
			}
		})
	}
}

// TestDiscoveryOversizedDescriptorsFailBeforeWriting catches partial JSON publication and silent field truncation.
func TestDiscoveryOversizedDescriptorsFailBeforeWriting(t *testing.T) {
	t.Parallel()
	product := registryProduct()
	product.Spec.Description = strings.Repeat("x", 65537)
	reader := discoveryReader{
		get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			product.DeepCopyInto(discoveryProductObject(t, out))
			return nil
		},
	}
	got := discoveryRequest(
		t,
		discoveryHandler(reader, true),
		"/api/v2/products/products/customer-catalog",
	)
	if got.Code != http.StatusRequestEntityTooLarge ||
		strings.Contains(got.Body.String(), "customer-catalog") {
		t.Fatalf("oversized descriptor escaped: %d %s", got.Code, got.Body.String())
	}
}

// TestLegacyCollectionFailsExplicitlyWhenIncomplete prevents bounded v1 reads from masquerading as a complete inventory.
func TestLegacyCollectionFailsExplicitlyWhenIncomplete(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(ctx context.Context, out client.ObjectList, opts ...client.ListOption) error {
			options := (&client.ListOptions{}).ApplyOptions(opts)
			if options.Limit == 0 || options.Limit > 100 {
				t.Fatalf("legacy read limit=%d", options.Limit)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("legacy read lacks deadline")
			}
			discoveryProductList(t, out).Continue = "more"
			discoveryProductList(t, out).Items = []datav1alpha1.DataProduct{
				*registryProduct(),
			}
			return nil
		},
	}
	got := discoveryRequest(t, NewHandler(reader), "/api/v1/products")
	if got.Code != http.StatusRequestEntityTooLarge ||
		strings.Contains(got.Body.String(), "customer-catalog") {
		t.Fatalf("legacy published incomplete inventory: %d %s", got.Code, got.Body.String())
	}
}

// TestLegacyReadFailurePreservesCompatibility keeps v1 backend errors unchanged and private.
func TestLegacyReadFailurePreservesCompatibility(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(context.Context, client.ObjectList, ...client.ListOption) error {
			return errors.New("private-sentinel")
		},
	}
	response := discoveryRequest(t, NewHandler(reader), "/api/v1/products")
	if response.Code != http.StatusInternalServerError ||
		strings.Contains(response.Body.String(), "private-sentinel") {
		t.Fatalf("legacy error changed or leaked: %d %s", response.Code, response.Body.String())
	}
}

// TestDiscoveryHealthIsCurrentAndPublic rejects stale success and arbitrary status messages in the new descriptor.
func TestDiscoveryHealthIsCurrentAndPublic(t *testing.T) {
	t.Parallel()
	product := registryProduct()
	product.Generation = 4
	product.Spec.Source = &datav1alpha1.ProvisionedSource{}
	product.Spec.Connector = &datav1alpha1.Connector{}
	product.Spec.ContractChecks = []datav1alpha1.ContractCheck{{Output: "query"}}
	product.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             "True",
			ObservedGeneration: 3,
			Reason:             "private-sentinel",
			Message:            "private-sentinel",
		},
		{Type: "SourceReady", Status: "True", ObservedGeneration: 3, Message: "private-sentinel"},
		{
			Type:               "ConnectorReady",
			Status:             "False",
			ObservedGeneration: 4,
			Reason:             "ConnectorFeatureDisabled",
			Message:            "private-sentinel",
		},
		{
			Type:               "ContractsReady",
			Status:             "False",
			ObservedGeneration: 4,
			Reason:             "Broken",
			Message:            "private-sentinel",
		},
	}
	reader := discoveryReader{
		get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			product.DeepCopyInto(discoveryProductObject(t, out))
			return nil
		},
	}
	got := discoveryRequest(
		t,
		discoveryHandler(reader, true),
		"/api/v2/products/products/customer-catalog",
	)
	var descriptor struct {
		Ready              bool  `json:"ready"`
		Generation         int64 `json:"generation"`
		ObservedGeneration int64 `json:"observedGeneration"`
		Health             map[string]struct {
			State              string `json:"state"`
			Generation         int64  `json:"generation"`
			ObservedGeneration int64  `json:"observedGeneration"`
		} `json:"health"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &descriptor) != nil {
		t.Fatalf("health descriptor = %d %s", got.Code, got.Body.String())
	}
	if descriptor.Ready || descriptor.Generation != 4 || descriptor.ObservedGeneration != 3 ||
		strings.Contains(got.Body.String(), "private-sentinel") {
		t.Fatalf("unsafe health: %s", got.Body.String())
	}
	for dimension, state := range map[string]string{"source": "stale", "connector": "disabled", "contracts": "not-ready", "composition": "not-applicable"} {
		if descriptor.Health[dimension].State != state ||
			descriptor.Health[dimension].Generation != 4 {
			t.Fatalf("%s health=%+v, want %s", dimension, descriptor.Health[dimension], state)
		}
	}
}
