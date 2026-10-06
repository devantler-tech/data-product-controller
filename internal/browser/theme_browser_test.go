//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/web"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestWorkspaceThemes changes appearance without reloading a sandboxed product or losing its query.
func TestWorkspaceThemes(t *testing.T) {
	page, loads := themeWorkspace(t, false, true, true, true)
	if !page.MustEval(`() => !!document.querySelector('select[aria-label="Theme"]')`).Bool() {
		t.Fatal("workspace has no accessible theme selector")
	}
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	frame.MustElement("#station").MustSelect("Nordhavn")
	frame.MustElement("button[type=submit]").MustClick()
	frame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	page.MustElement("#appearance").MustSelect("Dark")
	waitTheme(t, page, frame, "rgb(0, 0, 0)")
	page.MustElement("#appearance").MustFocus()
	page.Keyboard.MustType(input.KeyL)
	waitTheme(t, page, frame, "rgb(255, 255, 255)")
	if loads.Load() != 1 || frame.MustElement("#status").MustText() != "1 observation" ||
		!strings.Contains(frame.MustElement("#observations").MustText(), "Nordhavn") {
		t.Fatal("appearance change reloaded the product or lost the selected query")
	}
	if sandbox := page.MustElement("#product-surface").
		MustAttribute("sandbox"); sandbox == nil ||
		*sandbox != "allow-forms allow-scripts" {
		t.Fatal("appearance change widened the product sandbox")
	}
	page.MustElement("#appearance").MustSelect("Dark")
	page.MustReload().MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	if got := page.MustElement("#appearance").MustProperty("value").Str(); got != "dark" {
		t.Fatalf("explicit appearance was not restored: %q", got)
	}
	page.MustElement("html").
		MustWait(`() => getComputedStyle(this).backgroundColor === 'rgb(0, 0, 0)'`)
	page.MustSetViewport(375, 812, 1, false)
	if page.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
		t.Fatal("theme controls overflow the mobile workspace")
	}
}

