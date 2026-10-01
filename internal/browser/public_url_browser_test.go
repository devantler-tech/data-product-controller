//go:build browser

package browser_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
)

// TestPublicURLSandboxQueries permits only the sample's declared reads from an opaque iframe.
func TestPublicURLSandboxQueries(t *testing.T) {
	var productHandler atomic.Value
	var forbiddenRequests atomic.Int32
	productHandler.Store(demoproduct.NewHandler())
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			forbiddenRequests.Add(1)
		}
		productHandler.Load().(http.Handler).ServeHTTP(w, r)
	}))
	t.Cleanup(product.Close)
	handler, err := demoproduct.NewHandlerWithPublicURL(product.URL)
	if err != nil {
		t.Fatal(err)
	}
	productHandler.Store(handler)
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(
			[]byte(
				`<iframe sandbox="allow-forms allow-scripts" src="` + product.URL + `/ui"></iframe>`,
			),
		)
	}))
	t.Cleanup(host.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(15 * time.Second).
		MustNavigate(host.URL).
		MustWaitLoad()
	frame := page.MustElement("iframe").MustFrame()
	frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	frame.MustElement("#station").MustSelect("Nordhavn")
	frame.MustElement("button[type=submit]").MustClick()
	frame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	if !strings.Contains(frame.MustElement("#observations").MustText(), "Nordhavn") {
		t.Fatal("query did not return the chosen station")
	}
	if result := frame.MustEval(`async () => {
		try { await fetch('healthz', {credentials:'omit'}); return 'allowed'; }
		catch { return 'blocked'; }
	}`).Str(); result != "blocked" || forbiddenRequests.Load() != 0 {
		t.Fatal("CSP allowed an undeclared endpoint")
	}
	if sandbox := page.MustElement("iframe").
		MustAttribute("sandbox"); sandbox == nil ||
		*sandbox != "allow-forms allow-scripts" {
		t.Fatal("query repair weakened the opaque-origin sandbox")
	}
}
