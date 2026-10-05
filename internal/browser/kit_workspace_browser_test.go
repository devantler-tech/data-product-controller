//go:build browser

package browser_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/web"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

func TestKitRejectsDuplicateImportKeysBeforeNavigation(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	descriptor := descriptorJSON(t, offlineDescriptor(host, publisher))
	manifest := descriptorJSON(t, offlineDescriptor(host, publisher)["ui"])
	for _, test := range []struct{ name, selector, action, wire string }{
		{"manifest-url", "#manifest", "#validate", strings.Replace(manifest, `"url":`, `"url":"https://private.invalid/ui","url":`, 1)},
		{"manifest-escaped-url", "#manifest", "#validate", strings.Replace(manifest, `"url":`, `"\u0075rl":"https://private.invalid/ui","url":`, 1)},
		{"descriptor-ready", "#descriptor", "#import-descriptor", strings.Replace(descriptor, `"ready":true`, `"ready":false,"ready":true`, 1)},
		{"descriptor-escaped-ready", "#descriptor", "#import-descriptor", strings.Replace(descriptor, `"ready":true`, `"\u0072eady":false,"ready":true`, 1)},
		{"nested-contract", "#descriptor", "#import-descriptor", strings.Replace(descriptor, `"apiVersion":"data-product-ui/v1"`, `"apiVersion":"data-product-ui/v2","apiVersion":"data-product-ui/v1"`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			page.MustElement(test.selector).MustSelectAllText().MustInput(test.wire)
			page.MustElement(test.action).MustClick()
			page.MustElement("#kit-status").
				MustWait(`()=>['invalid','ready'].includes(this.dataset.state)`)
			if page.MustEval(`()=>document.querySelector('#kit-status').dataset.state !== 'invalid' || document.querySelector('iframe').hasAttribute('src')`).
				Bool() ||
				calls.Load() != before {
				t.Fatal("ambiguous JSON selected a published interface before rejection")
			}
		})
	}
	page.MustElement("#descriptor").MustSelectAllText().MustInput(descriptor)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	if !page.MustEval(`(text)=>DataProductDescriptor.validate(JSON.parse(text)).ready`, descriptor).
		Bool() {
		t.Fatal("ordinary object validation changed")
	}
	positive := offlineDescriptor(host, publisher)
	positive["description"] = `Literal JSON keys: {"ready":false} and a comma, kept as text.`
	uniqueAlias := strings.Replace(
		descriptorJSON(t, positive),
		`"ready":true`,
		`"\u0072eady":true`,
		1,
	)
	page.MustElement("#descriptor").MustSelectAllText().MustInput(uniqueAlias)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
}

func TestKitDescriptorFileSelectionCanBeClearedAndSwitched(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	wire := descriptorJSON(t, offlineDescriptor(host, publisher))
	file := filepath.Join(t.TempDir(), "descriptor.json")
	if err := os.WriteFile(file, []byte(wire), 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustElement("#descriptor-file").MustSetFiles(file)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	if !page.MustHas("#clear-descriptor-file") {
		t.Fatal("file selection has no visible clear action")
	}
	page.MustElement("#clear-descriptor-file").MustFocus().MustType(input.Enter)
	if !page.MustEval(`()=>document.querySelector('#descriptor-file').files.length===0 && !document.querySelector('iframe').hasAttribute('src')`).
		Bool() {
		t.Fatal("clearing a file retained its selection or session")
	}
	page.MustElement("#descriptor").MustInput(wire)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	// File selection replaces pasted input; entering pasted input switches back and revokes.
	page.MustElement("#descriptor-file").MustSetFiles(file)
	if !page.MustEval(`()=>document.querySelector('#descriptor').value==='' && !document.querySelector('iframe').hasAttribute('src')`).
		Bool() {
		t.Fatal("selecting a file left conflicting pasted input or an active session")
	}
	page.MustElement("#descriptor").MustInput(wire)
	if page.MustEval(`()=>document.querySelector('#descriptor-file').files.length`).Int() != 0 {
		t.Fatal("typing a descriptor retained the old file")
	}
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	before := calls.Load()
	// A real File object has one deliberately delayed external read; clear must defeat completion.
	page.MustEval(
		`()=>{File.prototype.arrayBuffer=function(){return new Promise(resolve=>window.finishWorkspaceFile=resolve);};}`,
	)
	page.MustElement("#descriptor-file").MustSetFiles(file)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='importing'`)
	page.MustElement("#clear-descriptor-file").MustFocus().MustType(input.Enter)
	page.MustEval(
		`async (wire)=>{finishWorkspaceFile(new TextEncoder().encode(wire).buffer);await new Promise(resolve=>setTimeout(resolve,50));}`,
		wire,
	)
	if calls.Load() != before ||
		page.MustEval(`()=>document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("cleared pending file read revived an interface")
	}
}

