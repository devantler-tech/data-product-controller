//go:build browser

package browser_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/web"
)

// TestOfflineDescriptorHandoff catches replacing the descriptor handoff with a URL fetch or bypassing UI grants.
func TestOfflineDescriptorHandoff(t *testing.T) {
	var navigations, otherRequests atomic.Int32
	var appearanceEnabled atomic.Bool
	appearanceEnabled.Store(true)
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui" {
			otherRequests.Add(1)
			http.Error(w, "metadata must stay offline", http.StatusForbidden)
			return
		}
		navigations.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Independent product</title><script>
		addEventListener('message', e => {
		if(e.source!==parent) return;
		if(e.data.type==='appearance') {window.appearance=e.data.appearance; return;}
		if(e.data.type!=='init') return;
		window.init=e.data; window.hostOrigin=e.origin;
		parent.postMessage({apiVersion:init.apiVersion,type:'ready',session:init.session},e.origin);
		});</script>`))
	}))
	t.Cleanup(product.Close)
	kit := httptest.NewTLSServer(web.KitHandlerWithOptions(web.KitOptions{
		ContractEnabled:   func() bool { return true },
		AppearanceEnabled: appearanceEnabled.Load,
		DiscoveryEnabled:  func() bool { return true },
	}))
	t.Cleanup(kit.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(15 * time.Second).
		MustNavigate(kit.URL).
		MustWaitLoad()
	if !page.MustEval(`() => !!document.querySelector('#descriptor-form') && !document.querySelector('#descriptor-import').hidden`).
		Bool() {
		t.Fatal("enabled kit cannot accept a complete offline descriptor")
	}
	descriptor := offlineDescriptor(kit.URL, product.URL)
	data := descriptorJSON(t, descriptor)
	file := filepath.Join(t.TempDir(), "public-descriptor.json")
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustElement("#grant-status").MustClick()
	page.MustElement("#descriptor-file").MustSetFiles(file)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	if !page.MustEval(`() => !document.querySelector('#descriptor-snapshot').hidden && /snapshot/i.test(document.querySelector('#descriptor-snapshot').textContent) && /authorization/i.test(document.querySelector('#descriptor-snapshot').textContent)`).
		Bool() {
		t.Fatal("imported readiness was presented without its snapshot/authorization reminder")
	}
	frame := page.MustElement("#product-surface").MustFrame()
	if got := frame.MustEval(`() => ({keys:Object.keys(init).sort(),capabilities:init.capabilities})`).
		JSON("", ""); got != `{"capabilities":["status"],"keys":["apiVersion","capabilities","session","type"]}` {
		t.Fatalf("offline handoff crossed the public protocol/grant boundary: %s", got)
	}
	if got := page.MustEval(`() => document.querySelector('#product-surface').getAttribute('sandbox')`).
		Str(); got != "allow-forms allow-scripts" {
		t.Fatal("offline handoff broadened the opaque iframe sandbox")
	}
	if navigations.Load() != 1 || otherRequests.Load() != 0 {
		t.Fatalf(
			"offline import fetched metadata or product records: UI=%d other=%d",
			navigations.Load(),
			otherRequests.Load(),
		)
	}
	// Declared metadata stays offline, including legitimate local DNS and IP destinations.
	for _, metadataURL := range []string{
		"https://localhost/query",
		"https://localhost/path%20ok?query=%25",
		"https://publisher.example:443/query",
		"https://127.0.0.1:8443/query",
		"https://[::1]:8443/query",
	} {
		var local map[string]any
		if err := json.Unmarshal([]byte(data), &local); err != nil {
			t.Fatal(err)
		}
		descriptorEntry(t, local["outputs"])["url"] = metadataURL
		page.MustEval(
			`(text) => { document.querySelector('#descriptor-file').value=''; document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
			descriptorJSON(t, local),
		)
		page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
		if otherRequests.Load() != 0 {
			t.Fatal("local metadata URL was fetched during descriptor import")
		}
	}
	// A complete descriptor retains optional composition metadata and independently gated v2 presentation.
	var expanded map[string]any
	if err := json.Unmarshal([]byte(data), &expanded); err != nil {
		t.Fatal(err)
	}
	expanded["name"] = strings.Repeat("a", 63) + ".harbour"
	descriptorObject(t, expanded, "health", "source")["state"] = "unobserved"
	descriptorObject(t, expanded, "health", "source")["observedGeneration"] = 0
	descriptorObject(t, expanded, "health", "connector")["state"] = "disabled"
	descriptorObject(t, expanded, "health", "connector")["observedGeneration"] = 0
	expanded["documentationUrl"] = product.URL + "/documentation"
	expanded["composition"] = map[string]any{
		"reason":  "ready",
		"message": "Public composition observation",
	}
	expanded["inputs"] = []any{
		map[string]any{
			"name": "external",
			"productRef": map[string]any{
				"name":      "producer",
				"namespace": "products",
				"output":    "observations",
			},
			"contract": map[string]any{"minimumVersion": "v1.0.0", "protocol": "OpenAPI"},
		},
	}
	expanded["lineage"] = []any{
		map[string]any{
			"name": "external",
			"productRef": map[string]any{
				"name":      "producer",
				"namespace": "products",
				"output":    "observations",
			},
			"ready":              true,
			"reason":             "InputReady",
			"productID":          "urn:example:producer",
			"observedGeneration": 7,
			"version":            "v1.0.0",
			"owner":              expanded["owner"],
			"output":             descriptorEntry(t, expanded["outputs"]),
		},
	}
	longName := strings.Repeat("a", 63) + ".producer"
	descriptorObject(t, descriptorEntry(t, expanded["inputs"]), "productRef")["name"] = longName
	descriptorObject(t, descriptorEntry(t, expanded["lineage"]), "productRef")["name"] = longName
	descriptorObject(t, expanded, "ui", "contract")["apiVersion"] = "data-product-ui/v2"
	descriptorObject(t, expanded, "ui", "contract")["capabilities"] = []string{
		"status",
		"resize",
		"appearance",
	}
	v2 := descriptorJSON(t, expanded)
	page.MustElement("#grant-appearance").MustClick()
	page.MustElement("#appearance").MustSelect("Dark")
	page.MustEval(
		`(text) => { document.querySelector('#descriptor-file').value=''; document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
		v2,
	)
	if page.MustEval(`() => document.querySelector('#kit-status').dataset.state === 'invalid'`).
		Bool() {
		t.Fatal("valid DNS-subdomain product reference was rejected")
	}
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	frame = page.MustElement("#product-surface").MustFrame()
	frame.MustWait(`() => window.appearance === 'dark'`)
	if got := frame.MustEval(`() => init.capabilities`).
		JSON("", ""); got != `["status","appearance"]` {
		t.Fatalf("v2 descriptor bypassed host grant intersection: %s", got)
	}
	appearanceEnabled.Store(false)
	// Release gates are sampled by the host document; a newly opened host must refuse v2 when off.
	page.MustNavigate(kit.URL).MustWaitLoad()
	beforeV2 := navigations.Load()
	page.MustEval(
		`(text) => { document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
		v2,
	)
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() ||
		navigations.Load() != beforeV2 {
		t.Fatal("v2 descriptor navigated with the appearance release gate disabled")
	}
	// Every refusal starts with a live session so missing revocation cannot pass.
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		raw    string
	}{
		{name: "not ready", mutate: func(d map[string]any) { d["ready"] = false }},
		{name: "nonboolean readiness", mutate: func(d map[string]any) { d["ready"] = "true" }},
		{name: "unsupported descriptor version", mutate: func(d map[string]any) { d["apiVersion"] = "data-product-descriptor/v99" }},
		{name: "wrong kind", mutate: func(d map[string]any) { d["kind"] = "Secret" }},
		{name: "missing identity", mutate: func(d map[string]any) { delete(d, "id") }},
		{name: "empty DNS label", mutate: func(d map[string]any) { d["name"] = "a..b" }},
		{name: "invalid DNS label", mutate: func(d map[string]any) { d["name"] = "a.-b" }},
		{name: "empty identity", mutate: func(d map[string]any) { d["id"] = "" }},
		{name: "wrong owner shape", mutate: func(d map[string]any) { d["owner"] = []string{"Marine team"} }},
		{name: "missing UI contract", mutate: func(d map[string]any) { delete(descriptorObject(t, d, "ui"), "contract") }},
		{name: "malformed UI", mutate: func(d map[string]any) { d["ui"] = nil }},
		{name: "no outputs", mutate: func(d map[string]any) { d["outputs"] = []any{} }},
		{name: "unknown health dimension", mutate: func(d map[string]any) { descriptorObject(t, d, "health")["records"] = "private" }},
		{name: "inconsistent ready reason", mutate: func(d map[string]any) { descriptorObject(t, d, "readiness")["reason"] = "not-ready" }},
		{name: "stale generation", mutate: func(d map[string]any) { d["observedGeneration"] = float64(6) }},
		{name: "foreign health generation", mutate: func(d map[string]any) {
			descriptorObject(t, d, "health", "source")["generation"] = 8
		}},
		{name: "stale ready health", mutate: func(d map[string]any) {
			descriptorObject(t, d, "health", "source")["observedGeneration"] = 6
		}},
		{name: "malformed health", mutate: func(d map[string]any) { descriptorObject(t, d, "health", "source")["state"] = true }},
		{name: "oversized field", mutate: func(d map[string]any) { d["description"] = strings.Repeat("x", 16385) }},
		{name: "oversized UTF8 field", mutate: func(d map[string]any) { d["description"] = strings.Repeat("é", 8193) }},
		{name: "oversized array", mutate: func(d map[string]any) { d["outputs"] = make([]any, 1025) }},
		{name: "foreign host", mutate: func(d map[string]any) {
			descriptorObject(t, d, "ui", "contract")["hostOrigins"] = []string{"https://publisher.example"}
		}},
		{name: "credential URL", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://user:secret@publisher.example/query"
		}},
		{name: "nonliteral HTTPS scheme", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https:publisher.example/query"
		}},
		{name: "empty HTTPS authority", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https:///publisher.example/query"
		}},
		{name: "uppercase HTTPS scheme", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "HTTPS://publisher.example/query"
		}},
		{name: "malformed metadata host", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://bad_host.example/query"
		}},
		{name: "empty metadata DNS label", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://a..b/query"
		}},
		{name: "unicode metadata URL", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://publisher.example/café"
		}},
		{name: "zero metadata port", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://publisher.example:0/query"
		}},
		{name: "empty metadata port", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://publisher.example:/query"
		}},
		{name: "invalid metadata URL escape", mutate: func(d map[string]any) {
			descriptorEntry(t, d["outputs"])["url"] = "https://publisher.example/%zz"
		}},
		{name: "nonliteral UI HTTPS scheme", mutate: func(d map[string]any) {
			descriptorObject(t, d, "ui")["url"] = "https:" + strings.TrimPrefix(product.URL, "https://") + "/ui"
		}},
		{name: "unknown private field", mutate: func(d map[string]any) { d["credentials"] = "must not import" }},
		{name: "malformed JSON", raw: "{"},
		{name: "oversized JSON", raw: strings.Repeat(" ", 65537)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page.MustEval(
				`(text) => { document.querySelector('#descriptor-file').value=''; document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
				data,
			)
			page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
			before := navigations.Load()
			invalid := tc.raw
			if tc.mutate != nil {
				var changed map[string]any
				if err := json.Unmarshal([]byte(data), &changed); err != nil {
					t.Fatal(err)
				}
				tc.mutate(changed)
				invalid = descriptorJSON(t, changed)
			}
			page.MustEval(
				`(text) => { document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
				invalid,
			)
			if page.MustEval(`() => document.querySelector('#kit-status').dataset.state !== 'invalid' || document.querySelector('#product-surface').hasAttribute('src') || !document.querySelector('#product-surface').hidden`).
				Bool() ||
				navigations.Load() != before {
				t.Fatal("invalid descriptor was accepted, retained or navigated a product session")
			}
		})
	}
	// Local file input is also bounded before allocating its contents.
	page.MustEval(
		`(text) => { document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
		data,
	)
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	before := navigations.Load()
	oversized := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversized, []byte(strings.Repeat(" ", 65537)), 0o600); err != nil {
		t.Fatal(err)
	}
	page.MustEval(`() => {document.querySelector('#descriptor').value='';}`)
	page.MustElement("#descriptor-file").MustSetFiles(oversized)
	page.MustElement("#import-descriptor").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() ||
		navigations.Load() != before {
		t.Fatal("oversized local file retained or navigated a product session")
	}
	// A slow file read is the asynchronous boundary; a later Close must win.
	page.MustEval(
		`(text) => { document.querySelector('#descriptor-file').value=''; document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
		data,
	)
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	before = navigations.Load()
	page.MustEval(`() => {
		const file=new File(['{}'],'slow.json',{type:'application/json'});
		file.text=()=>new Promise(resolve=>{window.finishDescriptorRead=resolve;});
		const transfer=new DataTransfer(); transfer.items.add(file);
		document.querySelector('#descriptor-file').files=transfer.files;
		document.querySelector('#descriptor').value='';
		document.querySelector('#descriptor-form').requestSubmit();
	}`)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src') || document.querySelector('#kit-status').dataset.state !== 'importing'`).
		Bool() {
		t.Fatal("pending local file read kept the previous session or its readiness status")
	}
	page.MustElement("#close").MustClick()
	page.MustEval(
		`async (text) => { finishDescriptorRead(text); await new Promise(resolve=>setTimeout(resolve,50)); }`,
		data,
	)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src') || document.querySelector('#kit-status').dataset.state !== 'closed'`).
		Bool() ||
		navigations.Load() != before {
		t.Fatal("completed file read revived a closed product session")
	}
	if otherRequests.Load() != 0 {
		t.Fatal("descriptor import fetched a contract, owner URL or product records")
	}
}

