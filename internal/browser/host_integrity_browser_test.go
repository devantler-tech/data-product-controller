//go:build browser

package browser_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/web"
)

// TestKitDeclarationAndGrantRevocation exercises the actual portable host and an independent opaque frame.
func TestKitDeclarationAndGrantRevocation(t *testing.T) {
	kit := httptest.NewTLSServer(web.KitHandlerWithOptions(web.KitOptions{
		ContractEnabled:   func() bool { return true },
		AppearanceEnabled: func() bool { return true },
		DiscoveryEnabled:  func() bool { return true },
	}))
	t.Cleanup(kit.Close)
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Independent surface</title><script>
		addEventListener('message', e => {if(e.source!==parent || e.data.type!=='init')return;
		window.init=e.data;parent.postMessage({apiVersion:init.apiVersion,type:'ready',session:init.session},e.origin);});</script>`))
	}))
	t.Cleanup(product.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(15 * time.Second).
		MustNavigate(kit.URL).
		MustWaitLoad()
	manifest := map[string]any{
		"url":   product.URL + "/ui",
		"title": "Independent surface",
		"contract": map[string]any{
			"apiVersion":   "data-product-ui/v2",
			"hostOrigins":  []string{kit.URL},
			"capabilities": []string{"status", "resize", "appearance"},
		},
	}
	manifestJSON := descriptorJSON(t, manifest)
	open := func() {
		page.MustEval(
			`(text)=>{document.querySelector('#manifest').value=text; for(const name of ['status','resize','appearance'])document.querySelector('#grant-'+name).checked=true; document.querySelector('#manifest-form').requestSubmit();}`,
			manifestJSON,
		)
		page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	}
	for _, grant := range []string{"status", "resize", "appearance"} {
		open()
		page.MustElement("#grant-" + grant).MustClick()
		if page.MustEval(`()=>document.querySelector('#product-surface').hasAttribute('src')`).
			Bool() {
			t.Errorf("withdrawing %s retained the previously granted interface", grant)
		}
		page.MustElement("#close").MustClick()
	}
	open()
	page.MustEval(
		`()=>{const field=document.querySelector('#manifest');field.value='';field.dispatchEvent(new Event('input',{bubbles:true}));}`,
	)
	page.MustElement("#validate").MustClick()
	if page.MustEval(`()=>document.querySelector('#manifest').checkValidity() || document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() {
		t.Error("native required-field validation retained the earlier manifest session")
	}
	page.MustElement("#close").MustClick()
	open()
	page.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))`)
	if page.MustEval(`()=>document.querySelector('#product-surface').hasAttribute('src')`).Bool() {
		t.Error("page exit retained an active compatibility session")
	}
	page.MustEval(`()=>dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))`)
	if page.MustEval(`()=>document.querySelector('#product-surface').hasAttribute('src')`).Bool() {
		t.Error("history restoration reopened the compatibility surface without an explicit action")
	}
	page.MustElement("#close").MustClick()
	data := offlineDescriptor(kit.URL, product.URL)
	file := filepath.Join(t.TempDir(), "descriptor.json")
	if err := os.WriteFile(file, []byte(descriptorJSON(t, data)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, withdrawal := range []string{"file", "paste", "manifest", "grant", "pagehide"} {
		page.MustEval(
			`()=>{document.querySelector('#descriptor').value='';File.prototype.arrayBuffer=function(){return new Promise(resolve=>window.finishImport=()=>resolve(new TextEncoder().encode(window.localDescriptor).buffer));};window.finishImport=null;}`,
		)
		page.MustEval(`(text)=>window.localDescriptor=text`, descriptorJSON(t, data))
		page.MustElement("#descriptor-file").MustSetFiles(file)
		page.MustElement("#import-descriptor").MustClick()
		page.MustWait(`()=>typeof window.finishImport==='function'`)
		page.MustEval(`(kind)=>{
		if(kind==='pagehide')dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));
		else if(kind==='grant'){const field=document.querySelector('#grant-status');field.checked=!field.checked;field.dispatchEvent(new Event('change',{bubbles:true}));}
		else {const field=document.querySelector(kind==='file'?'#descriptor-file':kind==='paste'?'#descriptor':'#manifest');field.value=kind==='file'?'':'{}';field.dispatchEvent(new Event(kind==='file'?'change':'input',{bubbles:true}));}
		finishImport();}`, withdrawal)
		page.MustEval(`async()=>{await new Promise(resolve=>setTimeout(resolve,100));}`)
		if page.MustEval(`()=>document.querySelector('#product-surface').hasAttribute('src') || !document.querySelector('#descriptor-snapshot').hidden`).
			Bool() {
			t.Errorf("delayed descriptor import revived after %s withdrawal", withdrawal)
		}
		page.MustElement("#close").MustClick()
	}
	// Explicit reopening remains usable after every withdrawal.
	open()
	if got := page.MustElement("#product-surface").
		MustAttribute("sandbox"); got == nil ||
		*got != "allow-forms allow-scripts" {
		t.Fatal("reopening broadened the sandbox")
	}
}

// TestPortableManifestURLProfile compares real portable library admission with the delivered descriptor profile.
func TestPortableManifestURLProfile(t *testing.T) {
	kit := httptest.NewTLSServer(web.KitHandler(func() bool { return true }))
	t.Cleanup(kit.Close)
	page := contractBrowser(t).MustPage().MustNavigate(kit.URL).MustWaitLoad()
	for _, value := range []string{"https:product.example/ui", "https:///product.example/ui", "HTTPS://product.example/ui", "https://product.example:/ui", "https://product.example:0/ui", "https://bad_host.example/ui", "https://product.example/%zz", "https://127.1/ui", "https://product.example/ui#", "https://product.example/é"} {
		if page.MustEval(`(url)=>{try{DataProductUI.validateMetadata({url,title:'Surface',contract:{apiVersion:'data-product-ui/v1',hostOrigins:[location.origin],capabilities:[]}});return true;}catch{return false;}}`, value).
			Bool() {
			t.Errorf("nonportable manifest URL admitted: %s", value)
		}
	}
	for _, value := range []string{"https://product.example/ui", "https://localhost/query", "https://127.0.0.1:8443/ui", "https://[::1]:8443/ui", "https://product.example:443/path%20ok?query=%25"} {
		if !page.MustEval(`(url)=>{try{DataProductUI.validateMetadata({url,title:'Surface',contract:{apiVersion:'data-product-ui/v1',hostOrigins:[location.origin],capabilities:[]}});return true;}catch{return false;}}`, value).
			Bool() {
			t.Errorf("supported canonical manifest URL rejected: %s", value)
		}
	}
}
