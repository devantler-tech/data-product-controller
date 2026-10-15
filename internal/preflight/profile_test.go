package preflight

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
)

// apiBundle evaluates fresh explicit sources so comparisons never reuse consumed readers.
func apiBundle(t *testing.T, sources ...string) BundleReport {
	t.Helper()
	readers := make([]io.Reader, 0, len(sources))
	for _, source := range sources {
		readers = append(readers, strings.NewReader(source))
	}
	return CheckAPIOnlyBundle(t.Context(), readers, "")
}

// TestAPIOnlyRetainsDeliveredValidation preserves admission, graph, parsing and shared-limit behavior.
func TestAPIOnlyRetainsDeliveredValidation(t *testing.T) {
	p, c := product("producer"), dependent("consumer", "producer")
	invalid := product("invalid")
	invalid.Spec.Owner.Name = ""
	a, b := dependent("a", "b"), dependent("b", "a")
	for _, sources := range [][]string{
		{bundle(t, p), "null\n---\n" + bundle(t, c)},
		{bundle(t, c)},
		{bundle(t, invalid)},
		{bundle(t, a, b)},
		{bundle(t, p), bundle(t, p)},
		{strings.Replace(bundle(t, p), `"spec":{`, `"spec":{"PRIVATE_FIELD":"PRIVATE_VALUE",`, 1)},
		{strings.Replace(bundle(t, p), `"kind":"DataProduct"`, `"kind":"DataProduct","kind":"DataProduct"`, 1)},
		{strings.Repeat(" ", maxInputBytes+1)},
	} {
		actual, compatibility := apiBundle(t, sources...), selected(t, sources...)
		if !reflect.DeepEqual(actual, compatibility) {
			t.Fatalf(
				"API-only selection changed a data-only result: %+v vs %+v",
				actual,
				compatibility,
			)
		}
		if err := reportSchema(t).Validate(reportValue(t, actual)); err != nil {
			t.Fatalf("API-only report left the existing offline contract: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := CheckAPIOnlyFiles(ctx, []io.Reader{strings.NewReader(bundle(t, p))}, "")
	requireCode(t, r, "ValidationLimit")
	if r.Valid || r.Complete || len(r.Descriptors) != 0 || len(r.documents) != 0 {
		t.Fatal("canceled API-only check retained declarations or previews")
	}
}

// TestAPIOnlyRejectsLegacyUIWithoutLeakingMetadata checks real v1/v2 declarations, provenance and whole-bundle withdrawal.
func TestAPIOnlyRejectsLegacyUIWithoutLeakingMetadata(t *testing.T) {
	for _, version := range []string{"", "data-product-ui/v1", "data-product-ui/v2"} {
		p := product("with-ui")
		p.Spec.UI = &data.ProductUI{
			URL:   "https://ui.example.test/PRIVATE_PATH",
			Title: "PRIVATE_TITLE",
		}
		if version != "" {
			p.Spec.UI.Contract = &data.UIContract{
				APIVersion: version, HostOrigins: []data.UIHostOrigin{"https://host.example.test"},
				Capabilities: []data.UICapability{"status", "resize"},
			}
		}
		selection := "null\n---\n" + bundle(t, p)
		legacy := selected(t, selection)
		if !legacy.Valid || !legacy.Complete || len(legacy.Descriptors) != 1 {
			t.Fatalf("compatibility control failed for %s: %+v", version, legacy)
		}
		r := apiBundle(t, bundle(t, product("safe")), selection)
		d := richCode(t, r, "InvalidUI")
		if r.Valid || r.Complete || len(r.Descriptors) != 0 || r.Plan != nil ||
			d.Source != 2 || d.Document != 2 || d.Path != "/spec/ui" || d.Line < 3 || d.Column == 0 ||
			len(r.RequiredFeatures) != 0 || len(r.ProductFeatures) != 1 {
			t.Fatalf("UI selection leaked a preview, requirements or location: %+v", r)
		}
		encoded, err := json.Marshal(r)
		if err != nil || strings.Contains(string(encoded), "PRIVATE_") ||
			!strings.Contains(d.Message, "Remove spec.ui") {
			t.Fatal("UI rejection disclosed submitted metadata or omitted its remedy")
		}
		if err := reportSchema(t).Validate(reportValue(t, r)); err != nil {
			t.Fatalf("UI rejection requires a new report reader: %v", err)
		}
	}
}

// TestAPIOnlyRejectsSchemaInvalidUI preserves both admission findings and the profile remedy.
func TestAPIOnlyRejectsSchemaInvalidUI(t *testing.T) {
	for _, url := range []string{"", "http://ui.example.test/PRIVATE_PATH", "PRIVATE_INVALID_URL"} {
		p := product("invalid-ui")
		p.Spec.UI = &data.ProductUI{URL: url, Title: "PRIVATE_TITLE"}
		selection := bundle(t, p)
		legacy := selected(t, selection)
		richCode(t, legacy, "InvalidField")
		r := apiBundle(t, selection)
		d := richCode(t, r, "InvalidUI")
		richCode(t, r, "InvalidField")
		if r.Valid || r.Complete || len(r.Descriptors) != 0 || r.Plan != nil ||
			d.Path != "/spec/ui" || !strings.Contains(d.Message, "Remove spec.ui") {
			t.Fatalf("schema-invalid UI lost admission or profile rejection: %+v", r)
		}
		encoded, err := json.Marshal(r)
		if err != nil || strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatal("schema-invalid UI disclosed submitted metadata")
		}
		if err := reportSchema(t).Validate(reportValue(t, r)); err != nil {
			t.Fatalf("schema-invalid UI report left the existing contract: %v", err)
		}
	}
}

// TestAPIOnlyRejectsStructurallyInvalidUI retains parse failures and the API-only remedy.
func TestAPIOnlyRejectsStructurallyInvalidUI(t *testing.T) {
	for _, value := range []string{`"PRIVATE_UI"`, `17`, `[]`, `{"url":17}`, `{"url":"https://ui.example.test","PRIVATE_FIELD":true}`} {
		selection := strings.Replace(
			bundle(t, product("invalid-ui")),
			`"spec":{`,
			`"spec":{"ui":`+value+`,`,
			1,
		)
		legacy := selected(t, selection)
		if legacy.Valid || legacy.Complete || len(legacy.Diagnostics) != 1 {
			t.Fatalf("compatibility control did not reject malformed UI: %+v", legacy)
		}
		r := apiBundle(t, selection)
		d := richCode(t, r, "InvalidUI")
		if r.Valid || r.Complete || len(r.Descriptors) != 0 || r.Plan != nil ||
			d.Path != "/spec/ui" || !strings.Contains(d.Message, "Remove spec.ui") ||
			len(r.Diagnostics) != 2 || r.Diagnostics[1].Code != legacy.Diagnostics[0].Code {
			t.Fatalf("structural UI failure lost the profile remedy or original rejection: %+v", r)
		}
		encoded, err := json.Marshal(r)
		if err != nil || strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatal("structural UI failure disclosed submitted metadata")
		}
		if err := reportSchema(t).Validate(reportValue(t, r)); err != nil {
			t.Fatalf("structural UI failure left the existing report contract: %v", err)
		}
	}
}