// TestKitAppearanceGrant requires a selected grant before the independent kit can theme the sample.
func TestKitAppearanceGrant(t *testing.T) {
	kit := httptest.NewTLSServer(
		web.KitHandlerWithAppearance(func() bool { return true }, func() bool { return true }),
	)
	t.Cleanup(kit.Close)
	var handler http.Handler
	product := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }),
	)
	t.Cleanup(product.Close)
	var err error
	handler, err = demoproduct.NewHandlerWithOptions(
		demoproduct.HandlerOptions{
			PublicBaseURL:     product.URL,
			HostOrigins:       []string{kit.URL},
			AppearanceEnabled: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(
		datav1alpha1.ProductUI{
			URL:   product.URL + "/ui",
			Title: "Harbour observations",
			Contract: &datav1alpha1.UIContract{
				APIVersion:   "data-product-ui/v2",
				HostOrigins:  []datav1alpha1.UIHostOrigin{datav1alpha1.UIHostOrigin(kit.URL)},
				Capabilities: []datav1alpha1.UICapability{"appearance"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(20 * time.Second).
		MustNavigate(kit.URL).
		MustWaitLoad()
	page.MustElement("#manifest").MustInput(string(manifest))
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	page.MustElement("#appearance").MustSelect("Dark")
	if frame.MustEval(`() => document.documentElement.hasAttribute('data-appearance')`).Bool() {
		t.Fatal("unselected appearance grant changed the sample")
	}
	page.MustElement("#close").MustClick()
	page.MustElement("#grant-appearance").MustClick()
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame = page.MustElement("#product-surface").MustFrame()
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
	frame.MustElement("#station").MustSelect("Nordhavn")
	frame.MustElement("button[type=submit]").MustClick()
	frame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	page.MustElement("#appearance").MustSelect("Light")
	frame.MustElement("html").
		MustWait(`() => getComputedStyle(this).backgroundColor === 'rgb(255, 255, 255)'`)
	if frame.MustElement("#status").MustText() != "1 observation" {
		t.Fatal("portable kit appearance lost the current query")
	}
}

// TestSystemThemeWithDeniedStorage follows preference changes even when browser storage is unavailable.
func TestSystemThemeWithDeniedStorage(t *testing.T) {
	page, _ := themeWorkspace(t, true, true, true, true)
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("#status").MustWait(`() => this.textContent === '2 observations'`)
	for _, theme := range []struct{ preference, background string }{{"dark", "rgb(0, 0, 0)"}, {"light", "rgb(255, 255, 255)"}} {
		if err := (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: theme.preference}}}).Call(
			page,
		); err != nil {
			t.Fatal(err)
		}
		waitTheme(t, page, frame, theme.background)
	}
	if err := (proto.EmulationSetEmulatedMedia{}).Call(page); err != nil {
		t.Fatal(err)
	}
	background := "rgb(255, 255, 255)"
	if page.MustEval(`() => matchMedia('(prefers-color-scheme: dark)').matches`).Bool() {
		background = "rgb(0, 0, 0)"
	}
	page.MustElement("#appearance").MustSelect("Dark")
	waitTheme(t, page, frame, "rgb(0, 0, 0)")
	page.MustElement("#appearance").MustSelect("System")
	waitTheme(t, page, frame, background)
}

// TestAppearanceGates refuses v2 navigation unless both independently controlled gates are enabled.
func TestAppearanceGates(t *testing.T) {
	for _, gates := range []struct{ contract, appearance bool }{{false, false}, {true, false}, {false, true}} {
		t.Run(
			fmt.Sprintf("contract=%t/appearance=%t", gates.contract, gates.appearance),
			func(t *testing.T) {
				page, loads := themeWorkspace(
					t,
					false,
					gates.contract,
					gates.appearance,
					gates.contract && gates.appearance,
				)
				page.MustElement(".product-card").MustClick()
				page.MustElement("#ui-contract-status").
					MustWait(`() => this.dataset.state === 'unavailable'`)
				if loads.Load() != 0 ||
					page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
						Bool() {
					t.Fatal("disabled appearance contract navigated a product")
				}
			},
		)
	}
}

// TestAppearanceMessageBoundary rejects cosmetic messages from the wrong parent, origin or session.
func TestAppearanceMessageBoundary(t *testing.T) {
	page, _ := themeWorkspace(t, false, true, true, true)
	page.MustElement("#appearance").MustSelect("Light")
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'light'`)
	frame.MustEval(`() => {
		const valid = {apiVersion:'data-product-ui/v2', type:'appearance', session:connection.session, appearance:'dark'};
		for (const variant of [
			{data:Object.assign([], valid)},
			{data:Object.assign([], {apiVersion:'data-product-ui/v2',type:'init',session:connection.session,capabilities:connection.capabilities})},
			{source:window}, {origin:'https://unapproved.example'},
			{data:{...valid,session:'00000000-0000-0000-0000-000000000000'}},
			{data:{...valid,apiVersion:'data-product-ui/v1'}},
			{data:{...valid,appearance:'url(https://unapproved.example)'}},
			{data:{...valid,css:'background:red'}},
		]) dispatchEvent(new MessageEvent('message', {source:parent, origin:connection.origin, data:valid, ...variant}));
	}`)
	if got := frame.MustElement("html").
		MustAttribute("data-appearance"); got == nil ||
		*got != "light" {
		t.Fatal("unapproved message changed the product palette")
	}
	oldSession := frame.MustEval(`() => connection.session`).Str()
	page.MustElement("#appearance").MustSelect("Dark")
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
	page.MustEval(
		`() => {window.productNavigated=false; document.querySelector('#product-surface').addEventListener('load', () => {window.productNavigated=true}, {once:true})}`,
	)
	frame.MustEval(`() => location.reload()`)
	page.MustWait(
		`() => productNavigated && document.querySelector('#ui-contract-status').dataset.state === 'ready'`,
	)
	frame = page.MustElement("#product-surface").MustFrame()
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
	if got := frame.MustEval(`() => connection.session`).Str(); got == oldSession {
		t.Fatal("appearance connection reused the previous navigation session")
	}
	frame.MustEval(
		`(session) => dispatchEvent(new MessageEvent('message', {source:parent,origin:connection.origin,
		data:{apiVersion:'data-product-ui/v2',type:'appearance',session,appearance:'light'}}))`,
		oldSession,
	)
	if got := frame.MustElement("html").
		MustAttribute("data-appearance"); got == nil ||
		*got != "dark" {
		t.Fatal("previous session changed a newly navigated product")
	}
}

// TestProductAppearanceGate refuses the host handshake when only the product gate is disabled.
func TestProductAppearanceGate(t *testing.T) {
	page, loads := themeWorkspace(t, false, true, true, false)
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'timeout'`)
	if loads.Load() != 1 ||
		page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
			Bool() {
		t.Fatal("product appearance gate did not fail closed after refusing the handshake")
	}
}

// TestAppearanceSessionLimits revokes each side of the connection after its bounded hint allowance.
func TestAppearanceSessionLimits(t *testing.T) {
	for _, side := range []string{"host", "product"} {
		t.Run(side, func(t *testing.T) {
			page, _ := themeWorkspace(t, false, true, true, true)
			page.MustElement("#appearance").MustSelect("Light")
			page.MustElement(".product-card").MustClick()
			page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
			frame := page.MustElement("#product-surface").MustFrame()
			frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'light'`)
			if side == "host" {
				page.MustEval(
					`() => {for(let i=0;i<255;i++) disposeSurface.setAppearance(i%2===0?'dark':'light')}`,
				)
				frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
				if !page.MustElement("#product-surface").MustVisible() {
					t.Fatal("host revoked the connection before its hint allowance")
				}
				page.MustEval(`() => disposeSurface.setAppearance('light')`)
				page.MustElement("#ui-contract-status").
					MustWait(`() => this.dataset.state === 'error'`)
				if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
					Bool() {
					t.Fatal("host retained navigation after exhausting its hint allowance")
				}
			} else {
				frame.MustEval(
					`() => {for(let i=0;i<255;i++) dispatchEvent(new MessageEvent('message', {
					source:parent,origin:connection.origin,data:{apiVersion:'data-product-ui/v2',type:'appearance',
					session:connection.session,appearance:i%2===0?'dark':'light'}}))}`,
				)
				if !frame.MustEval(`() => !!connection && document.documentElement.dataset.appearance === 'dark'`).
					Bool() {
					t.Fatal("product revoked the connection before its hint allowance")
				}
				frame.MustEval(`() => {const origin=connection.origin, session=connection.session;
					for(let i=0;i<2;i++) dispatchEvent(new MessageEvent('message', {source:parent,origin,
					data:{apiVersion:'data-product-ui/v2',type:'appearance',session,appearance:'light'}}))}`)
				if !frame.MustEval(`() => !connection && document.documentElement.dataset.appearance === 'dark'`).
					Bool() {
					t.Fatal("product accepted a hint after exhausting its session allowance")
				}
			}
		})
	}
}

// waitTheme observes the actual palette in both documents, rather than a host-only state attribute.
func waitTheme(t *testing.T, page, frame *rod.Page, background string) {
	t.Helper()
	for _, document := range []*rod.Page{page, frame} {
		document.MustElement("html").
			MustWait(`(color) => getComputedStyle(this).backgroundColor === color`, background)
	}
}

// themeWorkspace serves the real registry and sample under separate origins with an unchanged opaque sandbox.
func themeWorkspace(
	t *testing.T,
	deniedStorage, contractEnabled, appearanceEnabled, productAppearanceEnabled bool,
) (*rod.Page, *atomic.Int32) {
	t.Helper()
	var loads atomic.Int32
	var handler http.Handler
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ui" {
			loads.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(product.Close)
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var registryHandler http.Handler
	server := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { registryHandler.ServeHTTP(w, r) },
		),
	)
	t.Cleanup(server.Close)
	published := workspaceProduct(product.URL)
	published.Spec.UI.Contract = &datav1alpha1.UIContract{
		APIVersion:   "data-product-ui/v2",
		HostOrigins:  []datav1alpha1.UIHostOrigin{datav1alpha1.UIHostOrigin(server.URL)},
		Capabilities: []datav1alpha1.UICapability{"status", "resize", "appearance"},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(published).Build()
	registryHandler = registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
		ContractEnabled:   func(context.Context) bool { return contractEnabled },
		AppearanceEnabled: func(context.Context) bool { return appearanceEnabled },
	})
	var err error
	handler, err = demoproduct.NewHandlerWithOptions(demoproduct.HandlerOptions{
		PublicBaseURL: product.URL,
		HostOrigins: []string{
			server.URL,
		},
		AppearanceEnabled: productAppearanceEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	page := contractBrowser(t).MustPage().Timeout(15 * time.Second)
	if deniedStorage {
		page.MustEvalOnNewDocument(
			`Object.defineProperty(window, 'localStorage', {get() {throw new DOMException('Denied', 'SecurityError')}})`,
		)
	}
	page.MustNavigate(server.URL).MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible()
	return page, &loads
}

// TestDuplicateAppearanceHintsPreserveQuery retains the connection and its allowance for actual changes.
func TestDuplicateAppearanceHintsPreserveQuery(t *testing.T) {
	page, loads := themeWorkspace(t, false, true, true, true)
	page.MustElement("#appearance").MustSelect("Light")
	page.MustElement(".product-card").MustClick()
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame := page.MustElement("#product-surface").MustFrame()
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'light'`)
	frame.MustElement("#station").MustSelect("Nordhavn")
	frame.MustElement("button[type=submit]").MustClick()
	frame.MustElement("#status").MustWait(`() => this.textContent === '1 observation'`)
	session := frame.MustEval(`() => connection.session`).Str()
	page.MustEval(`() => {for(let i=0;i<1024;i++) disposeSurface.setAppearance('light')}`)
	if loads.Load() != 1 || !page.MustElement("#product-surface").MustVisible() ||
		frame.MustEval(`() => connection.session`).Str() != session ||
		frame.MustElement("#status").MustText() != "1 observation" ||
		!strings.Contains(frame.MustElement("#observations").MustText(), "Nordhavn") {
		t.Fatal("duplicate appearance hints revoked the session or lost its query")
	}
	page.MustEval(
		`() => {for(let i=0;i<255;i++) disposeSurface.setAppearance(i%2===0?'dark':'light')}`,
	)
	frame.MustElement("html").MustWait(`() => this.dataset.appearance === 'dark'`)
	if !page.MustElement("#product-surface").MustVisible() {
		t.Fatal("duplicate hints consumed the distinct-change allowance")
	}
	page.MustEval(`() => disposeSurface.setAppearance('light')`)
	page.MustElement("#ui-contract-status").MustWait(`() => this.dataset.state === 'error'`)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() {
		t.Fatal("distinct changes bypassed the appearance allowance")
	}
}
