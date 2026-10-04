package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPublisherReviewGate keeps report inspection independent of product-session permission.
func TestPublisherReviewGate(t *testing.T) {
	for _, contract := range []bool{false, true} {
		for _, publisher := range []bool{false, true} {
			for _, path := range []string{"/publisher-review", "/publisher.html", "/publisher.css", "/publisher.js", "/preflight-report.js"} {
				response := httptest.NewRecorder()
				KitHandlerWithOptions(KitOptions{
					ContractEnabled:  func() bool { return contract },
					PublisherEnabled: func() bool { return publisher },
				}).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
				want := http.StatusNotFound
				if publisher {
					want = http.StatusOK
				}
				if response.Code != want {
					t.Fatalf("contract=%t publisher=%t path=%s: %d", contract, publisher, path, response.Code)
				}
				if publisher && (!strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-src 'none'") ||
					!strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'none'") ||
					response.Header().Get("Cache-Control") != "no-store") {
					t.Fatal("report host permits an active product surface or retained response")
				}
			}
		}
	}
	response := httptest.NewRecorder()
	KitHandlerWithOptions(KitOptions{}).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/publisher-review", nil))
	if response.Code != 404 {
		t.Fatal("nil publisher flag enables review")
	}
}
