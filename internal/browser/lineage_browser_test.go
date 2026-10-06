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
	"github.com/devantler-tech/data-product-controller/internal/controller"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/go-rod/rod/lib/input"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// lineageFixture serves the real workspace and API with synthetic public products and controllable failures.
func lineageFixture(
	t *testing.T,
	override ...*atomic.Value,
) (*httptest.Server, *atomic.Int64, *atomic.Bool, *atomic.Bool) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	root := workspaceProduct("https://example.test")
	root.Namespace = "products"
	root.Name = "root"
	root.Generation = 2
	root.Spec.Name = "Coastal summary"
	root.Spec.UI = nil
	root.Spec.Outputs[0].MediaType = ""
	root.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             "False",
			ObservedGeneration: 2,
			Reason:             "DependencyNotReady",
			Message:            "private-diagnostic",
		},
	}
	root.Spec.Inputs = []datav1alpha1.InputPort{
		{
			Name:       "observations",
			ProductRef: datav1alpha1.ProductReference{Name: "producer", Output: "observations"},
		},
		{
			Name:       "missing",
			ProductRef: datav1alpha1.ProductReference{Name: "missing", Output: "observations"},
		},
	}
	producer := root.DeepCopy()
	producer.Name = "producer"
	producer.Spec.Name = "<script>window.pwned=true</script>"
	producer.Spec.Owner.Name = "Harbour team"
	producer.Spec.Inputs = nil
	producer.Status.Conditions[0].Status = "True"
	producer.Status.Conditions[0].ObservedGeneration = 1
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(root, producer).Build()
	enabled := &atomic.Bool{}
	enabled.Store(true)
	calls := &atomic.Int64{}
	failed := &atomic.Bool{}
	delayed := &atomic.Bool{}
	real := registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
		DiscoveryEnabled:   func(context.Context) bool { return true },
		LineageEnabled:     func(context.Context) bool { return enabled.Load() },
		InputCompatibility: controller.DeclaredInputCompatibility,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/lineage") {
			calls.Add(1)
			if len(override) > 0 && override[0].Load() != nil {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(fixtureValue[[]byte](t, override[0].Load()))
				return
			}
			if failed.Load() {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			if delayed.Load() {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(250 * time.Millisecond):
				}
			}
		}
		real.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, calls, failed, delayed
}

// TestLineageWorkspace exercises an unhealthy root through real assets, export and upstream navigation.
func TestLineageWorkspace(t *testing.T) {
	server, calls, _, _ := lineageFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Froot").
		MustWaitLoad()
	page.MustElement("#interaction-title").MustWait(`()=>this.textContent==="Coastal summary"`)
	t.Log("selected root")
	if calls.Load() != 0 {
		t.Fatal("trace was fetched without a user action")
	}
	page.MustElement("#trace-inputs").MustWaitVisible().MustFocus().MustType(input.Enter)
	page.MustElement("#trace-status").MustWait(`()=>this.textContent.includes("Incomplete trace")`)
	t.Log("trace rendered")
	if strings.Contains(page.MustElement("#product-interfaces").MustText(), "undefined") {
		t.Fatal("optional interface metadata is rendered as undefined")
	}
	if got := page.MustElement("#trace-table tr[data-state='missing'] td:first-child").
		MustText(); strings.Count(
		got,
		"products/missing",
	) != 1 {
		t.Fatalf("missing product identity repeated: %q", got)
	}
	body := page.MustElement("#trace-table").MustText()
	for _, want := range []string{"products/producer", "Missing product", "Stale", "Harbour team", "Coastal summary"} {
		if !strings.Contains(body, want) {
			t.Fatalf("trace lacks %q: %s", want, body)
		}
	}
	if page.MustEval(`()=>window.pwned===true`).Bool() {
		t.Fatal("trace executed publisher metadata")
	}
	page.MustEval(
		`()=>{const create=URL.createObjectURL;URL.createObjectURL=function(blob){blob.text().then(text=>window.savedTraceValue=JSON.parse(text));return create.call(this,blob);};HTMLAnchorElement.prototype.click=function(){window.savedTraceName=this.download;};}`,
	)
	page.MustElement("#save-trace").MustWaitVisible().MustClick()
	page.MustWait(`()=>!!window.savedTraceValue&&!!window.savedTraceName`)
	t.Log("trace downloaded")
	saved := page.MustEval(`()=>({name:window.savedTraceName,value:window.savedTraceValue})`).
		JSON("", "")
	var export struct {
		Name  string
		Value struct {
			APIVersion, Root string
			Complete         bool
			Nodes, Edges     []any
		}
	}
	if err := json.Unmarshal([]byte(saved), &export); err != nil {
		t.Fatal(err)
	}
	if export.Name != "products-root-lineage.json" ||
		export.Value.APIVersion != "data-product-lineage/v1" ||
		export.Value.Complete ||
		len(export.Value.Edges) != 2 {
		t.Fatalf("export=%s", saved)
	}
	for _, theme := range []struct{ preference, color string }{
		{"Light", "rgb(24, 24, 24)"}, {"Dark", "rgb(244, 244, 244)"},
	} {
		page.MustElement("#appearance").MustSelect(theme.preference)
		page.MustElement("#trace-table").
			MustWait(`()=>getComputedStyle(this).color===` + "'" + theme.color + "'")
	}
	page.MustSetViewport(375, 812, 1, false)
	if page.MustEval(`()=>document.documentElement.scrollWidth>innerWidth`).Bool() {
		t.Fatal("trace tables overflow the phone workspace")
	}
	page.MustElement("#trace-table a[data-product='products/producer']").
		MustFocus().
		MustType(input.Enter)
	t.Log("navigated upstream")
	page.MustElement("#interaction-title").MustWait(`()=>this.textContent.includes("window.pwned")`)
	if page.MustEval(`()=>!document.querySelector("#trace-result").hidden||!document.querySelector("#save-trace").hidden`).
		Bool() {
		t.Fatal("trace survived product navigation")
	}
	if src := page.MustElement("#product-surface").MustAttribute("src"); src != nil {
		t.Fatal("trace navigation opened an unhealthy surface")
	}
}

