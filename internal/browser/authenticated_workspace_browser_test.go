//go:build browser

package browser_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAuthenticatedWorkspace keeps the catalog session on its own API while opening a credential-free product.
func TestAuthenticatedWorkspace(t *testing.T) {
	for _, version := range []string{"data-product-ui/v1", "data-product-ui/v2"} {
		t.Run(version, func(t *testing.T) {
			var productHandler http.Handler
			var leakedCredentials atomic.Bool
			product := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						leakedCredentials.Store(true)
					}
					productHandler.ServeHTTP(w, r)
				}),
			)
			t.Cleanup(product.Close)
			// Different hostnames model independent publisher and catalog cookie scopes.
			productOrigin := strings.Replace(product.URL, "127.0.0.1", "localhost", 1)
			var registryHandler http.Handler
			catalog := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/" {
						http.SetCookie(w, &http.Cookie{
							Name: "workspace-session", Value: "fixture-authenticated", Path: "/",
							Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
						})
					} else {
						cookie, err := r.Cookie("workspace-session")
						if err != nil || cookie.Value != "fixture-authenticated" {
							http.Error(w, "Sign in required", http.StatusUnauthorized)
							return
						}
					}
					registryHandler.ServeHTTP(w, r)
				}),
			)
			t.Cleanup(catalog.Close)
			appearance := version == "data-product-ui/v2"
			published := workspaceProduct(productOrigin)
			published.Spec.UI.Contract = &datav1alpha1.UIContract{
				APIVersion: version,
				HostOrigins: []datav1alpha1.UIHostOrigin{
					datav1alpha1.UIHostOrigin(catalog.URL),
				},
				Capabilities: []datav1alpha1.UICapability{"status", "resize"},
			}
			if appearance {
				published.Spec.UI.Contract.Capabilities = append(
					published.Spec.UI.Contract.Capabilities,
					"appearance",
				)
			}
			scheme := runtime.NewScheme()
			if err := datav1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(published).Build()
			registryHandler = registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
				UIEnabled:         func(context.Context) bool { return true },
				ContractEnabled:   func(context.Context) bool { return true },
				AppearanceEnabled: func(context.Context) bool { return appearance },
			})
			var err error
			productHandler, err = demoproduct.NewHandlerWithOptions(demoproduct.HandlerOptions{
				PublicBaseURL: productOrigin, HostOrigins: []string{catalog.URL},
				AppearanceEnabled: appearance,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Anonymous access must remain blocked even though the response contains only flags.
			response, err := catalog.Client().Get(catalog.URL + "/api/v1/ui-config")
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("fixture exposed the configuration without the catalog session")
			}
			page := contractBrowser(t).MustPage().Timeout(15 * time.Second).
				MustNavigate(catalog.URL).MustWaitLoad()
			page.MustElement(".product-card").MustClick()
			status := page.MustElement("#ui-contract-status").MustWait(
				`() => this.dataset.state === 'ready' || this.dataset.state === 'unavailable'`,
			)
			if *status.MustAttribute("data-state") != "ready" {
				t.Fatalf("authenticated product could not open: %s", status.MustText())
			}
			frame := page.MustElement("#product-surface").MustFrame()
			frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
			frame.MustElement("#station").MustSelect("Nordhavn")
			frame.MustElement("button[type=submit]").MustClick()
			frame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
			if appearance {
				page.MustElement("#appearance").MustSelect("Dark")
				waitTheme(t, page, frame, "rgb(0, 0, 0)")
				if frame.MustElement("#status").MustText() != "1 observation" {
					t.Fatal("authenticated appearance change lost the selected query")
				}
			}
			if leakedCredentials.Load() {
				t.Fatal("catalog session reached the independently served product")
			}
		})
	}
}
