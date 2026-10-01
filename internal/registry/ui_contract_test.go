package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestUIContractDefaultsOff makes legacy handler construction expose a disabled release capability.
func TestUIContractDefaultsOff(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		fake.NewClientBuilder().WithScheme(scheme).Build(),
		func(context.Context) bool { return true },
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/ui-config", nil),
	)
	var config struct {
		UIContractEnabled *bool `json:"uiContractEnabled"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatalf("UI configuration unavailable: %v", err)
	}
	if config.UIContractEnabled == nil || *config.UIContractEnabled {
		t.Fatal("UI contract must default off")
	}
}

// TestUIContractHostConfiguration keeps release capability separate from registry availability.
func TestUIContractHostConfiguration(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, registryEnabled := range []bool{false, true} {
		for _, contractEnabled := range []bool{false, true} {
			handler := NewHandler(
				fake.NewClientBuilder().WithScheme(scheme).Build(),
				func(context.Context) bool { return registryEnabled },
				func(context.Context) bool { return contractEnabled },
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					"/api/v1/ui-config",
					nil,
				),
			)
			if !registryEnabled {
				if response.Code != http.StatusNotFound {
					t.Fatal("disabled registry exposes host configuration")
				}
				continue
			}
			var configuration map[string]bool
			if err := json.Unmarshal(response.Body.Bytes(), &configuration); err != nil {
				t.Fatal(err)
			}
			if len(configuration) != 2 || configuration["uiContractEnabled"] != contractEnabled ||
				configuration["uiAppearanceEnabled"] ||
				response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("configuration does not reflect the current release flag")
			}
		}
	}
}
