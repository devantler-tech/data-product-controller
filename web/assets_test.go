package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestKitReleaseGate ensures the independent host exposes no UI while disabled and keeps its sandbox policy.
func TestKitReleaseGate(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		for _, path := range []string{"/", "/ui-contract.js", "/kit.js"} {
			response := httptest.NewRecorder()
			KitHandler(
				func() bool { return enabled },
			).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
			want := http.StatusNotFound
			if enabled {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("enabled=%t path=%s status=%d", enabled, path, response.Code)
			}
			if enabled &&
				(!strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'none'") || response.Header().Get("Referrer-Policy") != "no-referrer") {
				t.Fatal("host policy missing")
			}
		}
	}
}
