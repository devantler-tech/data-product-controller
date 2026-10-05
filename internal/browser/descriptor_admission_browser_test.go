//go:build browser

package browser_test

import (
	"strings"
	"testing"
)

func TestDescriptorDependencyIdentityAdmission(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	for _, tc := range []struct {
		name, declaredNS, observedNS, producer, output, observedOutput string
		valid                                                          bool
	}{
		{"implicit declaration", "", "products", "producer", "observations", "observations", true},
		{"implicit observation", "products", "", "producer", "observations", "observations", true},
		{"both implicit", "", "", "producer", "observations", "observations", true},
		{"same foreign namespace", "other", "other", "producer", "observations", "observations", true},
		{"different namespace", "", "other", "producer", "observations", "observations", false},
		{"different producer", "", "", "replaced", "observations", "observations", false},
		{"different port", "", "", "producer", "other", "other", false},
		{"contradictory observed port", "", "", "producer", "observations", "other", false},
		{"orphan observation", "", "", "producer", "observations", "observations", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor := offlineDescriptor(host, publisher)
			declared := map[string]any{"name": "producer", "output": "observations"}
			observed := map[string]any{"name": tc.producer, "output": tc.output}
			if tc.declaredNS != "" {
				declared["namespace"] = tc.declaredNS
			}
			if tc.observedNS != "" {
				observed["namespace"] = tc.observedNS
			}
			descriptor["inputs"] = []any{map[string]any{"name": "upstream", "productRef": declared}}
			lineageName := "upstream"
			if tc.name == "orphan observation" {
				lineageName = "unknown"
			}
			port := descriptorEntry(t, descriptor["outputs"])
			// Use a distinct map so the observed port mutation cannot alter the root output.
			observedPort := make(map[string]any)
			for key, value := range port {
				observedPort[key] = value
			}
			observedPort["name"] = tc.observedOutput
			ready, reason := true, "InputReady"
			if tc.name == "same foreign namespace" {
				ready, reason = false, "InputNotReady"
			}
			descriptor["lineage"] = []any{map[string]any{
				"name": lineageName, "productRef": observed, "ready": ready, "reason": reason,
				"observedGeneration": 2, "output": observedPort,
			}}
			before := calls.Load()
			page.MustElement("#descriptor").
				MustSelectAllText().
				MustInput(descriptorJSON(t, descriptor))
			page.MustElement("#import-descriptor").MustClick()
			page.MustElement("#kit-status").
				MustWait(`()=>['ready','invalid'].includes(this.dataset.state)`)
			accepted := page.MustEval(`()=>document.querySelector('#kit-status').dataset.state==='ready'`).
				Bool()
			if accepted != tc.valid {
				t.Fatalf("dependency snapshot admitted=%t want=%t", accepted, tc.valid)
			}
			if !tc.valid && calls.Load() != before {
				t.Fatal("contradictory dependency snapshot navigated a product")
			}
		})
	}
}

func TestDescriptorLineageReadinessAdmission(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	for _, tc := range []struct {
		ready  bool
		reason string
		valid  bool
	}{
		{true, "InputReady", true},
		{false, "InputNotReady", true},
		{false, "InputReady", false},
		{true, "InputNotReady", false},
	} {
		t.Run(
			tc.reason+"/"+map[bool]string{true: "ready", false: "unready"}[tc.ready],
			func(t *testing.T) {
				descriptor := offlineDescriptor(host, publisher)
				ref := map[string]any{"name": "producer", "output": "observations"}
				descriptor["inputs"] = []any{map[string]any{"name": "upstream", "productRef": ref}}
				descriptor["lineage"] = []any{
					map[string]any{
						"name":       "upstream",
						"productRef": ref,
						"ready":      tc.ready,
						"reason":     tc.reason,
					},
				}
				before := calls.Load()
				page.MustElement("#descriptor").
					MustSelectAllText().
					MustInput(descriptorJSON(t, descriptor))
				page.MustElement("#import-descriptor").MustClick()
				page.MustElement("#kit-status").
					MustWait(`()=>['ready','invalid'].includes(this.dataset.state)`)
				accepted := page.MustEval(`()=>document.querySelector('#kit-status').dataset.state==='ready'`).
					Bool()
				if accepted != tc.valid {
					t.Fatalf("readiness admission=%t want=%t", accepted, tc.valid)
				}
				if !tc.valid && calls.Load() != before {
					t.Fatal("contradictory readiness navigated a product")
				}
			},
		)
	}
}

