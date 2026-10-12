//go:build browser

package browser_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/go-rod/rod"
)

const (
	releaseEndpoint = "https://api.github.com/repos/devantler-tech/ksail/releases"
	releaseFixture  = `[{"tag_name":"v1.0.0","published_at":"2026-10-01T12:00:00Z","draft":false,"prerelease":false,"body":"<script>throw 1</script>"},{"tag_name":"v1.1.0-rc.1","published_at":"2026-10-02T12:00:00Z","draft":false,"prerelease":true}]`
)

type releaseBrowser struct {
	page        *rod.Page
	frame       *rod.Page
	requests    *atomic.Int32
	credentials *atomic.Bool
}

func releaseBrowserPage(t *testing.T, body string, contentType string, code int) releaseBrowser {
	t.Helper()
	var productHandler atomic.Value
	productHandler.Store(demoproduct.NewHandler())
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixtureValue[http.Handler](t, productHandler.Load()).ServeHTTP(w, r)
	}))
	t.Cleanup(product.Close)
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(
			w,
			`<style>body{margin:0}iframe{width:100%%;height:800px;border:0}</style><iframe sandbox="allow-forms allow-scripts" src="%s/ksail-releases/ui"></iframe>`,
			product.URL,
		)
	}))
	t.Cleanup(host.Close)
	options := demoproduct.HandlerOptions{
		PublicBaseURL:     product.URL,
		HostOrigins:       []string{host.URL},
		AppearanceEnabled: true,
	}
	if err := json.Unmarshal([]byte(`{"KSailReleasesEnabled":true}`), &options); err != nil {
		t.Fatal(err)
	}
	handler, err := demoproduct.NewHandlerWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	productHandler.Store(handler)
	browser := contractBrowser(t)
	requests := &atomic.Int32{}
	credentials := &atomic.Bool{}
	router := browser.HijackRequests()
	router.MustAdd(releaseEndpoint, func(h *rod.Hijack) {
		requests.Add(1)
		for name, value := range h.Request.Headers() {
			if (strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization")) &&
				value.Str() != "" {
				credentials.Store(true)
			}
		}
		h.Response.Payload().ResponseCode = code
		h.Response.SetHeader("Content-Type", contentType, "Access-Control-Allow-Origin", "*").
			SetBody(body)
	})
	go router.Run()
	t.Cleanup(func() { _ = router.Stop() })
	page := browser.MustPage().Timeout(20 * time.Second).MustNavigate(host.URL).MustWaitLoad()
	frame := page.MustElement("iframe").MustFrame()
	frame.MustElement("#status").MustWait(`() => !this.textContent.startsWith('Loading')`)
	return releaseBrowser{page, frame, requests, credentials}
}

func TestKSailReleaseBrowserFiltersWithoutSourceReads(t *testing.T) {
	view := releaseBrowserPage(t, releaseFixture, "application/json; charset=utf-8", 200)
	t.Log("source page loaded")
	if view.requests.Load() != 1 {
		t.Fatalf("public source requests = %d, want 1", view.requests.Load())
	}
	if got := view.frame.MustElement("#status").MustText(); got != "2 of 2 releases" {
		t.Fatalf("release status = %q", got)
	}
	if view.credentials.Load() || len(view.frame.MustElements(".release")) != 2 ||
		len(view.frame.MustElements("#observations script")) != 0 {
		t.Fatal("source view forwarded credentials, lost rows or rendered source markup")
	}
	view.frame.MustElement("#release-search").MustInput("v1.0")
	view.frame.MustElement("#status").MustWait(`() => this.textContent === '1 of 2 releases'`)
	t.Log("version filter applied")
	view.frame.MustElement("#station").MustSelect("Pre-release")
	view.frame.MustElement("#status").
		MustWait(`() => this.textContent.startsWith('No releases match')`)
	t.Log("channel filter applied")
	view.frame.MustElement("#release-search").MustSelectAllText().MustInput("")
	view.frame.MustElement("#status").MustWait(`() => this.textContent === '1 of 2 releases'`)
	if view.requests.Load() != 1 {
		t.Fatal("local filters queried the source")
	}
	t.Log("version filter cleared")
	view.frame.MustElement("button[type=submit]").MustClick()
	view.frame.MustElement("#status").MustWait(`() => this.textContent === '1 of 2 releases'`)
	if view.requests.Load() != 2 {
		t.Fatal("explicit refresh did not read the source once")
	}
	t.Log("explicit refresh complete")
	view.page.MustEval(`() => {
		window.productMessages = [];
		window.addEventListener('message', (event) => window.productMessages.push(event.data));
		document.querySelector('iframe').contentWindow.postMessage({apiVersion:'data-product-ui/v2',type:'init',session:'11111111-1111-1111-1111-111111111111',capabilities:['appearance']}, '*');
	}`)
	view.page.MustElement("iframe").
		MustWait(`() => window.productMessages.some(message => message.type === 'ready')`)
	t.Log("presentation session ready")
	view.page.MustEval(
		`() => document.querySelector('iframe').contentWindow.postMessage({apiVersion:'data-product-ui/v2',type:'appearance',session:'11111111-1111-1111-1111-111111111111',appearance:'dark'}, '*')`,
	)
	view.frame.MustElement("html").
		MustWait(`() => getComputedStyle(this).backgroundColor === 'rgb(0, 0, 0)'`)
	if view.frame.MustElement("#status").MustText() != "1 of 2 releases" {
		t.Fatal("appearance lost the loaded selection")
	}
	view.page.MustSetViewport(375, 812, 1, false)
	if view.frame.MustEval(`() => document.documentElement.scrollWidth > innerWidth`).Bool() {
		t.Fatal("release view overflows a narrow screen")
	}
	if sandbox := view.page.MustElement("iframe").
		MustAttribute("sandbox"); sandbox == nil ||
		*sandbox != "allow-forms allow-scripts" {
		t.Fatal("release view weakened the opaque-origin sandbox")
	}
}

