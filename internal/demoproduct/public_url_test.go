package demoproduct_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
)

// TestPublicURLPolicy binds reads to configured endpoints rather than attacker-controlled host headers.
func TestPublicURLPolicy(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"https://sample.example", "https://sample.example:8443/products/harbour"} {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			handler, err := demoproduct.NewHandlerWithPublicURL(base)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequestWithContext(
				t.Context(),
				"GET",
				"https://untrusted.example/ui",
				nil,
			)
			request.Header.Set("X-Forwarded-Host", "other.example")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			policy := response.Header().Get("Content-Security-Policy")
			if !strings.Contains(
				policy,
				"connect-src "+base+"/api/observations "+base+"/ui-contract-config;",
			) {
				t.Fatalf("policy does not admit the configured read paths: %s", policy)
			}
			for _, forbidden := range []string{"untrusted.example", "other.example", "connect-src 'self'", "unsafe-inline", "unsafe-eval"} {
				if strings.Contains(policy, forbidden) {
					t.Fatalf("policy admits %q: %s", forbidden, policy)
				}
			}
		})
	}
}

// TestPublicURLValidation rejects credentials, ambiguous paths and CSP-injection inputs before serving.
func TestPublicURLValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"http://sample.example", "https://user:secret@sample.example", "https://*.example",
		"https://sample.example/", "https://sample.example//other", "https://sample.example/a/../b",
		"https://sample.example/a%2Fb", "https://sample.example?", "https://sample.example#",
		"https://sample.example;connect-src", "https://sample.example/path; https://other.example",
		"https://sample.example,https://other.example", "https://sample.example:0",
		"https://sample.example/" + strings.Repeat("a", 1024),
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			handler, err := demoproduct.NewHandlerWithPublicURL(value)
			if err == nil || handler != nil {
				t.Fatal("invalid public URL admitted")
			}
		})
	}
	handler, err := demoproduct.NewHandlerWithPublicURL("")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/ui", nil))
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'self';") {
		t.Fatal("unconfigured standalone policy changed")
	}
}
