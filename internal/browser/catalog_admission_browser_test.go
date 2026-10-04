//go:build browser

package browser_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// catalogWireFixture serves production assets and a caller-controlled public response.
func catalogWireFixture(
	t *testing.T,
	serve func(http.ResponseWriter, *http.Request),
) *httptest.Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	assets := registry.NewHandler(fake.NewClientBuilder().WithScheme(scheme).Build())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/ui-config":
			_, _ = w.Write([]byte(`{"discoveryEnabled":true}`))
		case strings.HasPrefix(r.URL.Path, "/api/v2/products"):
			serve(w, r)
		default:
			assets.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func catalogInspectionDescriptor() map[string]any {
	p := offlineDescriptor("https://host.example.test", "https://publisher.example.test")
	delete(p, "ui")
	p["displayName"] = "Harbour temperatures"
	return p
}

func catalogPage(products ...map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "data-product-discovery/v1", "products": products,
		"continue": "", "rejected": 0,
	}
}

// TestCatalogRejectsMalformedDescriptors verifies admission before display or export on both discovery paths.
func TestCatalogRejectsMalformedDescriptors(t *testing.T) {
	browser := contractBrowser(t)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong-kind", func(p map[string]any) { p["kind"] = "Unexpected" }},
		{"unknown-field", func(p map[string]any) { p["unsupported"] = "synthetic" }},
		{"nested-field", func(p map[string]any) { descriptorObject(t, p, "owner")["unsupported"] = "synthetic" }},
		{"null-owner", func(p map[string]any) { p["owner"] = nil }},
		{"string-readiness", func(p map[string]any) { p["ready"] = "false" }},
		{"unsafe-generation", func(p map[string]any) { p["generation"] = 9007199254740992 }},
		{"stale-ready", func(p map[string]any) { p["observedGeneration"] = 6 }},
		{"bad-health", func(p map[string]any) { descriptorObject(t, p, "health", "source")["state"] = "unknown" }},
		{"unsupported-id", func(p map[string]any) { p["id"] = "ftp://publisher.example.test/id" }},
		{"short-numeric-url", func(p map[string]any) { descriptorEntry(t, p["outputs"])["url"] = "https://127.1/query" }},
		{"hex-numeric-url", func(p map[string]any) { descriptorEntry(t, p["outputs"])["url"] = "https://0x7f000001/query" }},
		{"numeric-ui-url", func(p map[string]any) {
			p["ui"] = map[string]any{"url": "https://127.1/view", "title": "Published view"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := catalogInspectionDescriptor()
			tc.edit(bad)
			server := catalogWireFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/products" {
					_ = json.NewEncoder(w).Encode(catalogPage(bad))
				} else {
					_ = json.NewEncoder(w).Encode(bad)
				}
			})
			page := browser.MustPage().
				Timeout(10 * time.Second).
				MustNavigate(server.URL).
				MustWaitLoad()
			defer func() { _ = page.Close() }()
			page.MustElement("#product-count").MustWait(`() => this.textContent !== "Loading…"`)
			if len(page.MustElements(".product-card")) != 0 {
				t.Fatal("malformed descriptor became a catalog card")
			}
			page.MustNavigate(server.URL + "?product=products%2Fharbour").MustWaitLoad()
			page.MustElement("#selection-status").
				MustWait(`() => !this.hidden && !this.textContent.includes("Loading")`)
			if !page.MustEval(`() => document.querySelector('#product-metadata').hidden &&
				document.querySelector('#descriptor-actions').hidden &&
				!document.querySelector('#product-surface').hasAttribute('src')`).Bool() {
				t.Fatal("malformed exact response retained descriptor or surface state")
			}
		})
	}
}

