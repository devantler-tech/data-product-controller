//go:build browser

package browser_test

import (
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
	page.MustEval(
		`async()=>{dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));await new Promise(resolve=>setTimeout(resolve,300));}`,
	)
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
