//go:build browser

package browser_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRegistryHistoryRevocation models persisted page transitions with a product removed while away.
func TestRegistryHistoryRevocation(t *testing.T) {
	server, unavailable, _ := discoveryFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Fharbour").
		MustWaitLoad()
	page.MustElement("#descriptor-actions").MustWaitVisible()
	page.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))`)
	if page.MustEval(`()=>!document.querySelector('#descriptor-actions').hidden || !document.querySelector('#product-metadata').hidden`).
		Bool() {
		t.Error("page exit retained the selected descriptor and export")
	}
	unavailable.Store(true)
	page.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))`)
	page.MustElement("#selection-status").
		MustWait(`()=>this.textContent.includes('no longer available')`)
	if !strings.Contains(page.MustElement("#selection-status").MustText(), "no longer available") ||
		page.MustEval(`()=>!document.querySelector('#descriptor-actions').hidden`).Bool() {
		t.Error(
			"persisted history restoration reused a descriptor instead of checking its current availability",
		)
	}
	// A mounted opaque product and a retained trace obey the same document boundary.
	mounted, _ := themeWorkspace(t, false, true, true, true)
	mounted.MustElement(".product-card").MustClick()
	mounted.MustElement("#ui-contract-status").MustWait(`()=>this.dataset.state==='ready'`)
	mounted.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))`)
	if mounted.MustEval(`()=>document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() {
		t.Error("registry page exit retained a ready product interface")
	}
	trace, _, _, _ := lineageFixture(t)
	tracePage := contractBrowser(
		t,
	).MustPage().
		MustNavigate(trace.URL + "?product=products%2Froot").
		MustWaitLoad()
	tracePage.MustElement("#trace-inputs").MustWaitVisible().MustClick()
	tracePage.MustElement("#save-trace").MustWaitVisible()
	tracePage.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))`)
	if tracePage.MustEval(`()=>!document.querySelector('#save-trace').hidden || !document.querySelector('#trace-result').hidden`).
		Bool() {
		t.Error("page exit retained a trace snapshot and download")
	}
}

// TestRegistryNativeHistoryRestoration observes actual history navigation without weakening cache headers.
func TestRegistryNativeHistoryRestoration(t *testing.T) {
	server, unavailable, _ := discoveryFixture(t)
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(
			[]byte("<!doctype html><title>Away</title><main>Away from the registry</main>"),
		)
	}))
	t.Cleanup(away.Close)
	page := contractBrowser(t).MustPage()
	page.MustEvalOnNewDocument(
		`addEventListener('pageshow', event => document.documentElement.dataset.nativeHistoryRestored = String(event.persisted))`,
	)
	page.MustNavigate(server.URL + "?product=products%2Fharbour").MustWaitLoad()
	page.MustElement("#descriptor-actions").MustWaitVisible()
	page.MustEval(`()=>{
		window.nativeHistoryEntry = true;
		addEventListener('pagehide', event => sessionStorage.setItem('native-history-exit', JSON.stringify({
			persisted: event.persisted,
			exportHidden: document.querySelector('#descriptor-actions').hidden,
			metadataHidden: document.querySelector('#product-metadata').hidden
		})));
	}`)
	page.MustNavigate(away.URL).MustWaitLoad()
	unavailable.Store(true)
	page.MustNavigateBack()
	page.MustElement("#products").MustWaitVisible()
	page.MustElement("html").MustWait(`()=>this.dataset.nativeHistoryRestored !== undefined`)
	restored := page.MustEval(`()=>document.documentElement.dataset.nativeHistoryRestored === 'true'`).
		Bool()
	if original := page.MustEval(`()=>window.nativeHistoryEntry === true`).
		Bool(); original != restored {
		t.Fatalf("history restored flag = %t, original document retained = %t", restored, original)
	}
	t.Logf("native back/forward cache restoration: %t", restored)
	if !page.MustEval(`()=>{const exit = JSON.parse(sessionStorage.getItem('native-history-exit')); return exit.exportHidden && exit.metadataHidden;}`).
		Bool() {
		t.Error("native page exit froze a selected descriptor or export")
	}
	page.MustElement("#selection-status").
		MustWait(`()=>this.textContent.includes('no longer available')`)
	if page.MustEval(`()=>!document.querySelector('#descriptor-actions').hidden || !document.querySelector('#product-metadata').hidden`).
		Bool() {
		t.Error("native history restoration reused the removed product instead of re-reading it")
	}
}
