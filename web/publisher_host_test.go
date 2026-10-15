package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPublisherHostBoundary catches accidentally enabling legacy routes or retaining assets after gate withdrawal.
func TestPublisherHostBoundary(t *testing.T) {
	enabled := true
	handler := PublisherHandler(func() bool { return enabled })
	for _, state := range []bool{true, false} {
		enabled = state
		for _, path := range []string{"/", "/index.html", "/publisher-review", "/publisher.html", "/publisher.js", "/publisher.css", "/preflight-report.js", "/metadata.js", "/descriptor.js", "/ui-contract.js", "/kit.js", "/kit.css", "/api/products", "/missing"} {
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(
					response,
					httptest.NewRequestWithContext(t.Context(), method, path+"?ignored=1", nil),
				)
				want := http.StatusNotFound
				allowed := path != "/ui-contract.js" && path != "/kit.js" && path != "/kit.css" &&
					path != "/api/products" &&
					path != "/missing"
				if state && allowed && method != http.MethodPost {
					want = http.StatusOK
					if path == "/" || path == "/index.html" {
						want = http.StatusTemporaryRedirect
					}
				}
				if response.Code != want {
					t.Fatalf(
						"enabled=%t %s %s: status=%d want=%d",
						state,
						method,
						path,
						response.Code,
						want,
					)
				}
				if want != http.StatusNotFound {
					if !strings.Contains(
						response.Header().Get("Content-Security-Policy"),
						"frame-src 'none'",
					) ||
						!strings.Contains(
							response.Header().Get("Content-Security-Policy"),
							"connect-src 'none'",
						) ||
						response.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("publisher host lost offline policy")
					}
					if method == http.MethodHead && response.Body.Len() != 0 {
						t.Fatal("HEAD exposed a body")
					}
					if want == http.StatusTemporaryRedirect &&
						response.Header().Get("Location") != "/publisher-review" {
						t.Fatal("redirect echoed request input")
					}
				}
			}
		}
	}
	response := httptest.NewRecorder()
	PublisherHandler(
		nil,
	).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/publisher-review", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal("nil release gate enabled publisher review")
	}
}