// TestLineageRecoveryAndRevocation ensures retries work and delayed responses cannot restore a revoked selection.
func TestLineageRecoveryAndRevocation(t *testing.T) {
	server, calls, failed, delayed := lineageFixture(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Froot").
		MustWaitLoad()
	page.MustElement("#trace-inputs").MustWaitVisible()
	failed.Store(true)
	page.MustElement("#trace-inputs").MustClick()
	page.MustElement("#trace-status").MustWait(`()=>this.textContent.includes("Could not trace")`)
	if !page.MustEval(`()=>document.querySelector("#save-trace").hidden`).Bool() {
		t.Fatal("failed trace was downloadable")
	}
	failed.Store(false)
	page.MustElement("#trace-inputs").MustClick()
	page.MustElement("#trace-status").MustWait(`()=>this.textContent.includes("Incomplete trace")`)
	delayed.Store(true)
	page.MustElement("#trace-inputs").MustClick()
	page.MustElement("#trace-status").MustWait(`()=>this.textContent.includes("Tracing")`)
	page.MustElement("#refresh-products").MustClick()
	page.MustElement("#product-count").MustWait(`()=>this.textContent.includes("products")`)
	page.MustEval(`async()=>{await new Promise(r=>setTimeout(r,400));}`)
	if page.MustEval(`()=>!document.querySelector("#save-trace").hidden||!document.querySelector("#trace-result").hidden`).
		Bool() {
		t.Fatal("late trace replaced the refreshed selection")
	}
	if page.MustEval(`()=>document.querySelector("#dependency-trace").getAttribute("aria-busy")==="true"`).
		Bool() {
		t.Fatal("cancelled trace left the next selection busy")
	}
	if calls.Load() < 2 {
		t.Fatal("retry did not reach trace API")
	}
}

// TestLineageRejectsFalseCompleteAndPrivateFields keeps malformed or private snapshots out of rendering and export.
func TestLineageRejectsFalseCompleteAndPrivateFields(t *testing.T) {
	override := &atomic.Value{}
	server, _, _, _ := lineageFixture(t, override)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Froot").
		MustWaitLoad()
	page.MustElement("#trace-inputs").MustWaitVisible()
	for _, body := range []string{
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":2,"observedGeneration":2}],"edges":[{"from":"products/root","to":"products/root","input":"x","output":"query","depth":1,"state":"resolved","compatibility":"compatible"}]}`,
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":2,"observedGeneration":2},{"key":"products/producer","state":"ready","generation":2,"observedGeneration":2}],"edges":[{"from":"products/root","to":"products/producer","input":"x","output":"query","depth":1,"state":"resolved","compatibility":"compatible"},{"from":"products/producer","to":"products/root","input":"x","output":"query","depth":2,"state":"resolved","compatibility":"compatible"}]}`,
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":2,"observedGeneration":2},{"key":"products/producer","state":"ready","generation":2,"observedGeneration":2}],"edges":[{"from":"products/root","to":"products/producer","input":"x","output":"query","depth":1,"state":"resolved","compatibility":"compatible","requirement":null}]}`,
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":2,"observedGeneration":2}],"edges":[{"from":"products/root","to":"products/ghost","input":"x","output":"query","depth":1,"state":"resolved","compatibility":"compatible"}]}`,
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"missing","generation":0,"observedGeneration":0}],"edges":[]}`,
		`{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":2,"observedGeneration":2}],"edges":[],"credentials":"sentinel"}`,
	} {
		override.Store([]byte(body))
		page.MustElement("#trace-inputs").MustClick()
		page.MustElement("#trace-inputs").MustWait(`()=>!this.disabled`)
		if !strings.Contains(page.MustElement("#trace-status").MustText(), "Could not trace") {
			t.Fatalf("malformed trace rendered: %s", body)
		}
		if !page.MustEval(`()=>document.querySelector("#save-trace").hidden&&document.querySelector("#trace-result").hidden`).
			Bool() {
			t.Fatal("malformed trace downloadable")
		}
	}
}

