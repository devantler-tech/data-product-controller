//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/web"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestDeletingProductRevokesBrowserSurface catches opening a resource whose retained condition still says Ready.
func TestDeletingProductRevokesBrowserSurface(t *testing.T) {
	for _, discovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("discovery-%t", discovery), func(t *testing.T) {
			productServer := httptest.NewTLSServer(demoproduct.NewHandler())
			t.Cleanup(productServer.Close)
			product := workspaceProduct(productServer.URL)
			product.Finalizers = []string{"example.test/retain"}
			scheme := runtime.NewScheme()
			if err := datav1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
			server := httptest.NewTLSServer(registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
				DiscoveryEnabled: func(context.Context) bool { return discovery },
			}))
			t.Cleanup(server.Close)
			page := contractBrowser(t).MustPage().Timeout(15 * time.Second).MustNavigate(server.URL).MustWaitLoad()
			page.MustElement("#product-surface").MustWaitVisible()
			if err := reader.Delete(t.Context(), product); err != nil {
				t.Fatal(err)
			}
			if discovery {
				page.MustElement(".product-card").MustClick()
			} else {
				page.MustNavigate(server.URL).MustWaitLoad()
			}
			page.MustElement("#product-readiness").MustWait(`() => this.textContent === "Not ready"`)
			if src := page.MustElement("#product-surface").MustAttribute("src"); src != nil {
				t.Fatal("deleting product opened or retained a surface")
			}
		})
	}
}

// TestDescriptorFileRejectsMalformedUTF8 catches File.text silently replacing invalid bytes and mounting changed metadata.
func TestDescriptorFileRejectsMalformedUTF8(t *testing.T) {
	var navigations atomic.Int32
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		navigations.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><script>addEventListener('message',e=>{if(e.source===parent&&e.data.type==='init')parent.postMessage({apiVersion:e.data.apiVersion,type:'ready',session:e.data.session},e.origin);});</script>`))
	}))
	t.Cleanup(product.Close)
	kit := httptest.NewTLSServer(web.KitHandlerWithOptions(web.KitOptions{
		ContractEnabled: func() bool { return true }, DiscoveryEnabled: func() bool { return true },
	}))
	t.Cleanup(kit.Close)
	page := contractBrowser(t).MustPage().Timeout(15 * time.Second).MustNavigate(kit.URL).MustWaitLoad()
	wire := descriptorJSON(t, offlineDescriptor(kit.URL, product.URL))
	page.MustEval(`(wire) => {document.querySelector('#descriptor').value=wire; document.querySelector('#descriptor-form').requestSubmit();}`, wire)
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	before := navigations.Load()
	malformed := []byte(strings.Replace(wire, "Public observations", "Public "+string([]byte{0xff})+" observations", 1))
	file := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(file, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustEval(`() => {document.querySelector('#descriptor').value='';}`)
	page.MustElement("#descriptor-file").MustSetFiles(file)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`() => ['invalid','ready'].includes(this.dataset.state)`)
	if page.MustEval(`() => document.querySelector('#kit-status').dataset.state`).Str() != "invalid" ||
		page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).Bool() ||
		navigations.Load() != before {
		t.Fatal("malformed UTF-8 was changed into an accepted descriptor or retained a session")
	}
}

// largeCatalogReader models native Kubernetes pagination while the router, descriptor encoder and browser stay real.
type largeCatalogReader struct {
	client.Reader
	products []datav1alpha1.DataProduct
}

// List returns only the requested native page and carries its continuation.
func (r largeCatalogReader) List(_ context.Context, out client.ObjectList, opts ...client.ListOption) error {
	options := (&client.ListOptions{}).ApplyOptions(opts)
	list, ok := out.(*datav1alpha1.DataProductList)
	if !ok {
		return fmt.Errorf("unexpected list %T", out)
	}
	if options.Limit < 1 || options.Limit > 100 {
		return fmt.Errorf("invalid native limit %d", options.Limit)
	}
	offset := 0
	if options.Continue != "" {
		var err error
		offset, err = strconv.Atoi(options.Continue)
		if err != nil || offset < 0 || offset > len(r.products) {
			return fmt.Errorf("invalid continuation")
		}
	}
	end := min(offset+int(options.Limit), len(r.products))
	list.Items = append([]datav1alpha1.DataProduct(nil), r.products[offset:end]...)
	if end < len(r.products) {
		list.Continue = strconv.Itoa(end)
	}
	return nil
}

// TestLargeCatalogFitsBrowserPages catches a browser that repeatedly asks for a response larger than the public wire bound.
func TestLargeCatalogFitsBrowserPages(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := largeCatalogReader{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
	for index := range 40 {
		product := workspaceProduct("https://publisher.example")
		product.Name = fmt.Sprintf("product-%02d", index)
		product.Spec.ID = fmt.Sprintf("urn:example:product-%02d", index)
		product.Spec.Name = strings.Repeat("n", 16000)
		product.Spec.Description = strings.Repeat("d", 16000)
		product.Spec.Owner.Name = strings.Repeat("o", 16000)
		product.Spec.Outputs = make([]datav1alpha1.OutputPort, 100)
		for port := range product.Spec.Outputs {
			product.Spec.Outputs[port] = datav1alpha1.OutputPort{
				Name: fmt.Sprintf("query-%d", port), Protocol: datav1alpha1.ProtocolOpenAPI,
				URL: "https://example.test/data", ContractURL: "https://example.test/openapi.json",
			}
		}
		reader.products = append(reader.products, *product)
	}
	handler := registry.NewHandlerWithOptions(reader, registry.HandlerOptions{DiscoveryEnabled: func(context.Context) bool { return true }})
	exact := httptest.NewRecorder()
	single := registry.NewHandlerWithOptions(fake.NewClientBuilder().WithScheme(scheme).WithObjects(&reader.products[0]).Build(), registry.HandlerOptions{DiscoveryEnabled: func(context.Context) bool { return true }})
	single.ServeHTTP(exact, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/products/products/product-00", nil))
	if exact.Code != 200 || len(exact.Body.Bytes()) < 60000 || len(exact.Body.Bytes()) > 65536 {
		t.Fatalf("fixture must be valid near-limit metadata: status=%d bytes=%d", exact.Code, exact.Body.Len())
	}
	var descriptor map[string]any
	if err := json.Unmarshal(exact.Body.Bytes(), &descriptor); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	page := contractBrowser(t).MustPage().Timeout(15 * time.Second).MustNavigate(server.URL).MustWaitLoad()
	page.MustElement("#registry-status").MustWait(`() => this.textContent.includes('Could not') || document.querySelectorAll('.product-card').length > 0`)
	if len(page.MustElements(".product-card")) == 0 {
		t.Fatal("valid large catalog exceeds the browser's requested page response bound")
	}
	for range 4 {
		if !page.MustElement("#load-more").MustVisible() {
			break
		}
		before := len(page.MustElements(".product-card"))
		page.MustElement("#load-more").MustClick()
		page.MustWait(`(before) => document.querySelectorAll('.product-card').length > before`, before)
	}
	if len(page.MustElements(".product-card")) != 40 || page.MustElement("#load-more").MustVisible() {
		t.Fatal("bounded continuation did not enumerate all forty large products")
	}
}
