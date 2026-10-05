//go:build browser

package browser_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/preflight"
	"github.com/devantler-tech/data-product-controller/web"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// publisherReport uses the actual publisher evaluator, including dependency/source reordering.
func publisherReport(t *testing.T, cycle bool, uiURL string) preflight.BundleReport {
	t.Helper()
	product := func(name string) data.DataProduct {
		return data.DataProduct{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "data.devantler.tech/v1alpha1",
				Kind:       "DataProduct",
			},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "products"},
			Spec: data.DataProductSpec{
				ID:          "urn:example:" + name,
				Name:        name,
				Description: "Public observations",
				Version:     "v1.0.0",
				Owner: data.ProductOwner{
					Name: "Example team",
				},
				Outputs: []data.OutputPort{
					{
						Name:        "observations",
						Protocol:    data.ProtocolOpenAPI,
						URL:         "https://api.example.test/observations",
						ContractURL: "https://api.example.test/openapi.json",
					},
				},
			},
		}
	}
	consumer, producer := product("a-consumer"), product("z-producer")
	consumer.Spec.Inputs = []data.InputPort{
		{
			Name:       "upstream",
			ProductRef: data.ProductReference{Name: producer.Name, Output: "observations"},
		},
	}
	if cycle {
		producer.Spec.Inputs = []data.InputPort{
			{
				Name:       "loop",
				ProductRef: data.ProductReference{Name: consumer.Name, Output: "observations"},
			},
		}
	}
	if uiURL != "" {
		consumer.Spec.UI = &data.ProductUI{URL: uiURL, Title: "External product"}
	}
	readers := []io.Reader{}
	for _, p := range []data.DataProduct{consumer, producer} {
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, strings.NewReader(string(encoded)))
	}
	result := preflight.CheckBundle(t.Context(), readers, "")
	if result.Complete == cycle || (!cycle && !result.Valid) {
		t.Fatalf("unexpected evaluator fixture: %+v", result)
	}
	return result
}

func publisherHost(t *testing.T) *httptest.Server {
	t.Helper()
	host := httptest.NewTLSServer(
		web.KitHandlerWithOptions(web.KitOptions{PublisherEnabled: func() bool { return true }}),
	)
	t.Cleanup(host.Close)
	return host
}