// TestLineagePendingResponseCannotCrossPageExit revokes delayed observations before a document is restored.
func TestLineagePendingResponseCannotCrossPageExit(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%t", persisted), func(t *testing.T) {
			server, calls, _, _ := lineageFixture(t)
			page := contractBrowser(t).MustPage().Timeout(20 * time.Second).
				MustNavigate(server.URL + "?product=products%2Froot").MustWaitLoad()
			page.MustElement("#trace-inputs").MustWaitVisible()
			page.MustEval(`()=>{
				const originalFetch=window.fetch.bind(window);
				let held=false;
				window.fetch=async (url,options)=>{
					if(held || !String(url).endsWith('/lineage')) return originalFetch(url,options);
					held=true;
					// Deliver the real HTTP response even when this document cancels its read.
					const {signal,...transportOptions}=options;
					const response=await originalFetch(url,transportOptions);
					const body=await response.arrayBuffer();
					const delivered=new Response(body,{status:response.status,headers:response.headers});
					const getReader=delivered.body.getReader.bind(delivered.body);
					window.traceReadComplete=false;
					delivered.body.getReader=(...args)=>{
						const reader=getReader(...args);
						const read=reader.read.bind(reader),cancel=reader.cancel.bind(reader);
						let consumed=false;
						reader.read=async (...args)=>{const chunk=await read(...args);if(chunk.done) consumed=true;return chunk;};
						reader.cancel=async (...args)=>{
							await cancel(...args);
							// Signal only after EOF, cancellation, and the consumer's queued continuations.
							setTimeout(()=>window.traceReadComplete=consumed,0);
						};
						return reader;
					};
					return new Promise(resolve=>window.finishTrace=()=>resolve(delivered));
				};
			}`)
			page.MustElement("#trace-inputs").MustClick()
			page.MustWait(`()=>typeof window.finishTrace==='function'`)
			if calls.Load() != 1 {
				t.Fatal("pending trace did not reach the real lineage API once")
			}
			page.MustEval(
				`persisted=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted}))`,
				persisted,
			)
			if page.MustEval(`()=>!document.querySelector('#dependency-trace').hidden || document.querySelector('#dependency-trace').getAttribute('aria-busy')==='true'`).
				Bool() {
				t.Fatal("page exit retained a pending trace")
			}
			page.MustEval(`()=>finishTrace()`)
			page.MustWait(`()=>window.traceReadComplete===true`)
			if page.MustEval(`()=>!document.querySelector('#trace-result').hidden || !document.querySelector('#save-trace').hidden || document.querySelector('#trace-status').textContent!=='' || document.querySelectorAll('#trace-table tbody tr, #trace-edges tbody tr').length!==0`).
				Bool() {
				t.Fatal("late lineage response restored an exited document")
			}
			page.MustEval(
				`persisted=>dispatchEvent(new PageTransitionEvent('pageshow',{persisted}))`,
				persisted,
			)
			if !persisted {
				page.MustElement("#refresh-products").MustClick()
			}
			page.MustElement("#trace-inputs").MustWaitVisible()
			if calls.Load() != 1 {
				t.Fatal("document restoration fetched lineage without an explicit action")
			}
			page.MustElement("#trace-inputs").MustClick()
			page.MustElement("#trace-status").
				MustWait(`()=>this.textContent.includes('Incomplete trace')`)
			page.MustElement("#save-trace").MustWaitVisible()
			if calls.Load() != 2 {
				t.Fatal("restored document did not fetch a fresh lineage observation")
			}
		})
	}
}
