// Package registry exposes portable product descriptors and the optional registry UI.
package registry

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"sort"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed ui/*
var uiFiles embed.FS

// NewHandler builds the registry API and optional UI handler.
func NewHandler(reader client.Reader, uiEnabled func(context.Context) bool) http.Handler {
	server := &server{reader: reader, uiEnabled: uiEnabled}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/products", server.listProducts)
	mux.HandleFunc("GET /", server.registryUI)
	mux.HandleFunc("GET /assets/{asset}", server.registryAsset)

	return mux
}

func (s *server) registryUI(writer http.ResponseWriter, request *http.Request) {
	if !s.uiEnabled(request.Context()) {
		http.NotFound(writer, request)

		return
	}

	contents, err := uiFiles.ReadFile("ui/index.html")
	if err != nil {
		http.Error(writer, "Unable to load the registry UI.", http.StatusInternalServerError)

		return
	}

	setUISecurityHeaders(writer)
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write(contents)
}

func (s *server) registryAsset(writer http.ResponseWriter, request *http.Request) {
	if !s.uiEnabled(request.Context()) {
		http.NotFound(writer, request)

		return
	}

	asset := request.PathValue("asset")
	contentType := map[string]string{
		"registry.css": "text/css; charset=utf-8",
		"registry.js":  "text/javascript; charset=utf-8",
	}[asset]
	if contentType == "" {
		http.NotFound(writer, request)

		return
	}

	contents, err := uiFiles.ReadFile("ui/" + asset)
	if err != nil {
		http.NotFound(writer, request)

		return
	}

	setUISecurityHeaders(writer)
	writer.Header().Set("Content-Type", contentType)
	_, _ = writer.Write(contents)
}

func setUISecurityHeaders(writer http.ResponseWriter) {
	writer.Header().Set(
		"Content-Security-Policy",
		"default-src 'self'; connect-src 'self'; frame-src https:; frame-ancestors 'none'; object-src 'none'; base-uri 'none'",
	)
	writer.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

type server struct {
	reader    client.Reader
	uiEnabled func(context.Context) bool
}

type productCollection struct {
	Products []productDescriptor `json:"products"`
}

type productDescriptor struct {
	Namespace        string                    `json:"namespace"`
	Name             string                    `json:"name"`
	ID               string                    `json:"id"`
	DisplayName      string                    `json:"displayName"`
	Description      string                    `json:"description"`
	Version          string                    `json:"version"`
	Owner            datav1alpha1.ProductOwner `json:"owner"`
	DocumentationURL string                    `json:"documentationUrl,omitempty"`
	Inputs           []datav1alpha1.InputPort  `json:"inputs,omitempty"`
	Outputs          []datav1alpha1.OutputPort `json:"outputs"`
	UI               *datav1alpha1.ProductUI   `json:"ui,omitempty"`
	Ready            bool                      `json:"ready"`
	Readiness        readinessDescriptor       `json:"readiness"`
}

type readinessDescriptor struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (s *server) listProducts(writer http.ResponseWriter, request *http.Request) {
	products := &datav1alpha1.DataProductList{}
	if err := s.reader.List(request.Context(), products); err != nil {
		http.Error(writer, "Unable to read data products.", http.StatusInternalServerError)

		return
	}

	sort.Slice(products.Items, func(left, right int) bool {
		leftKey := products.Items[left].Namespace + "/" + products.Items[left].Name
		rightKey := products.Items[right].Namespace + "/" + products.Items[right].Name

		return leftKey < rightKey
	})

	descriptors := make([]productDescriptor, 0, len(products.Items))
	for index := range products.Items {
		descriptors = append(descriptors, descriptorFor(&products.Items[index]))
	}

	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(productCollection{Products: descriptors}); err != nil {
		http.Error(writer, "Unable to encode data products.", http.StatusInternalServerError)
	}
}

func descriptorFor(product *datav1alpha1.DataProduct) productDescriptor {
	condition := meta.FindStatusCondition(product.Status.Conditions, datav1alpha1.ConditionReady)
	readiness := readinessDescriptor{
		Reason:  "Unknown",
		Message: "The controller has not reported readiness yet.",
	}
	ready := false
	if condition != nil {
		if condition.ObservedGeneration != product.Generation {
			readiness.Reason = "StatusStale"
			readiness.Message = "The controller has not reconciled the current product generation."
		} else {
			readiness.Reason = condition.Reason
			readiness.Message = condition.Message
			ready = condition.Status == "True"
		}
	}

	return productDescriptor{
		Namespace:        product.Namespace,
		Name:             product.Name,
		ID:               product.Spec.ID,
		DisplayName:      product.Spec.Name,
		Description:      product.Spec.Description,
		Version:          product.Spec.Version,
		Owner:            product.Spec.Owner,
		DocumentationURL: product.Spec.DocumentationURL,
		Inputs:           product.Spec.Inputs,
		Outputs:          product.Spec.Outputs,
		UI:               product.Spec.UI,
		Ready:            ready,
		Readiness:        readiness,
	}
}