// TestPublisherReviewWorkflow exercises report production, identity joins, export and keyboard themes.
func TestPublisherReviewWorkflow(t *testing.T) {
	var requests atomic.Int64
	product := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) },
		),
	)
	defer product.Close()
	report := publisherReport(t, false, product.URL)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(20 * time.Second).
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustElement(".import-fields details summary").MustClick()
	page.MustElement("#report-text").MustInput(string(encoded))
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	if !strings.Contains(page.MustElement("#report-summary").MustText(), "Complete") {
		t.Fatal("complete outcome missing")
	}
	page.MustElement("#product-list button[data-key='products/a-consumer']").MustClick()
	if !strings.Contains(page.MustElement("#product-requirements").MustText(), "composition") {
		t.Fatal("requirements joined by descriptor order instead of plan provenance")
	}
	page.MustElement("#product-search").MustInput("z-producer")
	if got := len(page.MustElements("#product-list button")); got != 1 {
		t.Fatalf("search returns %d products", got)
	}
	page.MustElement("#product-list button").MustClick()
	if strings.Contains(page.MustElement("#product-requirements").MustText(), "composition") {
		t.Fatal("UI-less producer inherited consumer requirements")
	}
	page.MustEval(
		`()=>{URL.createObjectURL=blob=>{window.downloaded=blob;return 'blob:'+location.origin+'/test';};HTMLAnchorElement.prototype.click=function(){window.downloadName=this.download;};}`,
	)
	page.MustElement("#export-preview").MustClick()
	download := page.MustEval(`async()=>await window.downloaded.text()`).Str()
	var got map[string]any
	if err := json.Unmarshal([]byte(download), &got); err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	for _, descriptor := range report.Descriptors {
		var candidate map[string]any
		if err := json.Unmarshal(descriptor, &candidate); err != nil {
			t.Fatal(err)
		}
		if candidate["name"] == "z-producer" {
			expected = candidate
		}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("download changed publication preview")
	}
	if page.MustEval(`()=>window.downloadName`).Str() != "products-z-producer.json" {
		t.Fatal("download lacks a safe public filename")
	}
	if !strings.Contains(page.MustElement("#plan-order").MustText(), "z-producer") ||
		!strings.Contains(page.MustElement("#plan-edges").MustText(), "upstream") {
		t.Fatal("named static plan missing")
	}
	page.MustElement("#theme").MustSelect("Light")
	page.MustElement("#theme").MustWait(`()=>this.value==='Light'`)
	if page.MustEval(`()=>document.documentElement.dataset.theme`).Str() != "light" {
		t.Fatal("light mode failed")
	}
	page.MustElement("#theme").MustSelect("Dark")
	page.MustElement("#theme").MustWait(`()=>this.value==='Dark'`)
	if page.MustEval(`()=>document.documentElement.dataset.theme`).Str() != "dark" {
		t.Fatal("dark mode failed")
	}
	page.MustElement("#report-file").MustFocus()
	page.KeyActions().Press(input.ShiftLeft).Type(input.Tab).Release(input.ShiftLeft).MustDo()
	if page.MustEval(`()=>document.activeElement.id`).Str() != "theme" {
		t.Fatal("theme is not keyboard focusable")
	}
	page.MustSetViewport(390, 844, 1, false)
	if page.MustEval(`()=>document.documentElement.scrollWidth>innerWidth`).Bool() {
		t.Fatal("publisher review overflows a small viewport")
	}
	page.MustElement("#clear-report").MustClick()
	if page.MustEval(`()=>!document.querySelector('#review').hidden || !document.querySelector('#export-preview').disabled`).
		Bool() {
		t.Fatal("clear retained active review/export")
	}
	if requests.Load() != 0 || len(page.MustElements("iframe")) != 0 {
		t.Fatal("offline review contacted or mounted a product")
	}
}

// TestPublisherSystemThemeWithDeniedStorage keeps System and manual controls usable without persistence.
func TestPublisherSystemThemeWithDeniedStorage(t *testing.T) {
	host := publisherHost(t)
	page := contractBrowser(t).MustPage().Timeout(20 * time.Second)
	page.MustEvalOnNewDocument(
		`Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Denied','SecurityError')}})`,
	)
	page.MustNavigate(host.URL + "/publisher-review").MustWaitLoad()
	for _, preference := range []string{"dark", "light"} {
		if err := (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: preference}}}).Call(
			page,
		); err != nil {
			t.Fatal(err)
		}
		page.MustWait(`theme=>document.documentElement.dataset.theme===theme`, preference)
	}
	page.MustElement("#theme").MustSelect("Dark")
	if page.MustEval(`()=>document.documentElement.dataset.theme`).Str() != "dark" {
		t.Fatal("storage denial broke live theme selection")
	}
}

// TestPublisherFindingsPreserveOutcome checks real cycle witnesses, filtering and incomplete previews.
func TestPublisherFindingsPreserveOutcome(t *testing.T) {
	report := publisherReport(t, true, "")
	encoded, _ := json.Marshal(report)
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(20 * time.Second).
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustElement(".import-fields details summary").MustClick()
	page.MustElement("#report-text").MustInput(string(encoded))
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	if !strings.Contains(page.MustElement("#report-summary").MustText(), "Incomplete") ||
		!strings.Contains(page.MustElement("#findings").MustText(), "Source 1") ||
		!strings.Contains(page.MustElement("#findings").MustText(), "/spec/inputs/0/productRef") {
		t.Fatal("incomplete report lost outcome or numeric witness")
	}
	page.MustElement("#findings details summary").MustClick()
	if !strings.Contains(page.MustElement("#findings details").MustText(), "Source 2") {
		t.Fatal("cycle witness lost its other declaration")
	}
	if len(page.MustElements("#product-list button")) != 0 ||
		!page.MustElement("#export-preview").MustProperty("disabled").Bool() {
		t.Fatal("incomplete report fabricated previews")
	}
	page.MustElement("#code-filter").MustInput("no-such-code")
	if !strings.Contains(page.MustElement("#finding-count").MustText(), "0") ||
		!strings.Contains(page.MustElement("#report-summary").MustText(), "Incomplete") {
		t.Fatal("filter presented no matches as a complete outcome")
	}
}