func TestDescriptorUnicodeAdmission(t *testing.T) {
	page, host, publisher, calls := kitWorkspaceFixture(t, false, false)
	for _, tc := range []struct {
		wire  string
		valid bool
	}{
		{`"København 🌊"`, true},
		{`"Label �"`, true},
		{`"Label \ufffd"`, true},
		{`"Wave \ud83c\udf0a"`, true},
		{`"Literal \\ud800"`, true},
		{`"Label \ud800"`, false},
		{`"Label \udc00"`, false},
	} {
		t.Run(tc.wire, func(t *testing.T) {
			descriptor := offlineDescriptor(host, publisher)
			descriptor["description"] = "Unicode sentinel"
			original := descriptorJSON(t, descriptor)
			changed := strings.Replace(original, `"Unicode sentinel"`, tc.wire, 1)
			if changed == original {
				t.Fatal("Unicode import fixture was not substituted")
			}
			before := calls.Load()
			page.MustElement("#descriptor").MustSelectAllText().MustInput(changed)
			page.MustElement("#import-descriptor").MustClick()
			page.MustElement("#kit-status").
				MustWait(`()=>['ready','invalid'].includes(this.dataset.state)`)
			accepted := page.MustEval(`()=>document.querySelector('#kit-status').dataset.state==='ready'`).
				Bool()
			if accepted != tc.valid {
				t.Fatalf("Unicode descriptor admitted=%t want=%t", accepted, tc.valid)
			}
			if !tc.valid && calls.Load() != before {
				t.Fatal("malformed Unicode navigated a product")
			}
		})
	}
	for _, wire := range []string{`{"\ud800":"value"}`, `{"key":"\udc00"}`} {
		if page.MustEval(`wire=>{try{DataProductDescriptor.parseJSON(wire);return true}catch{return false}}`, wire).
			Bool() {
			t.Errorf("raw Unicode admitted: %s", wire)
		}
	}
}

func TestDiscoveryWithdrawsPreviousCompleteness(t *testing.T) {
	server, _, catalogStatus := discoveryFixture(t)
	page := contractBrowser(t).MustPage().MustNavigate(server.URL).MustWaitLoad()
	for _, scope := range []string{"refresh", "namespace"} {
		t.Run(scope, func(t *testing.T) {
			catalogStatus.Store(0)
			page.MustElement("#refresh-products").MustClick()
			page.MustElement("#load-more").MustWaitVisible().MustClick()
			page.MustElement("#discovery-scope").
				MustWait(`()=>this.textContent.includes('All products')`)
			catalogStatus.Store(503)
			if scope == "refresh" {
				page.MustElement("#refresh-products").MustClick()
			} else {
				page.MustElement("#namespace-filter").MustInput("other")
				page.MustElement("#catalog-scope button").MustClick()
			}
			page.MustElement("#registry-status").
				MustWait(`()=>this.textContent.includes('Could not load products')`)
			if page.MustEval(`()=>document.querySelectorAll('.product-card').length`).Int() != 0 {
				t.Fatal("failed scope retained old inventory")
			}
			if page.MustElement("#discovery-scope").MustVisible() &&
				strings.Contains(page.MustElement("#discovery-scope").MustText(), "All products") {
				t.Fatal("failed scope retained a completeness claim")
			}
		})
	}
}
