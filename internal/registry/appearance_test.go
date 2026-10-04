package registry

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAppearanceConfiguration requires both optional presentation gates in the standard workspace.
func TestAppearanceConfiguration(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, contract := range []bool{false, true} {
		for _, appearance := range []bool{false, true} {
			handler := NewHandlerWithOptions(
				fake.NewClientBuilder().WithScheme(scheme).Build(),
				HandlerOptions{
					ContractEnabled:   func(context.Context) bool { return contract },
					AppearanceEnabled: func(context.Context) bool { return appearance },
				},
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/ui-config", nil),
			)
			if response.Code != 200 {
				t.Fatalf("presentation configuration unavailable: %d", response.Code)
			}
			var config map[string]bool
			if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
				t.Fatal(err)
			}
			if len(config) != 4 || config["uiAppearanceEnabled"] != (contract && appearance) ||
				config["uiContractEnabled"] != contract || config["discoveryEnabled"] || config["lineageEnabled"] {
				t.Fatalf(
					"incorrect presentation policy for contract=%t appearance=%t: %v",
					contract,
					appearance,
					config,
				)
			}
		}
	}
}
