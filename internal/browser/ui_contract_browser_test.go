//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/web"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestDemoWorksInTwoHosts covers an independently served product, both release states and query recovery.
func TestDemoWorksInTwoHosts(t *testing.T) {
	kit := httptest.NewTLSServer(web.KitHandler(func() bool { return true }))
	t.Cleanup(kit.Close)
	var handler atomic.Value
	var outage, enabled atomic.Bool
	handler.Store(demoproduct.NewHandler())
	productServer := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if outage.Load() && r.URL.Path == "/api/observations" {
				http.Error(w, "fixture outage", http.StatusServiceUnavailable)
				return
			}
			handler.Load().(http.Handler).ServeHTTP(w, r)
		}),
	)
	t.Cleanup(productServer.Close)
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	product := &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{Name: "harbour", Namespace: "products"},
		Spec: datav1alpha1.DataProductSpec{
			ID:          "urn:example:harbour",
			Name:        "Harbour observations",
			Description: "Public marine observations",
			Version:     "v1.0.0",
			Owner: datav1alpha1.ProductOwner{
				Name: "Marine team",
			},
			UI: &datav1alpha1.ProductUI{
				URL:   productServer.URL + "/ui",
				Title: "Harbour observations",
				Contract: &datav1alpha1.UIContract{
					APIVersion:   "data-product-ui/v1",
					Capabilities: []datav1alpha1.UICapability{"status", "resize"},
				},
			},
		},
		Status: datav1alpha1.DataProductStatus{
			Conditions: []metav1.Condition{
				{
					Type:    datav1alpha1.ConditionReady,
					Status:  metav1.ConditionTrue,
					Reason:  "DependenciesReady",
					Message: "Ready",
				},
			},
		},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	registryServer := httptest.NewTLSServer(
		registry.NewHandler(
			reader,
			func(context.Context) bool { return true },
			func(context.Context) bool { return enabled.Load() },
		),
	)
	t.Cleanup(registryServer.Close)
	product.Spec.UI.Contract.HostOrigins = []datav1alpha1.UIHostOrigin{
		datav1alpha1.UIHostOrigin(kit.URL),
		datav1alpha1.UIHostOrigin(registryServer.URL),
	}
	if err := reader.Create(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	handler.Store(demoproduct.NewHandler(kit.URL, registryServer.URL))
	browser := contractBrowser(t)
	page := browser.MustPage().
		Timeout(30 * time.Second).
		MustNavigate(registryServer.URL).
		MustWaitLoad()
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'unavailable'`)
	if page.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("disabled host opened contract-bearing UI")
	}
	enabled.Store(true)
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	outage.Store(true)
	frame.MustElement("button").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'error'`)
	outage.Store(false)
	frame.MustElement("button").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	manifest, err := json.Marshal(product.Spec.UI)
	if err != nil {
		t.Fatal(err)
	}
	kitPage := browser.MustPage().Timeout(30 * time.Second).MustNavigate(kit.URL).MustWaitLoad()
	kitPage.MustSetViewport(375, 812, 1, false)
	if !kitPage.MustEval(`() => document.querySelector('#kit-status').getAttribute('role') === 'status' && document.querySelector('#kit-status').getAttribute('aria-live') === 'polite' && document.querySelector('label[for="manifest"]') !== null`).
		Bool() {
		t.Fatal("kit lost its live status or form label")
	}
	kitPage.MustElement("#manifest").MustInput(string(manifest))
	kitPage.MustElement("#grant-status").MustClick()
	kitPage.MustElement("#validate").MustClick()
	kitPage.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	kitFrame := kitPage.MustElement("#product-surface").MustFrame()
	kitFrame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	if kitPage.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
		t.Fatal("kit overflows narrow viewport")
	}
	if kitFrame.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
		t.Fatal("product overflows narrow frame")
	}
	kitFrame.MustElement("#station").MustSelect("Nordhavn")
	kitFrame.MustElement("button").MustFocus().MustType(input.Enter)
	kitFrame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	kitPage.MustElement("#close").MustFocus().MustType(input.Enter)
	if kitPage.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("kit retained closed product")
	}
	// Removing publisher approval cannot be bypassed by the host's declared manifest.
	handler.Store(demoproduct.NewHandler())
	page.MustActivate()
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'timeout'`)
	if page.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("timeout retained an unauthorized frame")
	}
}

// TestStandaloneKitLoadsWithoutRegistry exercises the portable host with no Kubernetes or registry API.
func TestStandaloneKitLoadsWithoutRegistry(t *testing.T) {
	server := httptest.NewTLSServer(web.KitHandler(func() bool { return true }))
	t.Cleanup(server.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(5 * time.Second).
		MustNavigate(server.URL).
		MustWaitLoad()
	page.MustElement("#manifest").
		MustInput(`{"url":"javascript:alert(1)","title":"Invalid","contract":{"apiVersion":"data-product-ui/v1","hostOrigins":[],"capabilities":[]}}`)
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
	if page.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("kit navigated an invalid manifest")
	}
}

// TestUIHostMessageBoundary exercises real opaque-origin frames and rejects spoofed or ungranted hints.
func TestUIHostMessageBoundary(t *testing.T) {
	child := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Protocol fixture</title><script>
		addEventListener('message', e => { if (e.source !== parent || e.data.type !== 'init') return;
		window.session = e.data.session; window.hostOrigin = e.origin;
		parent.postMessage({apiVersion:'data-product-ui/v1',type:'ready',session},e.origin);
		});</script>`))
	}))
	t.Cleanup(child.Close)
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewTLSServer(
		registry.NewHandler(
			fake.NewClientBuilder().WithScheme(scheme).Build(),
			func(context.Context) bool { return true },
		),
	)
	t.Cleanup(host.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(25 * time.Second).
		MustNavigate(host.URL).
		MustWaitLoad()
	page.MustEval(`async (url) => {
		await new Promise((resolve,reject) => {const s=document.createElement('script');s.src='/assets/ui-contract.js';s.onload=resolve;s.onerror=reject;document.head.append(s);});
		window.states=[];
		const frame=document.querySelector('#product-surface');
		window.disposeUI=DataProductUI.mount({frame,manifest:{url,title:'Fixture',contract:{apiVersion:'data-product-ui/v1',hostOrigins:[location.origin],capabilities:['status','resize']}},grants:['status'],onState:s=>states.push(s)});
	}`, child.URL)
	page.MustWait(`() => states.includes('ready')`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustEval(
		`() => { parent.postMessage({apiVersion:'data-product-ui/v1',type:'status',session:'wrong',state:'error'},hostOrigin); parent.postMessage({apiVersion:'v2',type:'status',session,state:'error'},hostOrigin); parent.postMessage({apiVersion:'data-product-ui/v1',type:'resize',session,height:1100},hostOrigin); parent.postMessage({apiVersion:'data-product-ui/v1',type:'status',session,state:'<img src=x onerror=alert(1)>'},hostOrigin); parent.postMessage({apiVersion:'data-product-ui/v1',type:'status',session,state:'ready'},hostOrigin); }`,
	)
	page.MustWait(`() => states.filter(s=>s==='ready').length === 2`)
	if got := page.MustEval(`() => ({error:states.includes('error'),height:document.querySelector('iframe').style.height,sandbox:document.querySelector('iframe').getAttribute('sandbox')})`).
		JSON("", ""); got != "{\"error\":false,\"height\":\"\",\"sandbox\":\"allow-forms allow-scripts\"}" {
		t.Fatalf("boundary result: %s", got)
	}
	// The correct token from a different window or a non-opaque origin must not authenticate.
	session := frame.MustEval(`() => session`).Str()
	page.MustEval(`(session) => {
		const data={apiVersion:'data-product-ui/v1',type:'status',session,state:'error'};
		dispatchEvent(new MessageEvent('message',{origin:'null',source:window,data}));
		dispatchEvent(new MessageEvent('message',{origin:location.origin,source:document.querySelector('iframe').contentWindow,data}));
	}`, session)
	if page.MustEval(`() => states.includes('error')`).Bool() {
		t.Fatal("spoofed message accepted")
	}
	frame.MustEval(
		`() => parent.postMessage({apiVersion:'data-product-ui/v1',type:'status',session,state:'error'},hostOrigin)`,
	)
	page.MustWait(`() => states.includes('error')`)
	// Navigation rotates the session even though the iframe window stays the same.
	frame.MustEval(`() => location.reload()`)
	page.MustWait(`() => states.filter(s=>s==='loading').length === 3`)
	page.MustWait(`() => states.at(-1) === 'ready'`)
	page.MustEval(`(oldSession) => {
		window.states=[];
		dispatchEvent(new MessageEvent('message',{origin:'null',source:document.querySelector('iframe').contentWindow,
		data:{apiVersion:'data-product-ui/v1',type:'status',session:oldSession,state:'error'}}));
	}`, session)
	if page.MustEval(`() => states.length`).Int() != 0 {
		t.Fatal("navigation accepted the previous session")
	}
	page.MustEval(`() => disposeUI()`)
	if page.MustEval(`() => document.querySelector('iframe').hasAttribute('src')`).Bool() {
		t.Fatal("disposed frame remains navigated")
	}
	// A different grant set allows only bounded resize; status remains untrusted.
	page.MustEval(`(url) => {
		window.states=[];
		window.disposeUI=DataProductUI.mount({frame:document.querySelector('iframe'),manifest:{url,title:'Fixture',contract:{apiVersion:'data-product-ui/v1',hostOrigins:[location.origin],capabilities:['status','resize']}},grants:['resize'],onState:s=>states.push(s)});
	}`, child.URL)
	page.MustWait(`() => states.includes('ready')`)
	resizedFrame := page.MustElement("#product-surface").MustFrame()
	page.MustEval(
		`() => {window.barrierReached=false; addEventListener('message', e => {if(e.source === document.querySelector('iframe').contentWindow && e.data.type === 'test-barrier') window.barrierReached=true;});}`,
	)
	resizedFrame.MustEval(`() => {
		parent.postMessage({apiVersion:'data-product-ui/v1',type:'status',session,state:'error'},hostOrigin);
		for(const height of [239,1201,500.5,'640']) parent.postMessage({apiVersion:'data-product-ui/v1',type:'resize',session,height},hostOrigin);
		parent.postMessage({type:'test-barrier'},hostOrigin);
	}`)
	page.MustWait(`() => barrierReached`)
	if page.MustEval(`() => document.querySelector('iframe').style.height`).Str() != "" {
		t.Fatal("out-of-bounds resize was applied")
	}
	resizedFrame.MustEval(
		`() => parent.postMessage({apiVersion:'data-product-ui/v1',type:'resize',session,height:640},hostOrigin)`,
	)
	page.MustWait(`() => document.querySelector('iframe').style.height === '640px'`)
	if page.MustEval(`() => states.includes('error')`).Bool() {
		t.Fatal("ungranted status was accepted")
	}
	resizedFrame.MustEval(
		`() => {for(let i=0;i<257;i++) parent.postMessage({apiVersion:'data-product-ui/v1',type:'resize',session,height:640},hostOrigin);}`,
	)
	page.MustWait(
		`() => states.includes('error') && !document.querySelector('iframe').hasAttribute('src')`,
	)
}

// TestUIManifestValidation rejects unsafe contracts before any iframe navigation.
func TestUIManifestValidation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(registry.NewHandler(
		fake.NewClientBuilder().
			WithScheme(scheme).
			Build(),
		func(context.Context) bool { return true },
	))
	t.Cleanup(server.Close)
	browser := contractBrowser(t)
	page := browser.MustPage().Timeout(20 * time.Second).MustNavigate(server.URL).MustWaitLoad()
	result := page.MustEval(`async () => {
		await new Promise((resolve, reject) => {
			const script = document.createElement('script');
			script.src = '/assets/ui-contract.js'; script.onload = resolve;
			script.onerror = () => reject(new Error('portable UI library unavailable'));
			document.head.append(script);
		});
		const valid = {url:'https://product.example/ui',title:'Explore',contract:{apiVersion:'data-product-ui/v1',hostOrigins:[location.origin],capabilities:['status','resize']}};
		const accepted = DataProductUI.validate(valid, location.origin);
		if (accepted.url !== valid.url) return 'valid entrypoint changed';
		const invalid = [
			v => v.url = 'javascript:alert(1)',
			v => v.url = 'https://user:password@product.example/ui',
			v => v.url = 'https://product.example/ui#token',
			v => v.title = '',
			v => v.title = 'x'.repeat(201),
			v => v.contract.apiVersion = 'data-product-ui/v2',
			v => v.contract.hostOrigins = ['*'],
			v => v.contract.hostOrigins.push('https://*.example'),
			v => v.contract.hostOrigins.push('https://-invalid.example'),
			v => v.contract.hostOrigins.push('https://catalog.example:0'),
			v => v.contract.hostOrigins = [location.origin + '/'],
			v => v.contract.hostOrigins = ['https://other.example'],
			v => v.contract.capabilities = ['credentials'],
			v => v.contract.capabilities = ['status','status'],
			v => v.contract.unexpected = true,
			v => v.extra = 'x'.repeat(17000),
		];
		for (let i=0;i<invalid.length;i++) {
			const value = structuredClone(valid); invalid[i](value);
			try { DataProductUI.validate(value, location.origin); return 'accepted invalid case '+i; } catch { /* Expected refusal. */ }
		}
		return 'validated';
	}`).Str()
	if result != "validated" {
		t.Fatal(result)
	}
}

// contractBrowser runs protocol checks in Chromium with certificates confined to TLS fixtures.
func contractBrowser(t *testing.T) *rod.Browser {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	controlURL, err := launcher.New().
		Context(ctx).
		Headless(true).
		NoSandbox(true).
		Set("ignore-certificate-errors").
		Launch()
	if err != nil {
		t.Fatal(err)
	}
	browser := rod.New().
		Context(t.Context()).
		Timeout(60 * time.Second).
		ControlURL(controlURL).
		WithPanic(func(value interface{}) { t.Fatalf("browser: %v", value) }).
		MustConnect()
	t.Cleanup(func() { _ = browser.Context(context.Background()).Timeout(3 * time.Second).Close() })
	return browser
}
