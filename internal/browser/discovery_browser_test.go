//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/web"
	"github.com/go-rod/rod/lib/input"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// discoveryFixture serves the real workspace assets against bounded discovery responses.
func discoveryFixture(t *testing.T) (*httptest.Server, *atomic.Bool, *atomic.Int64) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	assets := registry.NewHandler(fake.NewClientBuilder().WithScheme(scheme).Build())
	unavailable := &atomic.Bool{}
	catalogStatus := &atomic.Int64{}
	first := discoveryProduct("summary")
	second := discoveryProduct("harbour")
	first["inputs"] = []any{
		map[string]any{
			"name": "observations",
			"productRef": map[string]any{
				"name":      "harbour",
				"namespace": "products",
				"output":    "observations",
			},
		},
	}
	first["composition"] = map[string]any{"reason": "InputsReady", "message": "Inputs are ready."}
	first["lineage"] = []any{
		map[string]any{
			"name":   "observations",
			"ready":  true,
			"reason": "InputReady",
			"productRef": map[string]any{
				"name":      "harbour",
				"namespace": "products",
				"output":    "observations",
			},
			"version": "v1.0.0",
			"owner":   map[string]any{"name": "Harbour team"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/ui-config":
			_ = json.NewEncoder(w).
				Encode(map[string]any{"discoveryEnabled": true, "uiContractEnabled": false, "uiAppearanceEnabled": false})
		case r.URL.Path == "/api/v2/products":
			if code := catalogStatus.Load(); code != 0 {
				http.Error(w, "catalog unavailable", int(code))
				return
			}
			if r.URL.Query().Get("continue") != "" {
				if unavailable.Load() {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).
					Encode(map[string]any{"apiVersion": "data-product-discovery/v1", "products": []any{first, second}, "continue": ""})
			} else {
				_ = json.NewEncoder(w).
					Encode(map[string]any{"apiVersion": "data-product-discovery/v1", "products": []any{first}, "continue": "second-page"})
			}
		case r.URL.Path == "/api/v2/products/products/summary":
			_ = json.NewEncoder(w).Encode(first)
		case r.URL.Path == "/api/v2/products/products/harbour":
			if unavailable.Load() {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(second)
		case strings.HasPrefix(r.URL.Path, "/api/v2/products/"):
			http.NotFound(w, r)
		default:
			assets.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, unavailable, catalogStatus
}

// TestDiscoveryExpiredPageAndListOutage keeps incomplete discovery honest while exact links remain usable.
func TestDiscoveryExpiredPageAndListOutage(t *testing.T) {
	server, _, catalogStatus := discoveryFixture(t)
	browser := contractBrowser(t)
	page := browser.MustPage().MustNavigate(server.URL).MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	catalogStatus.Store(410)
	page.MustElement("#load-more").MustClick()
	page.MustElement("#registry-status").MustWait(`() => this.textContent.includes("expired")`)
	page.MustElement("#product-search").MustInput("summary")
	if strings.Contains(page.MustElement("#discovery-scope").MustText(), "All products") {
		t.Fatal("expired partial inventory presented as complete")
	}
	if !strings.Contains(page.MustElement("#registry-status").MustText(), "expired") {
		t.Fatal("filter hid the page failure")
	}
	catalogStatus.Store(503)
	page.MustNavigate(server.URL + "?product=products%2Fharbour").MustWaitLoad()
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="harbour"`)
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes("Could not load products")`)
	for _, query := range []string{"product=products%2Fa..b", "product=products%2Fa.-b", "product=products%2Fharbour&product=products%2Fsummary"} {
		page.MustNavigate(server.URL + "?" + query).MustWaitLoad()
		page.MustElement("#selection-status").MustWait(`() => this.textContent.includes("invalid")`)
	}
}

// TestDiscoveryRechecksAndRevokesOpenedProduct exercises exact lookup against the real registry after readiness changes.
func TestDiscoveryRechecksAndRevokesOpenedProduct(t *testing.T) {
	productServer := httptest.NewTLSServer(demoproduct.NewHandler())
	t.Cleanup(productServer.Close)
	product := workspaceProduct(productServer.URL)
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product).
		Build()
	server := httptest.NewTLSServer(
		registry.NewHandlerWithOptions(
			reader,
			registry.HandlerOptions{DiscoveryEnabled: func(context.Context) bool { return true }},
		),
	)
	t.Cleanup(server.Close)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Fharbour").
		MustWaitLoad()
	page.MustElement("#product-surface").
		MustWaitVisible().
		MustFrame().
		MustElement("#status").
		MustWait(`() => this.textContent==="2 observations"`)
	product.Status.Conditions[0].Status = metav1.ConditionFalse
	product.Status.Conditions[0].Reason = "SourceNotReady"
	if err := reader.Status().Update(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	page.MustElement(".product-card").MustClick()
	page.MustElement("#product-readiness").MustWait(`() => this.textContent==="Not ready"`)
	if src := page.MustElement("#product-surface").MustAttribute("src"); src != nil {
		t.Fatal("exact lookup retained the previously available frame")
	}
}

// discoveryProduct is public metadata only; stale evidence must never be rendered as healthy.
func discoveryProduct(name string) map[string]any {
	return map[string]any{
		"apiVersion":  "data-product-descriptor/v1",
		"kind":        "DataProduct",
		"namespace":   "products",
		"name":        name,
		"id":          "https://example.test/products/" + name,
		"displayName": name,
		"description": "Public observations.",
		"version":     "v1.0.0",
		"owner":       map[string]any{"name": "Harbour team"},
		"outputs":     []any{},
		"ready":       false,
		"readiness": map[string]any{
			"reason":  "StatusStale",
			"message": "Current generation has not been observed.",
		},
		"generation":         3,
		"observedGeneration": 2,
		"health": map[string]any{
			"source": map[string]any{
				"state":              "stale",
				"generation":         3,
				"observedGeneration": 2,
				"message":            "Current generation has not been observed.",
			},
			"connector": map[string]any{
				"state":              "disabled",
				"generation":         3,
				"observedGeneration": 3,
				"message":            "Connector observation is disabled.",
			},
			"contracts": map[string]any{
				"state":              "unobserved",
				"generation":         3,
				"observedGeneration": 0,
				"message":            "No observation is available.",
			},
			"composition": map[string]any{
				"state":              "ready",
				"generation":         3,
				"observedGeneration": 3,
				"message":            "Inputs are ready.",
			},
		},
	}
}

// TestDiscoveryPagingRecovery keeps partial results honest and retries the same page without duplicate cards.
func TestDiscoveryPagingRecovery(t *testing.T) {
	server, _, catalogStatus := discoveryFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(5 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	page.MustElement("#discovery-scope").MustWait(`() => this.textContent.includes("loaded")`)
	catalogStatus.Store(503)
	page.MustElement("#load-more").MustWaitVisible().MustClick()
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes("Could not load more")`)
	if len(page.MustElements(".product-card")) != 1 {
		t.Fatal("failed page discarded loaded products")
	}
	catalogStatus.Store(0)
	page.MustElement("#load-more").MustClick()
	page.MustElement("#product-count").MustWait(`() => this.textContent.includes("2 products")`)
	if len(page.MustElements(".product-card")) != 2 {
		t.Fatal("page duplicated a product")
	}
	if page.MustElement("#load-more").MustVisible() {
		t.Fatal("complete inventory still offers another page")
	}
	page.MustElement("#product-search").MustInput("missing")
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes("No products match")`)
}

// TestDiscoveryHistoryAndMissingSelection restores deep links and does not disguise a deleted producer.
func TestDiscoveryHistoryAndMissingSelection(t *testing.T) {
	server, unavailable, _ := discoveryFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Fharbour").
		MustWaitLoad()
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="harbour"`)
	page.MustElement(".product-card").MustClick()
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="summary"`)
	page.MustEval(`() => history.back()`)
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="harbour"`)
	page.MustEval(`() => history.forward()`)
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="summary"`)
	unavailable.Store(true)
	page.MustNavigate(server.URL + "?product=products%2Fharbour").MustWaitLoad()
	page.MustElement("#selection-status").
		MustWait(`() => this.textContent.includes("no longer available")`)
	if src := page.MustElement("#product-surface").MustAttribute("src"); src != nil {
		t.Fatal("missing product retained a frame")
	}
}

