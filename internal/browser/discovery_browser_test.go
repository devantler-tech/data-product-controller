//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
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
