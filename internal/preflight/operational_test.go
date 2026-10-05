package preflight

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
)

// TestV2RequiredGatesMatchHostAdmission keeps protocol requirements independent of optional hints.
func TestV2RequiredGatesMatchHostAdmission(t *testing.T) {
	for _, version := range []string{"data-product-ui/v1", "data-product-ui/v2"} {
		for _, caps := range [][]data.UICapability{{}, {"status"}, {"appearance"}} {
			p := product("ui")
			p.Spec.UI = &data.ProductUI{
				URL:   "https://example.com/ui",
				Title: "Explore observations",
				Contract: &data.UIContract{
					APIVersion:   version,
					HostOrigins:  []data.UIHostOrigin{"https://registry.example.com"},
					Capabilities: caps,
				},
			}
			want := []string{"ui-contract"}
			if version == "data-product-ui/v2" {
				want = []string{"ui-appearance", "ui-contract"}
			}
			wire := bundle(t, p)
			legacy := Check(t.Context(), strings.NewReader(wire), "")
			current := CheckBundle(t.Context(), []io.Reader{strings.NewReader(wire)}, "")
			if version == "data-product-ui/v1" &&
				reflect.DeepEqual(caps, []data.UICapability{"appearance"}) {
				if legacy.Valid || current.Valid {
					t.Fatal("v1 appearance bypassed publication admission")
				}
				continue
			}
			if !legacy.Valid || !legacy.Complete || !current.Valid || !current.Complete ||
				len(current.Descriptors) != 1 {
				t.Fatalf(
					"valid declaration lost complete preview: legacy=%+v current=%+v",
					legacy,
					current,
				)
			}
			if !reflect.DeepEqual(legacy.RequiredFeatures, want) ||
				!reflect.DeepEqual(current.RequiredFeatures, want) ||
				len(current.ProductFeatures) != 1 ||
				!reflect.DeepEqual(current.ProductFeatures[0].RequiredFeatures, want) {
				t.Errorf(
					"%s %v: requirements=%v per-product=%v want=%v",
					version,
					caps,
					current.RequiredFeatures,
					current.ProductFeatures,
					want,
				)
			}
		}
	}
}

// TestUnknownFieldKeepsSafeContainerProvenance never reports private unknown names or values.
func TestUnknownFieldKeepsSafeContainerProvenance(t *testing.T) {
	input := "apiVersion: data.devantler.tech/v1alpha1\nkind: DataProduct\nmetadata:\n  name: product\nspec:\n  outputs:\n    - name: observations\n      PRIVATE_FIELD: PRIVATE_VALUE\n"
	r := selected(t, input)
	d := richCode(t, r, "UnknownField")
	if d.Path != "/spec/outputs/0" || d.Line != 7 || d.Column != 7 {
		t.Fatalf("unknown field lost safe container: %+v", d)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatalf("unknown submitted field escaped report: %s", encoded)
	}
	legacy := Check(t.Context(), strings.NewReader(input), "")
	if len(legacy.Diagnostics) != 1 || legacy.Diagnostics[0].Code != "UnknownField" ||
		legacy.Diagnostics[0].Document != 1 ||
		legacy.Diagnostics[0].Path != "" {
		t.Fatalf("legacy diagnostic changed: %+v", legacy)
	}
	encoded, _ = json.Marshal(legacy)
	if strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatalf("unknown submitted field escaped legacy report: %s", encoded)
	}
	multi := selected(t, bundle(t, product("first")), "---\n---\n"+input)
	at := richCode(t, multi, "UnknownField")
	if at.Source != 2 || at.Document != 2 || at.Line != 9 || at.Column != 7 ||
		at.Path != "/spec/outputs/0" {
		t.Fatalf("physical selected-source location lost: %+v", at)
	}
}

// TestLiteralUnknownKeysCannotForgeLocations keeps decoder path punctuation private.
func TestLiteralUnknownKeysCannotForgeLocations(t *testing.T) {
	for _, tc := range []struct{ input, path string }{
		{"apiVersion: data.devantler.tech/v1alpha1\nkind: DataProduct\nmetadata: {name: product}\nspec:\n  outputs: [{name: observations}]\n  'outputs[0].name': PRIVATE_VALUE\n", "/spec"},
		{"apiVersion: data.devantler.tech/v1alpha1\nkind: DataProduct\nmetadata: {name: product}\n'metadata.name': PRIVATE_VALUE\nspec: {}\n", ""},
		{"apiVersion: data.devantler.tech/v1alpha1\nkind: DataProduct\nmetadata: {name: product}\n'manifest.spec.outputs[0].name': PRIVATE_VALUE\nspec: {outputs: [{name: observations}]}\n", ""},
	} {
		r := selected(t, tc.input)
		d := richCode(t, r, "UnknownField")
		if d.Path != tc.path {
			t.Fatalf("literal key forged known location: %+v want=%q", d, tc.path)
		}
	}
}
