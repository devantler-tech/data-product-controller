package dataspace_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

// example uses the runnable operator examples as shared consumer fixtures.
func example(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("../../docs/examples/dsp-catalog"), name+".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// object decodes an asserted fixture object for semantic mutation or comparison.
func object(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// encode serializes test data without swallowing fixture errors.
func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestExportPreservesProviderIntent compares every emitted field and policy rule.
func TestExportPreservesProviderIntent(t *testing.T) {
	t.Parallel()
	b, err := dataspace.Export(
		bytes.NewReader(example(t, "catalog")),
		bytes.NewReader(example(t, "bindings")),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := object(t, b)
	want := object(
		t,
		[]byte(
			`{"@context":["https://w3id.org/dspace/2025/1/context.jsonld"],"@id":"urn:example:dsp-catalog","@type":"Catalog","participantId":"urn:example:provider","service":[{"@id":"urn:example:dsp-service","@type":"DataService","endpointURL":"https://connector.example/dsp"}],"dataset":[{"@id":"urn:example:harbour","@type":"Dataset","dct:title":"Harbour observations","dct:description":"Public temperature observations.","dcat:version":"v1.0.0","hasPolicy":[{"@id":"urn:example:harbour-offer","@type":"Offer","assigner":"urn:example:provider","permission":[{"action":"use","constraint":[{"leftOperand":"http://www.w3.org/ns/odrl/2/purpose","operator":"eq","rightOperand":"research"}]}],"prohibition":[{"action":"http://www.w3.org/ns/odrl/2/sell"}]}],"distribution":[{"@id":"urn:example:dsp-observations","@type":"Distribution","format":"https://example.com/transfer/harbour-pull","accessService":{"@id":"urn:example:dsp-service","@type":"DataService","endpointURL":"https://connector.example/dsp"}}]}]}`,
		),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export changed public metadata or provider claims:\n%s", b)
	}
}

// TestExportRejectsAmbiguousOrIncompleteBindings checks stale selections and unsupported claims.
func TestExportRejectsAmbiguousOrIncompleteBindings(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, old, replacement string }{
		{"stale dataset", `"id": "urn:example:harbour"`, `"id": "urn:example:missing"`},
		{
			"stale output",
			`"sourceId": "urn:example:observations"`,
			`"sourceId": "urn:example:missing"`,
		},
		{"implicit provider", `"participantId": "urn:example:provider",`, ``},
		{"wrong assigner", `"assigner": "urn:example:provider"`, `"assigner": "urn:example:other"`},
		{"missing assigner", `"assigner": "urn:example:provider",`, ``},
		{
			"query as base",
			`https://connector.example/dsp`,
			`https://connector.example/dsp?token=secret`,
		},
		{
			"credential URL",
			`https://connector.example/dsp`,
			`https://user:secret@connector.example/dsp`,
		},
		{"plain HTTP", `https://connector.example/dsp`, `http://connector.example/dsp`},
		{"invented format", `https://example.com/transfer/harbour-pull`, `application/json`},
		{"collision", `urn:example:dsp-observations`, `urn:example:harbour`},
		{"source identity reused", `urn:example:dsp-observations`, `urn:example:observations`},
		{"policy target", `"@type": "Offer",`, `"@type": "Offer", "target":"urn:example:harbour",`},
		{"hidden condition", `"action": "use",`, `"action": "use", "duty":[{"action":"use"}],`},
		{
			"nested context",
			`"@type": "Offer",`,
			`"@type": "Offer", "@context":{"assigner":"https://evil.example/"},`,
		},
		{
			"duplicate keys",
			`"action": "use",`,
			`"action": "use", "action":"http://www.w3.org/ns/odrl/2/sell",`,
		},
		{"unknown action", `"action": "use"`, `"action": "sell"`},
		{"unknown version", `dsp-catalog/v1`, `dsp-catalog/v2`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := example(t, "bindings")
			changed := bytes.ReplaceAll(source, []byte(tt.old), []byte(tt.replacement))
			if bytes.Equal(source, changed) {
				t.Fatal("fixture did not change")
			}
			b, err := dataspace.Export(
				bytes.NewReader(example(t, "catalog")),
				bytes.NewReader(changed),
			)
			if err == nil || len(b) != 0 {
				t.Fatalf("unsafe export succeeded: %s, %v", b, err)
			}
		})
	}
}

// TestEmptySelectionKeepsService preserves the required service without an invalid empty dataset array.
func TestEmptySelectionKeepsService(t *testing.T) {
	t.Parallel()
	bindings := object(t, example(t, "bindings"))
	bindings["datasets"] = []any{}
	b, err := dataspace.Export(
		bytes.NewReader(example(t, "catalog")),
		bytes.NewReader(encode(t, bindings)),
	)
	if err != nil {
		t.Fatal(err)
	}
	doc := object(t, b)
	if _, ok := doc["dataset"]; ok {
		t.Fatal("empty datasets must be omitted")
	}
	if doc["service"] == nil || strings.Contains(string(b), "hasPolicy") {
		t.Fatalf("invalid empty catalog: %s", b)
	}
}
