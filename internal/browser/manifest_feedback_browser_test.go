//go:build browser

package browser_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/kweft/internal/demoproduct"
	"github.com/devantler-tech/kweft/web"
)

// TestManifestMissingFields identifies repairs without navigating rejected declarations.
func TestManifestMissingFields(t *testing.T) {
	host := httptest.NewTLSServer(web.KitHandler(func() bool { return true }))
	t.Cleanup(host.Close)
	var opened atomic.Int32
	product := demoproduct.NewHandler(host.URL)
	child := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ui" {
			opened.Add(1)
		}
		product.ServeHTTP(w, r)
	}))
	t.Cleanup(child.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(30 * time.Second).
		MustNavigate(host.URL).
		MustWaitLoad()
	valid := map[string]any{
		"url": child.URL + "/ui", "title": "Harbour observations",
		"contract": map[string]any{
			"apiVersion": "data-product-ui/v1", "hostOrigins": []string{host.URL},
			"capabilities": []string{"status"},
		},
	}
	for _, tc := range []struct {
		name   string
		mutate string
		fields []string
	}{
		{"missing URL", "delete value.url", []string{"url"}},
		{"missing title", "delete value.title", []string{"title"}},
		{"missing contract", "delete value.contract", []string{"contract"}},
		{"empty manifest", "value = {}", []string{"url", "title", "contract"}},
		{"missing version", "delete value.contract.apiVersion", []string{"apiVersion"}},
		{"missing hosts", "delete value.contract.hostOrigins", []string{"hostOrigins"}},
		{"missing capabilities", "delete value.contract.capabilities", []string{"capabilities"}},
		{"empty contract", "value.contract = {}", []string{"apiVersion", "hostOrigins", "capabilities"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page.MustEval(
				`(valid) => { let value = structuredClone(valid); `+tc.mutate+`; document.querySelector('#manifest').value = JSON.stringify(value); }`,
				valid,
			)
			page.MustElement("#validate").MustClick()
			page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
			message := page.MustElement("#kit-status").MustText()
			if !strings.Contains(strings.ToLower(message), "missing") {
				t.Fatalf("feedback does not identify missing fields: %s", message)
			}
			for _, field := range tc.fields {
				if !strings.Contains(message, field) {
					t.Fatalf("feedback omits required field %s: %s", field, message)
				}
			}
			if page.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() ||
				opened.Load() != 0 {
				t.Fatal("rejected manifest navigated a product frame")
			}
		})
	}
	// Unknown names and values must never become validation feedback.
	for _, mutate := range []string{
		`value['private-input-canary'] = 'private-value-canary'; delete value.title`,
		`value.contract['private-input-canary'] = 'private-value-canary'; delete value.contract.apiVersion`,
	} {
		page.MustEval(
			`(valid) => { const value = structuredClone(valid); `+mutate+`; document.querySelector('#manifest').value = JSON.stringify(value); }`,
			valid,
		)
		page.MustElement("#validate").MustClick()
		page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
		if strings.Contains(page.MustElement("#kit-status").MustText(), "canary") ||
			opened.Load() != 0 {
			t.Fatal("unknown declaration was reflected or opened")
		}
	}
	// A corrected declaration connects, but another invalid submission revokes it.
	page.MustEval(
		`(valid) => { document.querySelector('#manifest').value = JSON.stringify(valid); }`,
		valid,
	)
	page.MustElement("#grant-status").MustClick()
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	if opened.Load() != 1 {
		t.Fatal("corrected manifest did not open exactly one product surface")
	}
	page.MustEval(
		`(valid) => { delete valid.title; document.querySelector('#manifest').value = JSON.stringify(valid); }`,
		valid,
	)
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
	if page.MustEval(`() => !document.querySelector('iframe').hidden || document.querySelector('iframe').hasAttribute('src')`).
		Bool() ||
		opened.Load() != 1 {
		t.Fatal("invalid replacement retained or renavigated the open surface")
	}
	page.MustEval(
		`(valid) => { document.querySelector('#manifest').value = JSON.stringify(valid); }`,
		valid,
	)
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	if opened.Load() != 2 {
		t.Fatal("correcting the replacement did not reconnect")
	}
}