func TestKSailReleaseBrowserRejectsUnavailableOrInvalidPages(t *testing.T) {
	tooMany := make([]string, 31)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(
			`{"tag_name":"v1.0.%d","published_at":"2026-10-01T12:00:00Z","draft":false,"prerelease":false}`,
			i,
		)
	}
	cases := []struct {
		name, body, contentType string
		code                    int
	}{
		{"quota", `{"message":"limited"}`, "application/json", 403},
		{"partial", releaseFixture, "application/json", 206},
		{"HTML", releaseFixture, "text/html", 200},
		{"malformed", `{"incomplete"`, "application/json", 200},
		{"not-array", `{}`, "application/json", 200},
		{
			"bad-date",
			strings.ReplaceAll(releaseFixture, "2026-10-01", "2026-02-31"),
			"application/json",
			200,
		},
		{
			"draft",
			strings.ReplaceAll(releaseFixture, `"draft":false`, `"draft":true`),
			"application/json",
			200,
		},
		{
			"markup-version",
			strings.ReplaceAll(releaseFixture, "v1.0.0", "<img src=x onerror=alert(1)>"),
			"application/json",
			200,
		},
		{"records", "[" + strings.Join(tooMany, ",") + "]", "application/json", 200},
		{"bytes", strings.Repeat(" ", 1024*1024) + "[]", "application/json", 200},
		{
			"invalid-UTF8",
			`[{"tag_name":"v1.0.0","published_at":"2026-10-01T12:00:00Z","draft":false,"prerelease":false,"body":"` + string(
				[]byte{255},
			) + `"}]`,
			"application/json",
			200,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := releaseBrowserPage(t, tc.body, tc.contentType, tc.code)
			if view.requests.Load() != 1 ||
				!strings.HasPrefix(
					view.frame.MustElement("#status").MustText(),
					"Could not read GitHub releases",
				) ||
				len(view.frame.MustElements(".release")) != 0 {
				t.Fatal("invalid source page did not fail closed")
			}
			view.frame.MustElement("#release-search").MustInput("v1")
			if !strings.HasPrefix(
				view.frame.MustElement("#status").MustText(),
				"Could not read GitHub releases",
			) {
				t.Fatal("a local filter erased source failure")
			}
		})
	}
}

// TestKSailReleaseBrowserDiscardsLateReads covers a source that ignores abort during refresh/history.
func TestKSailReleaseBrowserDiscardsLateReads(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprintf("history=%t", history), func(t *testing.T) {
			view := releaseBrowserPage(t, releaseFixture, "application/json", 200)
			view.frame.MustEval(`() => {
				const original = window.fetch;
				let calls = 0;
				window.oldFinished = false;
				window.fetch = (url, options) => {
					if (String(url) !== 'https://api.github.com/repos/devantler-tech/ksail/releases') return original(url, options);
					if (++calls === 1) {
						window.oldSignal = options.signal;
						return new Promise(resolve => { window.releaseOld = () => {
							const response = new Response('[{"tag_name":"v1.0.0","published_at":"2026-10-01T12:00:00Z","draft":false,"prerelease":false}]', {headers:{'Content-Type':'application/json'}});
							const getReader = response.body.getReader.bind(response.body);
							response.body.getReader = () => {
								const reader = getReader();
								return {read: async () => {const part = await reader.read(); if(part.done)window.oldFinished=true; return part;},cancel:()=>reader.cancel(),releaseLock:()=>reader.releaseLock()};
							};
							resolve(response);
						}; });
					}
					return Promise.resolve(new Response('[{"tag_name":"v2.0.0","published_at":"2026-10-02T12:00:00Z","draft":false,"prerelease":false}]', {headers:{'Content-Type':'application/json'}}));
				};
			}`)
			view.frame.MustElement("button[type=submit]").MustClick()
			view.frame.MustElement("#status").
				MustWait(`() => this.textContent === 'Loading releases…' && !!window.releaseOld`)
			view.frame.MustElement("#release-search").MustInput("v2")
			if view.frame.MustElement("#status").MustText() != "Loading releases…" {
				t.Fatal("filter replaced pending-source state")
			}
			if history {
				view.frame.MustEval(
					`() => window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))`,
				)
				if len(view.frame.MustElements(".release")) != 0 {
					t.Fatal("page exit retained release rows")
				}
				view.frame.MustEval(
					`() => window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))`,
				)
			} else {
				view.frame.MustElement("button[type=submit]").MustClick()
			}
			view.frame.MustElement("#status").
				MustWait(`() => this.textContent === '1 of 1 releases'`)
			if !view.frame.MustEval(`() => window.oldSignal.aborted`).Bool() {
				t.Fatal("superseded source request was not aborted")
			}
			view.frame.MustEval(`() => window.releaseOld()`)
			view.frame.MustElement("#status").MustWait(`() => window.oldFinished`)
			if got := view.frame.MustEval(`async () => {await new Promise(resolve=>requestAnimationFrame(resolve)); return document.querySelector('#observations').textContent;}`).
				Str(); !strings.Contains(
				got,
				"v2.0.0",
			) ||
				strings.Contains(got, "v1.0.0") {
				t.Fatal("late source response replaced the current page")
			}
		})
	}
}