// TestPublisherOmittedFindingsKeepsGlobalTotals uses the evaluator's actual retention limit.
func TestPublisherOmittedFindingsKeepsGlobalTotals(t *testing.T) {
	consumer := data.DataProduct{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "data.devantler.tech/v1alpha1",
			Kind:       "DataProduct",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "unresolved", Namespace: "products"},
		Spec: data.DataProductSpec{
			ID:          "urn:example:unresolved",
			Name:        "Unresolved",
			Description: "Public metadata",
			Version:     "v1.0.0",
			Owner: data.ProductOwner{
				Name: "Example team",
			},
			Outputs: []data.OutputPort{
				{
					Name:        "observations",
					Protocol:    data.ProtocolOpenAPI,
					URL:         "https://api.example.test/observations",
					ContractURL: "https://api.example.test/openapi.json",
				},
			},
		},
	}
	for i := 0; i < 140; i++ {
		consumer.Spec.Inputs = append(
			consumer.Spec.Inputs,
			data.InputPort{
				Name:       fmt.Sprintf("input-%d", i),
				ProductRef: data.ProductReference{Name: "missing", Output: "observations"},
			},
		)
	}
	source, err := json.Marshal(consumer)
	if err != nil {
		t.Fatal(err)
	}
	report := preflight.CheckBundle(t.Context(), []io.Reader{strings.NewReader(string(source))}, "")
	if report.DiagnosticCounts.Total != 140 || report.DiagnosticCounts.Omitted != 12 {
		t.Fatalf("fixture lost retention limit: %+v", report.DiagnosticCounts)
	}
	encoded, _ := json.Marshal(report)
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(20 * time.Second).
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustEval(
		`wire=>{document.querySelector('#report-text').value=wire;document.querySelector('#report-form').requestSubmit();}`,
		string(encoded),
	)
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	page.MustElement("#severity-filter").MustSelect("Errors")
	if !strings.Contains(page.MustElement("#finding-count").MustText(), "0 of 128") ||
		!strings.Contains(page.MustElement("#finding-count").MustText(), "12 omitted") ||
		!strings.Contains(page.MustElement("#report-summary").MustText(), "140") ||
		!strings.Contains(page.MustElement("#report-summary").MustText(), "Incomplete") {
		t.Fatal("filter hid warnings or omitted findings from the outcome")
	}
}

