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

// TestKitAppearanceGate requires operator opt-in and does not broaden the host's network policy.
func TestKitAppearanceGate(t *testing.T) {
	t.Parallel()
	for _, contract := range []bool{false, true} {
		for _, appearance := range []bool{false, true} {
			response := httptest.NewRecorder()
			KitHandlerWithAppearance(
				func() bool { return contract },
				func() bool { return appearance },
			).ServeHTTP(response,
				httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
			if !contract {
				if response.Code != 404 {
					t.Fatal("disabled kit is available")
				}
				continue
			}
			if strings.Contains(
				response.Body.String(),
				`data-appearance-enabled="true"`,
			) != appearance {
				t.Fatal("kit appearance grant does not reflect its explicit gate")
			}
			if !strings.Contains(
				response.Header().Get("Content-Security-Policy"),
				"connect-src 'none'",
			) {
				t.Fatal("appearance broadened kit connections")
			}
		}
	}
}