// TestDiscoveryHealthAndLineage exposes stale checks and opens only a resolved producer.
func TestDiscoveryHealthAndLineage(t *testing.T) {
	server, _, _ := discoveryFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Fsummary").
		MustWaitLoad()
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="summary"`)
	page.MustElement("#product-health").MustWait(`() => this.textContent.includes("Stale")`)
	health := page.MustElement("#product-health").MustText()
	for _, want := range []string{"Source", "Disabled", "Unobserved", "3", "2"} {
		if !strings.Contains(health, want) {
			t.Fatalf("health missing %q: %s", want, health)
		}
	}
	page.MustElement("#appearance").MustSelect("Dark")
	if path := os.Getenv("DPC_DISCOVERY_SCREENSHOT"); path != "" {
		page.MustScreenshotFullPage(path)
	}
	page.MustElement("#product-lineage .lineage-link").MustFocus().MustType(input.Enter)
	page.MustElement("#interaction-title").MustWait(`() => this.textContent==="harbour"`)
	page.MustSetViewport(390, 844, 1, false)
	if page.MustEval(`() => document.documentElement.scrollWidth>innerWidth`).Bool() {
		t.Fatal("discovery details overflow narrow viewport")
	}
}

// TestDiscoveryExportFitsOfflineImport keeps the actual downloaded document within the shared wire limit.
func TestDiscoveryExportFitsOfflineImport(t *testing.T) {
	productServer := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><title>Independent product</title><script>
		addEventListener('message', e => {
		if (e.source !== parent || e.data.type !== 'init') return;
		parent.postMessage({apiVersion:e.data.apiVersion,type:'ready',session:e.data.session},e.origin);
		});</script>`))
		}),
	)
	t.Cleanup(productServer.Close)
	kit := httptest.NewTLSServer(web.KitHandlerWithOptions(web.KitOptions{
		ContractEnabled:  func() bool { return true },
		DiscoveryEnabled: func() bool { return true },
	}))
	t.Cleanup(kit.Close)
	product := workspaceProduct(productServer.URL)
	product.Spec.Name = strings.Repeat("n", 16000)
	product.Spec.Description = strings.Repeat("d", 16000)
	product.Spec.Owner.Name = strings.Repeat("o", 16000)
	product.Spec.Outputs = make([]datav1alpha1.OutputPort, 100)
	for index := range product.Spec.Outputs {
		product.Spec.Outputs[index] = datav1alpha1.OutputPort{
			Name: fmt.Sprintf("query-%d", index), Protocol: datav1alpha1.ProtocolOpenAPI,
			URL: "https://example.test/data", ContractURL: "https://example.test/openapi.json",
		}
	}
	product.Spec.UI.Contract = &datav1alpha1.UIContract{
		APIVersion:   "data-product-ui/v1",
		HostOrigins:  []datav1alpha1.UIHostOrigin{datav1alpha1.UIHostOrigin(kit.URL)},
		Capabilities: []datav1alpha1.UICapability{"status"},
	}
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
	server := httptest.NewTLSServer(registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
		DiscoveryEnabled: func(context.Context) bool { return true },
	}))
	t.Cleanup(server.Close)
	browser := contractBrowser(t)
	page := browser.MustPage().
		Timeout(15 * time.Second).
		MustNavigate(server.URL + "?product=products%2Fharbour").
		MustWaitLoad()
	page.MustElement("#export-descriptor").MustWaitVisible()
	wire := page.MustEval(`async () => {
		const response = await fetch('/api/v2/products/products/harbour');
		if (!response.ok) throw new Error('Fixture descriptor was rejected: '+response.status);
		return await response.text();
	}`).Str()
	if len(wire) < 60000 || len(wire) > 65536 {
		t.Fatalf("fixture must be an accepted near-limit descriptor, got %d bytes", len(wire))
	}
	page.MustEval(`() => {
		const create = URL.createObjectURL.bind(URL);
		URL.createObjectURL = blob => { window.exportedDescriptor = blob; return create(blob); };
		const click = HTMLAnchorElement.prototype.click;
		HTMLAnchorElement.prototype.click = function() {
			if (this.download) { window.exportedFilename = this.download; return; }
			return click.call(this);
		};
	}`)
	page.MustElement("#export-descriptor").MustClick()
	download := page.MustEval(`async () => await window.exportedDescriptor.text()`).Str()
	if page.MustEval(`() => window.exportedFilename`).Str() != "products-harbour.json" {
		t.Fatal("export did not produce a named descriptor download")
	}
	var actual, expected map[string]any
	if err := json.Unmarshal([]byte(wire), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(download), &actual); err != nil {
		t.Fatal(err)
	}
	if got, want := descriptorJSON(t, actual), descriptorJSON(t, expected); got != want {
		t.Fatal("download changed public descriptor metadata")
	}
	kitPage := browser.MustPage().Timeout(15 * time.Second).MustNavigate(kit.URL).MustWaitLoad()
	kitPage.MustEval(`text => {
		document.querySelector('#descriptor').value = text;
		document.querySelector('#descriptor-form').requestSubmit();
	}`, download)
	kitPage.MustElement("#kit-status").
		MustWait(`() => this.dataset.state==='ready' || this.dataset.state==='invalid'`)
	if kitPage.MustEval(`() => document.querySelector('#kit-status').dataset.state`).
		Str() !=
		"ready" {
		t.Fatalf(
			"actual %d-byte API descriptor exported as %d bytes and failed offline import: %s",
			len(wire),
			len(download),
			kitPage.MustElement("#kit-status").MustText(),
		)
	}
	if len(download) > 65536 {
		t.Fatalf("download exceeded the public contract: %d bytes", len(download))
	}
}
