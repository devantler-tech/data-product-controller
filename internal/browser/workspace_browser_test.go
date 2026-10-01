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
	"github.com/go-rod/rod/lib/input"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestRegistryWorkspace filters real descriptors and keeps unsafe/unready products inspectable without loading them.
func TestRegistryWorkspace(t *testing.T) {
	productServer := httptest.NewTLSServer(demoproduct.NewHandler())
	t.Cleanup(productServer.Close)
	product := workspaceProduct(productServer.URL)
	other := product.DeepCopy()
	other.Name, other.Spec.Name, other.Spec.Owner.Name = "weather", "Weather readings", "Weather team"
	other.Spec.Owner.Name = strings.Repeat("Weather", 24)
	other.Status.Conditions = nil
	other.Spec.Outputs[0].ContractURL = "javascript:alert(1)"
	other.Spec.Description = "<img src=x onerror=alert(1)>"
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product, other).Build()
	server := httptest.NewTLSServer(
		registry.NewHandler(reader, func(context.Context) bool { return true }),
	)
	t.Cleanup(server.Close)
	page := contractBrowser(t).MustPage().MustNavigate(server.URL).MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	if !page.MustEval(`() => !!document.querySelector('#product-search')`).Bool() {
		t.Fatal("workspace has no product search")
	}
	page.MustElement("#product-search").MustInput("HARBOUR TEAM")
	page.MustElement("#product-count").MustWait(`() => this.textContent === '1 of 2 products'`)
	page.MustElement(".product-card").MustFocus()
	page.Keyboard.MustType(input.Enter)
	if page.MustEval(`() => document.activeElement.id`).Str() != "interaction-title" {
		t.Fatal("product selection did not focus its details")
	}
	if got := page.MustElement("#product-owner").MustText(); got != "Harbour team" {
		t.Fatalf("owner = %q", got)
	}
	if got := page.MustElement("#product-interfaces a[data-kind=contract]").
		MustAttribute("href"); got == nil ||
		*got != productServer.URL+"/openapi.json" {
		t.Fatalf("contract link = %v", got)
	}
	page.MustElement("#product-search").MustSelectAllText().MustInput("not present")
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes('No products match')`)
	page.MustElement("#clear-filters").MustClick()
	page.MustElement("#readiness-filter").MustSelect("Not ready")
	page.MustElement("#product-count").MustWait(`() => this.textContent === '1 of 2 products'`)
	page.MustElement(".product-card").MustFocus()
	page.Keyboard.MustType(input.Enter)
	if source := page.MustElement("#product-surface").MustAttribute("src"); source != nil {
		t.Fatal("unready product opened an iframe")
	}
	if page.MustEval(`() => !!document.querySelector('a[href^="javascript:"]') || !!document.querySelector('#interaction-description img')`).
		Bool() {
		t.Fatal("descriptor metadata became executable content")
	}
	// A narrow viewport must contain the workspace without a horizontal page scroll.
	page.MustSetViewport(375, 812, 1, false)
	if page.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
		t.Fatal("mobile workspace overflows horizontally")
	}
}

// TestRegistryRetry clears failed state and re-reads the real API when the user retries.
func TestRegistryRetry(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(workspaceProduct("https://example.com")).
		Build()
	handler := registry.NewHandler(reader, func(context.Context) bool { return true })
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/products" && fail.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	page := contractBrowser(t).MustPage().MustNavigate(server.URL).MustWaitLoad()
	if !page.MustEval(`() => !!document.querySelector('#refresh-products')`).Bool() {
		t.Fatal("workspace has no retry action")
	}
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes('Could not load')`)
	page.MustElement("#product-search").MustInput("harbour")
	if !strings.Contains(page.MustElement("#registry-status").MustText(), "Could not load") {
		t.Fatal("filtering an unavailable inventory hid the retry message")
	}
	fail.Store(false)
	page.MustElement("#refresh-products").MustClick()
	page.MustElement(".product-card").MustWaitVisible()
	if page.MustElement("#product-count").MustText() != "1 product" {
		t.Fatal("retry did not restore the inventory")
	}
}

