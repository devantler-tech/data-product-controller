package v1alpha1

import (
	"os"
	"strings"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

func TestCanonicalProductReferenceNameAdmission(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../config/crd/bases/data.devantler.tech_dataproducts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPI spec.Schema `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	ref := crd.Spec.Versions[0].Schema.OpenAPI.Properties["spec"].Properties["inputs"].Items.Schema.Properties["productRef"]
	longest := strings.Repeat(
		"a",
		63,
	) + "." + strings.Repeat(
		"b",
		63,
	) + "." + strings.Repeat(
		"c",
		63,
	) + "." + strings.Repeat(
		"d",
		61,
	)
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"source", true},
		{"warehouse.daily", true},
		{strings.Repeat("a", 40) + "." + strings.Repeat("b", 40), true},
		{longest, true},
		{"a..b", false},
		{"a.-b", false},
		{"a-.b", false},
		{".source", false},
		{"source.", false},
		{"Source", false},
		{strings.Repeat("a", 254), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validate.AgainstSchema(
				&ref,
				map[string]any{"name": tc.name, "output": "query"},
				strfmt.Default,
			)
			if (err == nil) != tc.valid {
				t.Fatalf("reference admission valid=%t, want %t: %v", err == nil, tc.valid, err)
			}
		})
	}
	for _, field := range []string{"namespace", "output"} {
		t.Run(field+"-label-bound", func(t *testing.T) {
			t.Parallel()
			values := map[string]any{"name": "warehouse.daily", "output": "query"}
			values[field] = "a.b"
			if err := validate.AgainstSchema(&ref, values, strfmt.Default); err == nil {
				t.Fatalf("%s accepted a product subdomain as a namespace or port label", field)
			}
		})
	}
}