// TestPublisherReportAdmissionAndReadRace rejects forged reports and invalidates delayed file reads.
func TestPublisherReportAdmissionAndReadRace(t *testing.T) {
	report := publisherReport(t, false, "")
	encoded, _ := json.Marshal(report)
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(20 * time.Second).
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustWait(`()=>typeof DataProductPreflightReport==='object'`)
	for _, mutation := range []string{
		"r.extra='private'", "r.products=3", "r.valid=false", "r.complete=false",
		"r.diagnosticCounts.total=1", "r.requiredFeatures=[]",
		"r.productFeatures[0].source=0", "r.productFeatures[1].document=2",
		"r.descriptors[0].ready=true", "r.descriptors[0].generation=1",
		"r.descriptors[0].health.source.state='ready'", "r.descriptors[0].outputs.push(r.descriptors[0].outputs[0])",
		"r.plan.order.reverse()", "r.plan.edges=[]", "r.plan.edges[0].output='missing'",
		"r.plan.order[0].source=1", "r.plan.edges[0].source=2",
		"r.productFeatures.forEach(entry=>entry.requiredFeatures=[]);r.requiredFeatures=[]",
	} {
		rejected := page.MustEval(`wire=>{let r=JSON.parse(wire);`+mutation+`;try{DataProductPreflightReport.parse(JSON.stringify(r));return false;}catch{return true;}}`, string(encoded)).
			Bool()
		if !rejected {
			t.Fatalf("forged report accepted: %s", mutation)
		}
	}
	for _, raw := range []string{strings.Replace(string(encoded), "{", `{"apiVersion":"data-product-preflight/v2",`, 1), string(encoded) + "{}", strings.Replace(string(encoded), `"products":2`, `"products":2.0`, 1)} {
		if !page.MustEval(`wire=>{try{DataProductPreflightReport.parse(wire);return false;}catch{return true;}}`, raw).
			Bool() {
			t.Fatal("ambiguous JSON report accepted")
		}
	}
	page.MustEval(
		`wire=>{document.querySelector('#report-text').value=wire;document.querySelector('#report-form').requestSubmit();}`,
		string(encoded),
	)
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	page.MustEval(
		`()=>{const file=new File(['{}'],'slow.json');file.arrayBuffer=()=>new Promise(resolve=>window.finishRead=resolve);const transfer=new DataTransfer();transfer.items.add(file);document.querySelector('#report-file').files=transfer.files;document.querySelector('#report-text').value='';document.querySelector('#report-form').requestSubmit();}`,
	)
	page.MustElement("#clear-report").MustClick()
	page.MustEval(
		`async wire=>{finishRead(new TextEncoder().encode(wire).buffer);await new Promise(resolve=>setTimeout(resolve,30));}`,
		string(encoded),
	)
	if page.MustEval(`()=>!document.querySelector('#review').hidden`).Bool() {
		t.Fatal("late file read restored a cleared report")
	}
	page.MustEval(
		`()=>{const file=new File(['{}'],'first.json');file.arrayBuffer=()=>new Promise(resolve=>window.finishRead=resolve);const transfer=new DataTransfer();transfer.items.add(file);document.querySelector('#report-file').files=transfer.files;document.querySelector('#report-form').requestSubmit();}`,
	)
	page.MustEval(
		`()=>{const transfer=new DataTransfer();transfer.items.add(new File(['{}'],'replacement.json'));const input=document.querySelector('#report-file');input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));}`,
	)
	page.MustEval(
		`async wire=>{finishRead(new TextEncoder().encode(wire).buffer);await new Promise(resolve=>setTimeout(resolve,30));}`,
		string(encoded),
	)
	if page.MustEval(`()=>!document.querySelector('#review').hidden || !document.querySelector('#export-preview').disabled`).
		Bool() {
		t.Fatal("late file read restored a replaced selection")
	}
	page.MustEval(
		`wire=>{document.querySelector('#report-file').value='';document.querySelector('#report-text').value=wire;document.querySelector('#report-form').requestSubmit();}`,
		string(encoded),
	)
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	page.MustElement("#product-list button").MustClick()
	page.MustEval(
		`()=>{const input=document.querySelector('#report-text');input.value='{}';input.dispatchEvent(new Event('input',{bubbles:true}));}`,
	)
	if page.MustEval(`()=>!document.querySelector('#review').hidden || !document.querySelector('#export-preview').disabled`).
		Bool() {
		t.Fatal("edited report retained earlier review/export")
	}
	page.MustEval(`()=>document.querySelector('#report-text').value=''`)
	bad := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(bad, []byte(fmt.Sprintf("{%c}", 0xff)), 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustElement("#report-file").MustSetFiles(bad)
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='rejected'`)
	if !page.MustEval(`()=>document.querySelector('#review').hidden`).Bool() {
		t.Fatal("rejected file retained report state")
	}
}

// TestPublisherVisibleUIRequirements joins mandatory visible gates to their declaration origins.
func TestPublisherVisibleUIRequirements(t *testing.T) {
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustWait(`()=>typeof DataProductPreflightReport==='object'`)
	for _, version := range []string{"data-product-ui/v1", "data-product-ui/v2"} {
		p := workspaceProduct("https://product.example.test")
		p.TypeMeta = metav1.TypeMeta{
			APIVersion: "data.devantler.tech/v1alpha1",
			Kind:       "DataProduct",
		}
		p.Spec.UI.Contract = &data.UIContract{
			APIVersion:   version,
			HostOrigins:  []data.UIHostOrigin{"https://host.example.test"},
			Capabilities: []data.UICapability{},
		}
		wire, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		report := preflight.CheckBundle(
			t.Context(),
			[]io.Reader{strings.NewReader(string(wire))},
			"",
		)
		if !report.Valid || !report.Complete {
			t.Fatalf("invalid report fixture: %+v", report.Diagnostics)
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if !page.MustEval(`wire=>{try{DataProductPreflightReport.parse(wire);return true;}catch{return false;}}`, string(encoded)).
			Bool() {
			t.Fatal("actual emitted report rejected")
		}
		required := []string{"ui-contract"}
		if version == "data-product-ui/v2" {
			required = append(required, "ui-appearance")
		}
		for _, feature := range required {
			if !page.MustEval(`(wire,feature)=>{const r=JSON.parse(wire);r.productFeatures.forEach(entry=>entry.requiredFeatures=entry.requiredFeatures.filter(f=>f!==feature));r.requiredFeatures=r.requiredFeatures.filter(f=>f!==feature);try{DataProductPreflightReport.parse(JSON.stringify(r));return false;}catch{return true;}}`, string(encoded), feature).
				Bool() {
				t.Fatalf("missing %s accepted for %s", feature, version)
			}
		}
	}
}

// TestPublisherInputSwitching makes the newest explicit selection win, including during a delayed read.
func TestPublisherInputSwitching(t *testing.T) {
	complete, err := json.Marshal(publisherReport(t, false, ""))
	if err != nil {
		t.Fatal(err)
	}
	incomplete, err := json.Marshal(publisherReport(t, true, ""))
	if err != nil {
		t.Fatal(err)
	}
	host := publisherHost(t)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(host.URL + "/publisher-review").
		MustWaitLoad()
	page.MustElement(".import-fields details summary").MustClick()
	page.MustElement("#report-text").MustInput(string(complete))
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").MustWait(`()=>this.dataset.state==='accepted'`)
	file := filepath.Join(t.TempDir(), "incomplete.json")
	if err := os.WriteFile(file, incomplete, 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustElement("#report-file").MustSetFiles(file)
	if page.MustEval(`()=>document.querySelector('#report-text').value`).Str() != "" {
		t.Error("file selection retained pasted input")
	}
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").
		MustWait(`()=>['accepted','rejected'].includes(this.dataset.state)`)
	if page.MustElement("#report-status").MustAttribute("data-state") == nil ||
		!strings.Contains(page.MustElement("#report-summary").MustText(), "Incomplete") {
		t.Error("latest file report was not inspected")
	}
	page.MustElement("#report-text").MustSelectAllText().MustInput(string(complete))
	if page.MustEval(`()=>document.querySelector('#report-file').files.length`).Int() != 0 {
		t.Error("pasted selection retained file")
	}
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").
		MustWait(`()=>['accepted','rejected'].includes(this.dataset.state)`)
	if !strings.Contains(page.MustElement("#report-summary").MustText(), "Complete") {
		t.Error("latest pasted report was not inspected")
	}
	page.MustEval(
		`()=>{const f=new File(['{}'],'slow.json');f.arrayBuffer=()=>new Promise(resolve=>window.finishRead=resolve);const transfer=new DataTransfer();transfer.items.add(f);const field=document.querySelector('#report-file');field.files=transfer.files;field.dispatchEvent(new Event('change',{bubbles:true}));document.querySelector('#report-form').requestSubmit();}`,
	)
	page.MustWait(`()=>typeof window.finishRead==='function'`)
	page.MustElement("#report-text").MustSelectAllText().MustInput(string(incomplete))
	page.MustElement("#read-report").MustClick()
	page.MustElement("#report-status").
		MustWait(`()=>['accepted','rejected'].includes(this.dataset.state)`)
	page.MustEval(
		`async wire=>{finishRead(new TextEncoder().encode(wire).buffer);await new Promise(resolve=>setTimeout(resolve,30));}`,
		string(complete),
	)
	if !strings.Contains(page.MustElement("#report-summary").MustText(), "Incomplete") ||
		!page.MustEval(`()=>document.querySelector('#export-preview').disabled`).Bool() {
		t.Error("late file read restored the obsolete complete report")
	}
}