// TestHarbourTable includes observation time and keeps the current station result after an older request finishes.
func TestHarbourTable(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := demoproduct.NewHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("station") == "nordhavn" {
			close(started)
			<-release
			defer close(finished)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	var released bool
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	page := contractBrowser(t).MustPage().MustNavigate(server.URL + "/ui").MustWaitLoad()
	page.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	if !page.MustEval(`async () => {
        const link = [...document.querySelectorAll('a')].find(a => a.textContent === 'Open JSON data');
        const response = await fetch(link.href);
        return response.ok && (await response.json()).items.length === 2;
    }`).Bool() {
		t.Fatal("direct JSON data link does not reach the observation API")
	}
	if !page.MustEval(`() => !!document.querySelector('#observations table')`).Bool() {
		t.Fatal("Harbour observations are not a comparison table")
	}
	if text := page.MustElement("#observations").
		MustText(); !strings.Contains(text, "2026-08-29") ||
		!strings.Contains(text, "16.8") {
		t.Fatalf("table omits timestamp or value: %s", text)
	}
	page.MustElement("#station").MustSelect("Nordhavn")
	page.MustElement("button[type=submit]").MustClick()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Nordhavn request did not start")
	}
	page.MustElement("#station").MustSelect("Refshaleoen")
	page.MustElement("button[type=submit]").MustClick()
	page.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	close(release)
	released = true
	<-finished
	// A same-origin fence confirms the delayed response is complete before the next UI assertion.
	page.MustEval(
		`async () => { await fetch('healthz'); await new Promise(requestAnimationFrame); }`,
	)
	if got := page.MustElement("#observations tbody").
		MustText(); !strings.Contains(got, "Refshaleoen") ||
		strings.Contains(got, "Nordhavn") {
		t.Fatalf("older query overwrote the chosen station: %s", got)
	}
	page.MustSetViewport(375, 812, 1, false)
	if page.MustEval(`() => document.documentElement.scrollWidth > innerWidth || document.querySelector('caption').getBoundingClientRect().width < 300`).
		Bool() {
		t.Fatal("mobile results overflow or collapse their caption")
	}
}

// TestHarbourRecovery reports a data-plane failure, then restores real results after a retry.
func TestHarbourRecovery(t *testing.T) {
	var unavailable atomic.Bool
	unavailable.Store(true)
	handler := demoproduct.NewHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/observations" && unavailable.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	page := contractBrowser(t).MustPage().MustNavigate(server.URL + "/ui").MustWaitLoad()
	page.MustElement("#status").MustWait(`() => this.textContent.includes('503')`)
	if !strings.Contains(page.MustElement("#status").MustText(), "retry") {
		t.Fatal("failed query provides no recovery action")
	}
	unavailable.Store(false)
	page.MustElement("button[type=submit]").MustClick()
	page.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	if got := len(page.MustElements("#observations tbody tr")); got != 2 {
		t.Fatalf("recovered rows = %d, want 2", got)
	}
}

// workspaceProduct supplies a complete published descriptor through the real registry handler.
func workspaceProduct(origin string) *datav1alpha1.DataProduct {
	return &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{Name: "harbour", Namespace: "products"},
		Spec: datav1alpha1.DataProductSpec{
			ID:          origin + "/products/harbour",
			Name:        "Harbour observations",
			Description: "Temperature and salinity",
			Version:     "v1.0.0",
			Owner:       datav1alpha1.ProductOwner{Name: "Harbour team"},
			Outputs: []datav1alpha1.OutputPort{
				{
					Name:        "observations",
					Protocol:    datav1alpha1.ProtocolOpenAPI,
					URL:         origin + "/api/observations",
					ContractURL: origin + "/openapi.json",
					MediaType:   "application/json",
				},
			},
			UI: &datav1alpha1.ProductUI{Title: "Harbour observations", URL: origin + "/ui"},
		},
		Status: datav1alpha1.DataProductStatus{Conditions: []metav1.Condition{
			{
				Type:    datav1alpha1.ConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "DependenciesReady",
				Message: "Declared dependencies are ready.",
			},
		}},
	}
}
