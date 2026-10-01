package v1alpha1

import (
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// TestUIContractSchema ensures API admission preserves and constrains declared UI permissions.
func TestUIContractSchema(t *testing.T) {
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
	ui := crd.Spec.Versions[0].Schema.OpenAPI.Properties["spec"].Properties["ui"]
	contract, exists := ui.Properties["contract"]
	if !exists {
		t.Fatal("UI contract permissions are pruned by the installed CRD")
	}
	for _, tc := range []struct {
		name, version, origin, capability string
		valid                             bool
	}{
		{"valid", "data-product-ui/v1", "https://catalog.example", "status", true},
		{"port", "data-product-ui/v1", "https://catalog.example:8443", "resize", true},
		{"appearance", "data-product-ui/v2", "https://catalog.example", "appearance", true},
		{"version", "v2", "https://catalog.example", "status", false},
		{"wildcard", "data-product-ui/v1", "*", "status", false},
		{"path", "data-product-ui/v1", "https://catalog.example/path", "status", false},
		{"http", "data-product-ui/v1", "http://catalog.example", "status", false},
		{"credentials", "data-product-ui/v1", "https://user@catalog.example", "status", false},
		{"capability", "data-product-ui/v1", "https://catalog.example", "credentials", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validate.AgainstSchema(
				&contract,
				map[string]any{
					"apiVersion":   tc.version,
					"hostOrigins":  []any{tc.origin},
					"capabilities": []any{tc.capability},
				},
				strfmt.Default,
			)
			if (err == nil) != tc.valid {
				t.Fatalf("admission=%v, want valid=%t", err, tc.valid)
			}
		})
	}
}
