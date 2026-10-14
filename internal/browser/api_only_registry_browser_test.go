//go:build browser

package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/kweft/api/v1alpha1"
	"github.com/devantler-tech/kweft/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestRegistryAPIOnly catches publisher execution while inspecting API descriptors.
func TestRegistryAPIOnly(t *testing.T) {
	for _, version := range []string{"absent", "legacy", "data-product-ui/v1", "data-product-ui/v2"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/old-grants=%t", version, enabled), func(t *testing.T) {
				var requests atomic.Int32
				publisher := httptest.NewTLSServer(
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "text/html")
						_, _ = w.Write([]byte("<!doctype html><title>Publisher code</title>"))
					}),
				)
				t.Cleanup(publisher.Close)
				product := workspaceProduct(publisher.URL)
				if version == "absent" {
					product.Spec.UI = nil
				} else if version != "legacy" {
					product.Spec.UI.Contract = &datav1alpha1.UIContract{
						APIVersion:   version,
						HostOrigins:  []datav1alpha1.UIHostOrigin{"https://publisher.example"},
						Capabilities: []datav1alpha1.UICapability{"status"},
					}
				}
				scheme := runtime.NewScheme()
				if err := datav1alpha1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
				catalog := httptest.NewTLSServer(
					registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
						ContractEnabled:   func(context.Context) bool { return enabled },
						AppearanceEnabled: func(context.Context) bool { return enabled },
						DiscoveryEnabled:  func(context.Context) bool { return enabled },
					}),
				)
				t.Cleanup(catalog.Close)
				page := contractBrowser(t).MustPage().Timeout(15 * time.Second).
					MustNavigate(catalog.URL).MustWaitLoad()
				page.MustElement(".product-card").MustWaitVisible().MustClick()
				page.MustElement("#product-owner").
					MustWait(`() => this.textContent === 'Harbour team'`)
				if len(page.MustElements("iframe")) != 0 || requests.Load() != 0 {
					t.Fatal("descriptor inspection mounted or requested a publisher surface")
				}
				link := page.MustElement("#product-interfaces a[data-kind=api]")
				if href := link.MustAttribute(
					"href",
				); href == nil ||
					*href != publisher.URL+"/api/observations" {
					t.Fatalf("published API link = %v", href)
				}
				if !page.MustEval(`() => [...document.querySelectorAll('#product-interfaces a')].every(link =>
					link.target === '_blank' && link.relList.contains('noopener') && link.relList.contains('noreferrer'))`).
					Bool() {
					t.Fatal("published interfaces lost their independent navigation boundary")
				}
				page.MustElement("#appearance").MustSelect("Dark")
				page.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
				if page.MustElement("#product-owner").MustText() != "Harbour team" ||
					requests.Load() != 0 {
					t.Fatal("local appearance withdrew selected metadata or requested a publisher")
				}
				updated := product.DeepCopy()
				if err := reader.Get(
					t.Context(),
					client.ObjectKeyFromObject(product),
					updated,
				); err != nil {
					t.Fatal(err)
				}
				updated.Spec.Owner.Name = "Updated team"
				if err := reader.Update(t.Context(), updated); err != nil {
					t.Fatal(err)
				}
				page.MustElement("#refresh-products").MustClick()
				page.MustElement("#refresh-products").MustWait(`() => !this.disabled`)
				page.MustElement(".product-card").MustWaitVisible()
				page.MustElement(".product-card").MustClick()
				page.MustElement("#product-owner").
					MustWait(`() => this.textContent === 'Updated team'`)
				if requests.Load() != 0 || len(page.MustElements("iframe")) != 0 {
					t.Fatal("fresh descriptor selection executed a publisher surface")
				}
			})
		}
	}
}

// TestRegistryDeniesFrames exercises the browser policy against attempted frame injection.
func TestRegistryDeniesFrames(t *testing.T) {
	var requests atomic.Int32
	publisher := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>Publisher</title>"))
		}),
	)
	t.Cleanup(publisher.Close)
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	catalog := httptest.NewTLSServer(
		registry.NewHandler(fake.NewClientBuilder().WithScheme(scheme).Build()),
	)
	t.Cleanup(catalog.Close)
	page := contractBrowser(t).MustPage().Timeout(15 * time.Second).
		MustNavigate(catalog.URL).MustWaitLoad()
	result := page.MustEval(`async (url) => await new Promise(resolve => {
		addEventListener('securitypolicyviolation', event => {
			if (event.effectiveDirective === 'frame-src') resolve('denied');
		}, {once: true});
		const frame = document.createElement('iframe');
		frame.addEventListener('load', () => resolve('loaded'), {once: true});
		frame.src = url; document.body.append(frame);
		setTimeout(() => resolve('no-event'), 3000);
	})`, publisher.URL).Str()
	if result != "denied" || requests.Load() != 0 {
		t.Fatalf(
			"registry frame policy result = %q, publisher requests = %d",
			result,
			requests.Load(),
		)
	}
}