func TestKitDescriptorProtocolClosureHasWorkingExplicitRetry(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	wire := descriptorJSON(t, offlineDescriptor(host, publisher))
	page.MustElement("#grant-status").MustClick()
	page.MustElement("#descriptor").MustInput(wire)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	oldSession := frame.MustEval(`()=>init.session`).Str()
	before := calls.Load()
	frame.MustEval(
		`()=>{for(let i=0;i<257;i++)parent.postMessage({apiVersion:init.apiVersion,type:'status',session:init.session,state:'error'},hostOrigin);}`,
	)
	page.MustWait(`()=>document.querySelector('iframe').hidden`)
	if !page.MustHas("#retry-interface") {
		t.Fatal("descriptor closure offers no explicit descriptor retry")
	}
	if page.MustElement("#retry-interface").MustText() != "Retry descriptor" {
		t.Fatal("retry does not identify the descriptor input")
	}
	if calls.Load() != before ||
		page.MustEval(`()=>document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("closed interface retried without user action")
	}
	page.MustElement("#retry-interface").MustFocus().MustType(input.Enter)
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
	if calls.Load() != before+1 ||
		page.MustElement("#product-surface").
			MustFrame().
			MustEval(`()=>init.session`).
			Str() ==
			oldSession {
		t.Fatal("explicit retry reused a revoked session")
	}
	// Retry reads current declarations again, rather than retaining the previous accepted object.
	frame = page.MustElement("#product-surface").MustFrame()
	frame.MustEval(
		`()=>{for(let i=0;i<257;i++)parent.postMessage({apiVersion:init.apiVersion,type:'status',session:init.session,state:'error'},hostOrigin);}`,
	)
	page.MustWait(`()=>document.querySelector('iframe').hidden`)
	page.MustEval(`()=>document.querySelector('#descriptor').value='{}'`)
	page.MustElement("#retry-interface").MustClick()
	page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='invalid'`)
	if calls.Load() != before+1 ||
		page.MustEval(`()=>document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("retry bypassed current descriptor validation")
	}
}

func TestKitHostAppearanceRemainsIndependentOfPresentationGrant(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			page, host, publisher, _ := kitWorkspaceFixture(t, enabled, true)
			if !page.MustElement("#appearance").MustVisible() {
				t.Fatal("host theme disappears when product appearance hints are disabled")
			}
			page.MustElement("#appearance").MustSelect("Dark")
			if !page.MustEval(`()=>getComputedStyle(document.documentElement).backgroundColor==='rgb(0, 0, 0)'`).
				Bool() {
				t.Fatal("selected dark appearance does not apply to the workspace")
			}
			descriptor := offlineDescriptor(host, publisher)
			if enabled {
				contract := descriptorObject(t, descriptor, "ui", "contract")
				contract["apiVersion"], contract["capabilities"] = "data-product-ui/v2", []string{
					"status",
					"appearance",
				}
			}
			page.MustElement("#descriptor").MustInput(descriptorJSON(t, descriptor))
			page.MustElement("#import-descriptor").MustClick()
			page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
			frame := page.MustElement("#product-surface").MustFrame()
			if frame.MustEval(`()=>Object.hasOwn(init,'appearance') || init.capabilities.includes('appearance') || Object.hasOwn(window,'appearance')`).
				Bool() {
				t.Fatal("host theme granted unsolicited appearance hints")
			}
			if enabled {
				page.MustElement("#grant-appearance").MustClick()
				page.MustElement("#import-descriptor").MustClick()
				page.MustElement("#kit-status").MustWait(`()=>this.dataset.state==='ready'`)
				page.MustElement("#product-surface").
					MustFrame().
					MustWait(`()=>window.appearance==='dark'`)
			}
			page.MustElement("#appearance").MustSelect("Light")
			page.MustElement("html").
				MustWait(`()=>getComputedStyle(this).backgroundColor==='rgb(255, 255, 255)'`)
			if enabled {
				page.MustElement("#product-surface").
					MustFrame().
					MustWait(`()=>window.appearance==='light'`)
			}
			if err := (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: "light"}}}).Call(
				page,
			); err != nil {
				t.Fatal(err)
			}
			page.MustElement("#appearance").MustSelect("System")
			page.MustElement("html").
				MustWait(`()=>getComputedStyle(this).backgroundColor==='rgb(255, 255, 255)'`)
			if err := (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: "dark"}}}).Call(
				page,
			); err != nil {
				t.Fatal(err)
			}
			page.MustElement("html").
				MustWait(`()=>getComputedStyle(this).backgroundColor==='rgb(0, 0, 0)'`)
			if enabled {
				page.MustElement("#product-surface").
					MustFrame().
					MustWait(`()=>window.appearance==='dark'`)
			}
			page.MustSetViewport(375, 812, 1, false)
			if page.MustEval(`()=>document.documentElement.scrollWidth>innerWidth`).Bool() {
				t.Fatal("workspace controls overflow the narrow viewport")
			}
			page.MustElement("#appearance").MustFocus().MustType(input.KeyL)
			if page.MustElement("#appearance").MustProperty("value").Str() != "light" {
				t.Fatal("theme cannot be changed through the keyboard")
			}
			page.MustElement("html").
				MustWait(`()=>getComputedStyle(this).backgroundColor==='rgb(255, 255, 255)'`)
		})
	}
}

func TestKitAppearancePreferenceSurvivesReload(t *testing.T) {
	page, _, _, _ := kitWorkspaceFixture(t, false, false)
	page.MustElement("#appearance").MustSelect("Dark")
	page.MustReload().MustWaitLoad()
	if page.MustElement("#appearance").MustProperty("value").Str() != "dark" ||
		!page.MustEval(`()=>getComputedStyle(document.documentElement).backgroundColor==='rgb(0, 0, 0)'`).
			Bool() {
		t.Fatal("saved host appearance was lost on reload")
	}
}

func kitWorkspaceFixture(
	t *testing.T,
	appearance, deniedStorage bool,
) (*rod.Page, string, string, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Independent interface</title><script>
		addEventListener('message', e=>{if(e.source!==parent)return;if(e.data.type==='appearance'){window.appearance=e.data.appearance;return;}if(e.data.type!=='init')return;window.init=e.data;window.hostOrigin=e.origin;parent.postMessage({apiVersion:init.apiVersion,type:'ready',session:init.session},e.origin);});</script>`))
	}))
	t.Cleanup(product.Close)
	host := httptest.NewTLSServer(
		web.KitHandlerWithOptions(
			web.KitOptions{
				ContractEnabled:   func() bool { return true },
				DiscoveryEnabled:  func() bool { return true },
				AppearanceEnabled: func() bool { return appearance },
			},
		),
	)
	t.Cleanup(host.Close)
	page := contractBrowser(t).MustPage().Timeout(15 * time.Second)
	if deniedStorage {
		page.MustEvalOnNewDocument(
			`Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Denied','SecurityError');}});`,
		)
	}
	page.MustNavigate(host.URL).MustWaitLoad()
	return page, host.URL, product.URL, calls
}
