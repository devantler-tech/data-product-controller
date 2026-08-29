package registry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProductRegistryReturnsPortableDescriptors(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register data-product API: %v", err)
	}
	product := registryProduct()
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
	handler := NewHandler(reader, func(context.Context) bool { return false })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	want := `{"products":[{"namespace":"products","name":"customer-catalog","id":"urn:devantler:data-product:customer-catalog","displayName":"Customer catalog","description":"Queryable customer records.","version":"v0.1.0","owner":{"name":"Customer data team","url":"https://example.test/owners/customer-data"},"documentationUrl":"https://example.test/docs/customer-catalog","outputs":[{"name":"query","protocol":"OpenAPI","url":"https://example.test/customer-catalog/query","contractUrl":"https://example.test/customer-catalog/openapi.json","mediaType":"application/json"}],"ui":{"url":"https://example.test/customer-catalog/ui","title":"Explore customer data"},"ready":true,"readiness":{"reason":"DependenciesReady","message":"All referenced data products and output ports are ready."}}]}` + "\n"
	if string(body) != want {
		t.Fatalf("response body =\n%s\nwant:\n%s", body, want)
	}
}

func TestRegistryUIFeatureFlagControlsTheUserSurface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enabled    bool
		wantStatus int
	}{
		{name: "disabled", enabled: false, wantStatus: http.StatusNotFound},
		{name: "enabled", enabled: true, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			if err := datav1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("register data-product API: %v", err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).Build()
			handler := NewHandler(reader, func(context.Context) bool { return test.enabled })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if !test.enabled {
				return
			}

			if got := response.Header().
				Get("Content-Security-Policy"); got != "default-src 'self'; connect-src 'self'; frame-src https:; frame-ancestors 'none'; object-src 'none'; base-uri 'none'" {
				t.Fatalf("Content-Security-Policy = %q", got)
			}
			body := response.Body.String()
			for _, required := range []string{
				"<title>Data product constellation</title>",
				`id="data-product-grid"`,
				`sandbox="allow-forms allow-scripts"`,
				`src="/assets/registry.js"`,
			} {
				if !strings.Contains(body, required) {
					t.Fatalf("UI body does not contain %q", required)
				}
			}
		})
	}
}

func registryProduct() *datav1alpha1.DataProduct {
	return &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{Name: "customer-catalog", Namespace: "products"},
		Spec: datav1alpha1.DataProductSpec{
			ID:          "urn:devantler:data-product:customer-catalog",
			Name:        "Customer catalog",
			Description: "Queryable customer records.",
			Version:     "v0.1.0",
			Owner: datav1alpha1.ProductOwner{
				Name: "Customer data team",
				URL:  "https://example.test/owners/customer-data",
			},
			DocumentationURL: "https://example.test/docs/customer-catalog",
			Outputs: []datav1alpha1.OutputPort{{
				Name:        "query",
				Protocol:    datav1alpha1.ProtocolOpenAPI,
				URL:         "https://example.test/customer-catalog/query",
				ContractURL: "https://example.test/customer-catalog/openapi.json",
				MediaType:   "application/json",
			}},
			UI: &datav1alpha1.ProductUI{
				URL:   "https://example.test/customer-catalog/ui",
				Title: "Explore customer data",
			},
		},
		Status: datav1alpha1.DataProductStatus{
			Conditions: []metav1.Condition{{
				Type:    datav1alpha1.ConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "DependenciesReady",
				Message: "All referenced data products and output ports are ready.",
			}},
		},
	}
}
