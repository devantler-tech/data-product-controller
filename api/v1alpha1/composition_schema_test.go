package v1alpha1

import (
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// TestInputContractSchema catches malformed requirements before they reach reconciliation.
func TestInputContractSchema(t *testing.T) {
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
	input := crd.Spec.Versions[0].Schema.OpenAPI.Properties["spec"].Properties["inputs"].Items.Schema
	contract, exists := input.Properties["contract"]
	if !exists {
		t.Fatal("input contracts are pruned by the installed CRD")
	}
	for _, tc := range []struct {
		version, protocol string
		valid             bool
	}{
		{"v1.2.3", "OpenAPI", true},
		{"v0.1.0", "GraphQL", true},
		{"v1.2", "OpenAPI", false},
		{"1.2.3", "OpenAPI", false},
		{"v01.2.3", "OpenAPI", false},
		{"v1.0.0-rc.1", "OpenAPI", false},
		{"v1.0.0+build", "OpenAPI", false},
		{"v1.0.0", "unknown", false},
	} {
		t.Run(tc.version+tc.protocol, func(t *testing.T) {
			t.Parallel()
			err := validate.AgainstSchema(
				&contract,
				map[string]any{"minimumVersion": tc.version, "protocol": tc.protocol},
				strfmt.Default,
			)
			if (err == nil) != tc.valid {
				t.Fatalf("schema error=%v, want valid=%t", err, tc.valid)
			}
		})
	}
}
