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

// TestAppearanceConfiguration requires both gates and keeps the configuration unavailable with the UI off.
func TestAppearanceConfiguration(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, ui := range []bool{false, true} {
		for _, contract := range []bool{false, true} {
			for _, appearance := range []bool{false, true} {
				handler := NewHandlerWithOptions(
					fake.NewClientBuilder().WithScheme(scheme).Build(),
					HandlerOptions{
						UIEnabled:         func(context.Context) bool { return ui },
						ContractEnabled:   func(context.Context) bool { return contract },
						AppearanceEnabled: func(context.Context) bool { return appearance },
					},
				)
				response := httptest.NewRecorder()
				handler.ServeHTTP(
					response,
					httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/ui-config", nil),
				)
				if !ui {
					if response.Code != 404 {
						t.Fatal("disabled UI exposed presentation configuration")
					}
					continue
				}
				var config map[string]bool
				if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
					t.Fatal(err)
				}
				if len(config) != 2 || config["uiAppearanceEnabled"] != (contract && appearance) ||
					config["uiContractEnabled"] != contract {
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
}
