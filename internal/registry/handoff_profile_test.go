package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestDiscoveryRejectsUnknownLengthBody covers body-bearing HTTP/2 GETs before any metadata read.
func TestDiscoveryRejectsUnknownLengthBody(t *testing.T) {
	t.Parallel()
	source := discoveryReader{
		list: func(context.Context, client.ObjectList, ...client.ListOption) error {
			t.Error("body-bearing request read inventory")
			return nil
		},
		get: func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
			t.Error("body-bearing request read a product")
			return nil
		},
	}
	for _, path := range []string{"/api/v2/products", "/api/v2/products/products/customer-catalog", "/api/v2/schema"} {
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			path,
			strings.NewReader("body"),
		)
		request.ContentLength = -1
		request.TransferEncoding = nil
		response := httptest.NewRecorder()
		discoveryHandler(source, true).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d, want 400", path, response.Code)
		}
		disabled := httptest.NewRecorder()
		discoveryHandler(source, false).ServeHTTP(disabled, request)
		if disabled.Code != http.StatusNotFound {
			t.Errorf("disabled %s status=%d", path, disabled.Code)
		}
	}
}

// TestPortableDescriptorUniqueCollections enforces separate name identities before API publication.
func TestPortableDescriptorUniqueCollections(t *testing.T) {
	t.Parallel()
	for _, collection := range []string{"outputs", "inputs", "lineage"} {
		t.Run(collection, func(t *testing.T) {
			t.Parallel()
			product := traceProduct("root")
			ref := data.ProductReference{Name: "producer", Output: "query"}
			product.Spec.Inputs = []data.InputPort{{Name: "upstream", ProductRef: ref}}
			product.Status.Inputs = []data.InputStatus{
				{Name: "upstream", ProductRef: ref, Ready: true},
			}
			meta.SetStatusCondition(
				&product.Status.Conditions,
				metav1.Condition{
					Type:               data.ConditionCompositionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "CompositionReady",
					ObservedGeneration: product.Generation,
				},
			)
			switch collection {
			case "outputs":
				product.Spec.Outputs = append(product.Spec.Outputs, product.Spec.Outputs[0])
			case "inputs":
				product.Spec.Inputs = append(product.Spec.Inputs, product.Spec.Inputs[0])
			case "lineage":
				product.Status.Inputs = append(product.Status.Inputs, product.Status.Inputs[0])
			}
			if _, err := encodePortableDescriptor(product); err == nil {
				t.Fatal("published ambiguous " + collection)
			}
		})
	}
}
