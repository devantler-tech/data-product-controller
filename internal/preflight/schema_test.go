package preflight

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type offlineReportSchemas struct{}

func (offlineReportSchemas) Load(string) (any, error) {
	return nil, errors.New("external schema loading is forbidden")
}

func reportSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineReportSchemas{})
	compiler.AssertFormat()
	reportData, err := os.ReadFile("schema/report-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	descriptorData, err := os.ReadFile("../registry/schema/descriptor-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	for id, data := range map[string][]byte{
		"urn:data-product-preflight:v2":  reportData,
		"urn:data-product-descriptor:v1": descriptorData,
	} {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(id, value); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile("urn:data-product-preflight:v2")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func reportValue(t *testing.T, report BundleReport) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func reportObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatal("test fixture must contain an object")
	}
	return result
}

func reportArray(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatal("test fixture must contain an array")
	}
	return result
}

func TestActualBundleReportsSatisfyOfflineSchema(t *testing.T) {
	schema := reportSchema(t)
	p, c := product("producer"), dependent("consumer", "producer")
	invalid := product("invalid")
	invalid.Spec.Owner.Name = ""
	for _, report := range []BundleReport{selected(t, bundle(t, p, c)), selected(t, bundle(t, c)), selected(t, bundle(t, invalid)), selected(t, ""), selected(t, "apiVersion: [")} {
		if err := schema.Validate(reportValue(t, report)); err != nil {
			t.Fatalf("emitted invalid v2 profile: %v", err)
		}
	}
	valid := selected(t, bundle(t, p, c))
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { delete(v, "valid") },
		func(v map[string]any) { v["diagnostics"] = nil },
		func(v map[string]any) { v["privateField"] = "sentinel" },
		func(v map[string]any) {
			reportObject(t, reportObject(t, reportArray(t, v["descriptors"])[0])["owner"])["privateField"] = "sentinel"
		},
		func(v map[string]any) { reportObject(t, reportArray(t, v["descriptors"])[0])["ready"] = true },
		func(v map[string]any) { v["complete"] = false },
		func(v map[string]any) { v["plan"] = nil },
	} {
		v := reportValue(t, valid)
		mutate(v)
		if err := schema.Validate(v); err == nil {
			t.Fatal("schema accepted unsupported or inconsistent profile")
		}
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineReportSchemas{})
	if err := compiler.AddResource(
		"urn:test",
		map[string]any{"$ref": "https://example.test/private-schema"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile("urn:test"); err == nil {
		t.Fatal("schema compiler fetched an unresolved external reference")
	}
}
