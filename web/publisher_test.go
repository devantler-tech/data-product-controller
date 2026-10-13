package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPublisherEntryPoints keeps root navigation independent from product-session permission.
func TestPublisherEntryPoints(t *testing.T) {
	for _, publisher := range []bool{false, true} {
		for _, contract := range []bool{false, true} {
			handler := KitHandlerWithOptions(KitOptions{
				PublisherEnabled: func() bool { return publisher },
				ContractEnabled:  func() bool { return contract },
			})
			for _, path := range []string{"/", "/index.html"} {
				for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
					response := httptest.NewRecorder()
					handler.ServeHTTP(
						response,
						httptest.NewRequestWithContext(t.Context(), method, path+"?ignored=1", nil),
					)
					want := http.StatusNotFound
					if method != http.MethodPost {
						if contract {
							want = http.StatusOK
						} else if publisher {
							want = http.StatusTemporaryRedirect
						}
					}
					if response.Code != want {
						t.Fatalf(
							"publisher=%t contract=%t %s %s: status=%d want=%d",
							publisher,
							contract,
							method,
							path,
							response.Code,
							want,
						)
					}
					if want == http.StatusNotFound {
						continue
					}
					if response.Header().Get("Cache-Control") != "no-store" ||
						!strings.Contains(
							response.Header().Get("Content-Security-Policy"),
							"connect-src 'none'",
						) {
						t.Fatal("entry point lost its offline/no-store policy")
					}
					if method == http.MethodHead && response.Body.Len() != 0 {
						t.Fatal("HEAD exposed an entry-point body")
					}
					if want == http.StatusTemporaryRedirect {
						if response.Header().Get("Location") != "/publisher-review" ||
							!strings.Contains(
								response.Header().Get("Content-Security-Policy"),
								"frame-src 'none'",
							) {
							t.Fatal("publisher-only entry granted a frame or echoed query input")
						}
					} else if method == http.MethodGet &&
						strings.Contains(
							response.Body.String(),
							`href="/publisher-review"`,
						) != publisher {
						t.Fatal("kit navigation did not reflect the publisher gate")
					}
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					"/publisher-review",
					nil,
				),
			)
			if publisher && (strings.Contains(response.Body.String(), `href="/"`) != contract ||
				(contract && !strings.Contains(response.Body.String(), `aria-current="page">Publisher review`))) {
				t.Fatal("publisher navigation lost current page or product-session gate")
			}
			for _, path := range []string{"/kit.js", "/kit.css"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(
					response,
					httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil),
				)
				if (response.Code == http.StatusOK) != contract {
					t.Fatal("publisher root enabled kit assets")
				}
			}
		}
	}
}

// TestPublisherNavigationRevocation reuses one handler across gate changes and nil callbacks.
func TestPublisherNavigationRevocation(t *testing.T) {
	publisher, contract := true, true
	handler := KitHandlerWithOptions(KitOptions{
		PublisherEnabled: func() bool { return publisher },
		ContractEnabled:  func() bool { return contract },
	})
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil),
		)
		return response
	}
	if !strings.Contains(request("/").Body.String(), `href="/publisher-review"`) {
		t.Fatal("enabled publisher link missing")
	}
	publisher = false
	if strings.Contains(request("/").Body.String(), `href="/publisher-review"`) ||
		request("/publisher-review").Code != 404 {
		t.Fatal("withdrawn publisher gate retained its entry point")
	}
	publisher, contract = true, false
	if request("/").Code != http.StatusTemporaryRedirect ||
		strings.Contains(request("/publisher-review").Body.String(), `href="/"`) {
		t.Fatal("publisher-only hosting retained product-session navigation")
	}
	for _, path := range []string{"/", "/index.html", "/publisher-review"} {
		response := httptest.NewRecorder()
		KitHandlerWithOptions(
			KitOptions{},
		).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatal("nil callbacks enabled a workspace")
		}
	}
}

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
					t.Fatalf(
						"contract=%t publisher=%t path=%s: %d",
						contract,
						publisher,
						path,
						response.Code,
					)
				}
				if publisher &&
					(!strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-src 'none'") ||
						!strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'none'") ||
						response.Header().Get("Cache-Control") != "no-store") {
					t.Fatal("report host permits an active product surface or retained response")
				}
			}
		}
	}
	response := httptest.NewRecorder()
	KitHandlerWithOptions(
		KitOptions{},
	).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/publisher-review", nil))
	if response.Code != 404 {
		t.Fatal("nil publisher flag enables review")
	}
}