// TestOfflineDescriptorGatePreservesManifest catches an invisible form bypass and a regression in legacy manifest import.
func TestOfflineDescriptorGatePreservesManifest(t *testing.T) {
	kit := httptest.NewTLSServer(web.KitHandler(func() bool { return true }))
	t.Cleanup(kit.Close)
	var navigations atomic.Int32
	product := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		navigations.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(
			[]byte(
				`<!doctype html><script>addEventListener('message',e=>{if(e.source===parent&&e.data.type==='init')parent.postMessage({apiVersion:e.data.apiVersion,type:'ready',session:e.data.session},e.origin);});</script>`,
			),
		)
	}))
	t.Cleanup(product.Close)
	page := contractBrowser(
		t,
	).MustPage().
		Timeout(15 * time.Second).
		MustNavigate(kit.URL).
		MustWaitLoad()
	if !page.MustEval(`() => document.querySelector('#descriptor-import').hidden`).Bool() {
		t.Fatal("legacy kit enabled descriptor discovery without operator opt-in")
	}
	descriptor := offlineDescriptor(kit.URL, product.URL)
	page.MustElement("#manifest").MustInput(descriptorJSON(t, descriptor["ui"]))
	page.MustElement("#validate").MustClick()
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'ready'`)
	if navigations.Load() != 1 {
		t.Fatal("legacy manifest import failed while descriptor discovery is off")
	}
	page.MustEval(
		`(text) => { document.querySelector('#descriptor').value=text; document.querySelector('#descriptor-form').requestSubmit(); }`,
		descriptorJSON(t, descriptor),
	)
	page.MustElement("#kit-status").MustWait(`() => this.dataset.state === 'invalid'`)
	if page.MustEval(`() => document.querySelector('#product-surface').hasAttribute('src')`).
		Bool() ||
		navigations.Load() != 1 {
		t.Fatal("disabled descriptor import retained or navigated the old product")
	}
}

// offlineDescriptor is a literal exported v1 document, independent of registry builders.
func offlineDescriptor(host, publisher string) map[string]any {
	health := map[string]any{}
	for _, dimension := range []string{"source", "connector", "contracts", "composition"} {
		health[dimension] = map[string]any{
			"state":              "ready",
			"message":            "Current observation",
			"generation":         7,
			"observedGeneration": 7,
		}
	}
	return map[string]any{
		"apiVersion":  "data-product-descriptor/v1",
		"kind":        "DataProduct",
		"namespace":   "products",
		"name":        "harbour",
		"id":          "urn:example:harbour",
		"displayName": "Harbour observations",
		"description": "Public observations",
		"version":     "v1.0.0",
		"owner":       map[string]any{"name": "Marine team", "url": publisher + "/owner"},
		"outputs": []any{
			map[string]any{
				"name":        "observations",
				"protocol":    "OpenAPI",
				"url":         publisher + "/query",
				"contractUrl": publisher + "/schema",
			},
		},
		"ui": map[string]any{
			"url":   publisher + "/ui",
			"title": "Harbour observations",
			"contract": map[string]any{
				"apiVersion":   "data-product-ui/v1",
				"hostOrigins":  []string{host},
				"capabilities": []string{"status", "resize"},
			},
		},
		"ready": true,
		"readiness": map[string]any{
			"reason":  "ready",
			"message": "Current metadata observation",
		},
		"generation":         7,
		"observedGeneration": 7,
		"health":             health,
	}
}

func descriptorJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// descriptorObject checks the public fixture path before applying an independent mutation.
func descriptorObject(t *testing.T, value any, keys ...string) map[string]any {
	t.Helper()
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatal("descriptor fixture path is not an object")
		}
		value = object[key]
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatal("descriptor fixture value is not an object")
	}
	return object
}

func descriptorEntry(t *testing.T, value any) map[string]any {
	t.Helper()
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		t.Fatal("descriptor fixture array is empty or malformed")
	}
	return descriptorObject(t, values[0])
}
