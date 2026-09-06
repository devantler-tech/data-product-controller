package v1alpha1

import (
	"encoding/json"
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// TestConnectorSchema validates the authored example and rejects unsupported references using the generated schema.
func TestConnectorSchema(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../config/crd/bases/data.devantler.tech_dataproducts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPI *spec.Schema `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Schema.OpenAPI == nil {
		t.Fatal("missing generated validation schema")
	}
	example, err := os.ReadFile("../../docs/examples/http-source-product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var product map[string]any
	if err := yaml.Unmarshal(example, &product); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, field, value string
		remove, valid      bool
	}{
		{name: "authored example", valid: true},
		{name: "legacy product", remove: true, valid: true},
		{name: "explicit namespace", field: "namespace", value: "other"},
		{name: "unsupported API", field: "apiVersion", value: "apps/v1beta1"},
		{name: "unsupported kind", field: "kind", value: "Secret"},
		{name: "empty name", field: "name"},
		{name: "invalid name", field: "name", value: "../export"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var candidate map[string]any
			encoded, err := json.Marshal(product)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &candidate); err != nil {
				t.Fatal(err)
			}
			productSpec, ok := candidate["spec"].(map[string]any)
			if !ok {
				t.Fatal("example spec must be an object")
			}
			if test.remove {
				delete(productSpec, "connector")
			} else if test.field != "" {
				connector, ok := productSpec["connector"].(map[string]any)
				if !ok {
					t.Fatal("example connector must be an object")
				}
				ref, ok := connector["resourceRef"].(map[string]any)
				if !ok {
					t.Fatal("example connector reference must be an object")
				}
				ref[test.field] = test.value
			}
			err = validate.AgainstSchema(
				crd.Spec.Versions[0].Schema.OpenAPI,
				candidate,
				strfmt.Default,
			)
			if (err == nil) != test.valid {
				t.Fatalf("schema validation error = %v, want valid=%t", err, test.valid)
			}
		})
	}
}
