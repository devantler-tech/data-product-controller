package demoproduct_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devantler-tech/kweft/internal/demoproduct"
)

func TestKSailReleaseSurfaceServesWithoutOptIn(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/ksail-releases/ui", "/ksail-releases/openapi.json", "/ksail-releases/ui-contract-config", "/ksail-releases/assets/product.js"} {
		response := httptest.NewRecorder()
		demoproduct.NewHandler().
			ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("standard GET %s = %d, want 200", path, response.Code)
		}
	}
}

func TestKSailReleaseSurfacePublishesFixedPublicSource(t *testing.T) {
	t.Parallel()
	options := demoproduct.HandlerOptions{
		PublicBaseURL: "https://harbour.example.test",
	}
	handler, err := demoproduct.NewHandlerWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ksail-releases/ui", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("release UI = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "<title>KSail releases</title>") {
		t.Fatal("release surface has no product identity")
	}
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://api.github.com/repos/devantler-tech/ksail/releases") ||
		strings.Contains(csp, "connect-src *") {
		t.Fatalf("release CSP does not bind the fixed public source: %s", csp)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("release UI can outlive its compiled asset set")
	}
	contract := httptest.NewRecorder()
	handler.ServeHTTP(
		contract,
		httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/ksail-releases/openapi.json",
			nil,
		),
	)
	var document struct {
		OpenAPI string `json:"openapi"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(contract.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if contract.Code != http.StatusOK || document.OpenAPI != "3.1.0" ||
		len(document.Servers) != 1 ||
		document.Servers[0].URL != "https://api.github.com" ||
		document.Paths["/repos/devantler-tech/ksail/releases"] == nil {
		t.Fatal("contract does not describe the actual external release endpoint")
	}
	for _, path := range []string{"/ksail-releases/ui", "/ksail-releases/openapi.json", "/ksail-releases/ui-contract-config"} {
		denied := httptest.NewRecorder()
		handler.ServeHTTP(
			denied,
			httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil),
		)
		if denied.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s = %d, want 405", path, denied.Code)
		}
	}
}
