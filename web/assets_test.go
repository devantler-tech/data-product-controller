package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestCompatibilityAssetHTTP preserves complete single-script delivery for GET, HEAD and range clients.
func TestCompatibilityAssetHTTP(t *testing.T) {
	t.Parallel()
	handler := KitHandler(func() bool { return true })
	full := httptest.NewRecorder()
	handler.ServeHTTP(
		full,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ui-contract.js", nil),
	)
	if full.Code != http.StatusOK ||
		full.Header().Get("Content-Length") != strconv.Itoa(full.Body.Len()) ||
		!strings.Contains(full.Body.String(), "window.DataProductMetadata =") ||
		!strings.Contains(full.Body.String(), "window.DataProductUI =") {
		t.Fatal("compatibility response does not contain both complete public libraries")
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(
		head,
		httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/ui-contract.js", nil),
	)
	if head.Code != http.StatusOK || head.Body.Len() != 0 ||
		head.Header().Get("Content-Length") != full.Header().Get("Content-Length") {
		t.Fatal("HEAD does not describe the complete compatibility response")
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ui-contract.js", nil)
	request.Header.Set("Range", "bytes=0-63")
	part := httptest.NewRecorder()
	handler.ServeHTTP(part, request)
	if part.Code != http.StatusPartialContent || part.Body.String() != full.Body.String()[:64] ||
		part.Header().Get("Content-Range") != "bytes 0-63/"+strconv.Itoa(full.Body.Len()) {
		t.Fatal("range delivery truncated or changed the composed compatibility response")
	}
}

// TestPublisherAssetBoundary exposes inert inspection assets without presentation execution.
func TestPublisherAssetBoundary(t *testing.T) {
	t.Parallel()
	for _, contract := range []bool{false, true} {
		for _, path := range []string{"/metadata.js", "/descriptor.js", "/ui-contract.js", "/kit.js"} {
			response := httptest.NewRecorder()
			KitHandlerWithOptions(KitOptions{
				PublisherEnabled: func() bool { return true },
				ContractEnabled:  func() bool { return contract },
			}).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
			want := http.StatusOK
			if !contract && (path == "/ui-contract.js" || path == "/kit.js") {
				want = http.StatusNotFound
			}
			if response.Code != want {
				t.Errorf(
					"contract=%t path=%s status=%d want=%d",
					contract,
					path,
					response.Code,
					want,
				)
			}
		}
	}
}

// TestKitReleaseGate ensures the independent host exposes no UI while disabled and keeps its sandbox policy.
func TestKitReleaseGate(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		for _, path := range []string{"/", "/metadata.js", "/ui-contract.js", "/kit.js"} {
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
