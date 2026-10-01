//go:build browser

package browser_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestUIUpgrade bypasses an intermediary retaining the previous release's shared asset URLs.
func TestUIUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(workspaceProduct("https://example.com")).Build()
	for _, test := range []struct {
		name, path, style string
		scripts           []string
		handler           http.Handler
	}{
		{"registry", "/", "registry.css", []string{"registry.js", "ui-contract.js"}, registry.NewHandler(reader, func(_ context.Context) bool { return true })},
		{"sample", "/ui", "product.css", []string{"product.js"}, demoproduct.NewHandler()},
	} {
		t.Run(test.name, func(t *testing.T) {
			var upgraded atomic.Bool
			legacy := `<h1>Previous release</h1><link rel="stylesheet" href="/assets/` + test.style + `">`
			for _, script := range test.scripts {
				legacy += `<script src="/assets/` + script + `"></script>`
			}
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// The old shared URLs remain stale even after the origin upgrades, as a CDN may retain them.
					if r.URL.Path == "/assets/"+test.style {
						w.Header().Set("Content-Type", "text/css")
						w.Header().Set("Cache-Control", "public, max-age=14400")
						_, _ = w.Write([]byte(`main { min-width: 2000px; }`))
						return
					}
					for _, script := range test.scripts {
						if r.URL.Path == "/assets/"+script {
							w.Header().Set("Content-Type", "text/javascript")
							w.Header().Set("Cache-Control", "public, max-age=14400")
							_, _ = w.Write(
								[]byte(`document.documentElement.dataset.legacyAsset = "cached";`),
							)
							return
						}
					}
					if !upgraded.Load() {
						w.Header().Set("Content-Type", "text/html")
						_, _ = w.Write([]byte(legacy))
						return
					}
					test.handler.ServeHTTP(w, r)
				}),
			)
			t.Cleanup(server.Close)
			page := contractBrowser(
				t,
			).MustPage().
				MustNavigate(server.URL + test.path).
				MustWaitLoad()
			if !page.MustEval(`() => document.documentElement.dataset.legacyAsset === "cached"`).
				Bool() {
				t.Fatal(
					"previous-release asset was not loaded; upgrade test did not prime the cache",
				)
			}
			upgraded.Store(true)
			page.MustNavigate(server.URL + test.path + "?release=current").MustWaitLoad()
			if page.MustEval(`() => document.documentElement.dataset.legacyAsset === "cached"`).
				Bool() {
				t.Fatal("upgraded page executed the previous release's cached JavaScript")
			}
			if test.name == "registry" {
				page.MustElement(".product-card").MustWaitVisible()
				page.MustElement("#product-search").MustInput("missing")
				page.MustElement("#registry-status").
					MustWait(`() => this.textContent.includes('No products match')`)
			} else {
				page.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
				if !strings.Contains(page.MustElement("table").MustText(), "Observed at") {
					t.Fatal("upgraded sample did not render its current observation table")
				}
			}
			page.MustSetViewport(375, 812, 1, false)
			if page.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
				t.Fatal("upgraded page still uses the cached previous-release stylesheet")
			}
		})
	}
}