// TestCatalogBoundsStreamedResponses tests chunked responses without relying on Content-Length.
func TestCatalogBoundsStreamedResponses(t *testing.T) {
	oversize := catalogInspectionDescriptor()
	oversize["unsupported"] = strings.Repeat("x", 2*1024*1024+1)
	oversizeWire := descriptorJSON(t, catalogPage(oversize))
	validWire := descriptorJSON(t, catalogPage(catalogInspectionDescriptor()))
	invalidUTF8 := strings.Replace(validWire, "Public observations", "\xff", 1)
	for _, tc := range []struct {
		name string
		wire string
	}{
		{"oversize", oversizeWire},
		{"invalid-utf8", invalidUTF8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			browser := contractBrowser(t)
			server := catalogWireFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte(tc.wire))
			})
			page := browser.MustPage().
				Timeout(10 * time.Second).
				MustNavigate(server.URL).
				MustWaitLoad()
			defer func() { _ = page.Close() }()
			page.MustElement("#product-count").MustWait(`() => this.textContent !== "Loading…"`)
			if page.MustElement("#product-count").MustText() != "Unavailable" {
				t.Fatal("invalid streamed inventory became usable")
			}
			page.MustNavigate(server.URL + "?product=products%2Fharbour").MustWaitLoad()
			page.MustElement("#selection-status").
				MustWait(`() => !this.hidden && !this.textContent.includes("Loading")`)
			if !page.MustEval(`() => document.querySelector('#descriptor-actions').hidden`).Bool() {
				t.Fatal("invalid streamed exact response became exportable")
			}
		})
	}
}

// TestCatalogSearchesCanonicalIdentity keeps unready products inspectable without authorizing a UI.
func TestCatalogSearchesCanonicalIdentity(t *testing.T) {
	product := catalogInspectionDescriptor()
	product["ready"] = false
	product["observedGeneration"] = 6
	product["readiness"] = map[string]any{
		"reason":  "stale",
		"message": "Awaiting current observation",
	}
	server := catalogWireFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/products" {
			_ = json.NewEncoder(w).Encode(catalogPage(product))
		} else {
			_ = json.NewEncoder(w).Encode(product)
		}
	})
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(10 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	for _, term := range []string{"harbour", "products/harbour", "urn:example:harbour", "Marine team", "Public observations"} {
		page.MustEval(`term => { const search = document.querySelector('#product-search');
			search.value = term; search.dispatchEvent(new Event('input')); }`, term)
		if len(page.MustElements(".product-card")) != 1 {
			t.Fatalf("canonical metadata search failed for %s", term)
		}
	}
	page.MustElement(".product-card").MustClick()
	page.MustElement("#export-descriptor").MustWaitVisible()
	if page.MustElement("#product-readiness").MustText() != "Not ready" ||
		page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
			Bool() {
		t.Fatal("unready descriptor was not inspectable without a surface")
	}
}

// TestCatalogRefreshCancelsExactRead observes actual HTTP cancellation when the user revokes selection.
func TestCatalogRefreshCancelsExactRead(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var reads atomic.Int64
	server := catalogWireFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/products" {
			_ = json.NewEncoder(w).Encode(catalogPage(catalogInspectionDescriptor()))
			return
		}
		if reads.Add(1) > 1 {
			_ = json.NewEncoder(w).Encode(catalogInspectionDescriptor())
			return
		}
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(10 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible().MustClick()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("exact lookup did not start")
	}
	page.MustElement("#refresh-products").MustClick()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not cancel the revoked exact lookup")
	}
}

// TestCatalogScopeCancelsInventory observes request cancellation when the user replaces a pending scope.
func TestCatalogScopeCancelsInventory(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var reads atomic.Int64
	server := catalogWireFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if reads.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			close(canceled)
			return
		}
		_ = json.NewEncoder(w).Encode(catalogPage(catalogInspectionDescriptor()))
	})
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(10 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("inventory lookup did not start")
	}
	page.MustEval(`() => {
		document.querySelector('#namespace-filter').value = 'products';
		document.querySelector('#catalog-scope').requestSubmit();
	}`)
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("scope replacement did not cancel the earlier inventory")
	}
	page.MustElement(".product-card").MustWaitVisible()
}

// TestCatalogMalformedLaterPagePreservesValidCards keeps a failed continuation visibly incomplete.
func TestCatalogMalformedLaterPagePreservesValidCards(t *testing.T) {
	server := catalogWireFixture(t, func(w http.ResponseWriter, r *http.Request) {
		product := catalogInspectionDescriptor()
		response := catalogPage(product)
		if r.URL.Query().Get("continue") == "" {
			response["continue"] = "next-page"
		} else {
			product["kind"] = "Unexpected"
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(10 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	page.MustElement("#load-more").MustWaitVisible().MustClick()
	page.MustElement("#registry-status").
		MustWait(`() => this.textContent.includes("invalid catalog page")`)
	if len(page.MustElements(".product-card")) != 1 ||
		strings.Contains(page.MustElement("#discovery-scope").MustText(), "All products") {
		t.Fatal("malformed continuation replaced valid cards or claimed complete discovery")
	}
}
