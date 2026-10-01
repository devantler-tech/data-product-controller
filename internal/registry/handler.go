// Package registry exposes portable product descriptors and the reference registry UI.
package registry

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"sort"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/uibundle"
	"github.com/devantler-tech/data-product-controller/web"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed ui/*
var uiFiles embed.FS

// HandlerOptions controls the optional portable UI presentation grants.
type HandlerOptions struct {
	ContractEnabled   func(context.Context) bool
	AppearanceEnabled func(context.Context) bool
}

// NewHandler builds the registry API and UI with optional presentation grants disabled.
func NewHandler(reader client.Reader) http.Handler {
	return NewHandlerWithOptions(reader, HandlerOptions{})
}

// NewHandlerWithOptions builds the registry with independently evaluated presentation gates.
func NewHandlerWithOptions(reader client.Reader, options HandlerOptions) http.Handler {
	bundle, bundleErr := uibundle.Load(
		uiFiles,
		"ui/index.html",
		uibundle.Source{
			FS:          uiFiles,
			Path:        "ui/registry.css",
			ContentType: "text/css; charset=utf-8",
		},
		uibundle.Source{
			FS:          uiFiles,
			Path:        "ui/registry.js",
			ContentType: "text/javascript; charset=utf-8",
		},
		uibundle.Source{
			FS:          web.Assets,
			Path:        "ui-contract.js",
			ContentType: "text/javascript; charset=utf-8",
		},
	)
	server := &server{
		reader: reader, contractEnabled: options.ContractEnabled,
		appearanceEnabled: options.AppearanceEnabled, bundle: bundle, bundleErr: bundleErr,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/products", server.listProducts)
	mux.HandleFunc("GET /api/v1/ui-config", server.uiConfig)
	mux.HandleFunc("GET /", server.registryUI)
	mux.HandleFunc("GET /assets/{asset}", server.registryAsset)

	return mux
}

// uiConfig advertises only current release capability, never user or credential context.
func (s *server) uiConfig(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	contractEnabled := s.contractEnabled != nil && s.contractEnabled(request.Context())
	_ = json.NewEncoder(writer).Encode(struct {
		UIContractEnabled   bool `json:"uiContractEnabled"`
		UIAppearanceEnabled bool `json:"uiAppearanceEnabled"`
	}{
		UIContractEnabled: contractEnabled,
		UIAppearanceEnabled: contractEnabled &&
			s.appearanceEnabled != nil && s.appearanceEnabled(request.Context()),
	})
}

// registryUI serves the current workspace document.
func (s *server) registryUI(writer http.ResponseWriter, request *http.Request) {
	if s.bundleErr != nil {
		http.Error(writer, "Unable to load the registry UI.", http.StatusInternalServerError)

		return
	}

	setUISecurityHeaders(writer)
	s.bundle.ServeHTML(writer)
}

// registryAsset resolves only an exact compiled asset name.
func (s *server) registryAsset(writer http.ResponseWriter, request *http.Request) {
	if s.bundleErr != nil {
		http.Error(writer, "Unable to load the registry UI.", http.StatusInternalServerError)
		return
	}
	setUISecurityHeaders(writer)
	s.bundle.ServeAsset(writer, request, request.PathValue("asset"))
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
	bundle            *uibundle.Bundle
	bundleErr         error
	reader            client.Reader
	contractEnabled   func(context.Context) bool
	appearanceEnabled func(context.Context) bool
}

type productCollection struct {
	Products []productDescriptor `json:"products"`
}

type productDescriptor struct {
	Namespace        string                     `json:"namespace"`
	Name             string                     `json:"name"`
	ID               string                     `json:"id"`
	DisplayName      string                     `json:"displayName"`
	Description      string                     `json:"description"`
	Version          string                     `json:"version"`
	Owner            datav1alpha1.ProductOwner  `json:"owner"`
	DocumentationURL string                     `json:"documentationUrl,omitempty"`
	Inputs           []datav1alpha1.InputPort   `json:"inputs,omitempty"`
	Outputs          []datav1alpha1.OutputPort  `json:"outputs"`
	UI               *datav1alpha1.ProductUI    `json:"ui,omitempty"`
	Ready            bool                       `json:"ready"`
	Readiness        readinessDescriptor        `json:"readiness"`
	Composition      *readinessDescriptor       `json:"composition,omitempty"`
	Lineage          []datav1alpha1.InputStatus `json:"lineage,omitempty"`
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

// descriptorFor projects published metadata while withholding readiness and lineage from stale generations.
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

	var composition *readinessDescriptor
	var lineage []datav1alpha1.InputStatus
	if observed := meta.FindStatusCondition(
		product.Status.Conditions,
		datav1alpha1.ConditionCompositionReady,
	); observed != nil {
		composition = &readinessDescriptor{
			Reason:  "StatusStale",
			Message: "Composition has not been observed for the current product generation.",
		}
		if observed.ObservedGeneration == product.Generation {
			composition.Reason, composition.Message = observed.Reason, observed.Message
			lineage = product.Status.Inputs
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
		Composition:      composition,
		Lineage:          lineage,
	}
}
