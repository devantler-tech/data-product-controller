package v1alpha1

import (
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// TestContractCheckSchema rejects malformed or excessive observation requests before reconciliation.
func TestContractCheckSchema(t *testing.T) {
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
	checks := crd.Spec.Versions[0].Schema.OpenAPI.Properties["spec"].Properties["contractChecks"]
	for _, tc := range []struct {
		name, field string
		value       any
		count       int
		valid       bool
	}{
		{"valid", "", nil, 1, true},
		{"none", "", nil, 0, true},
		{"too many", "", nil, 9, false},
		{"cross namespace", "namespace", "other", 1, false},
		{"wrong kind", "kind", "Secret", 1, false},
		{"wrong API", "apiVersion", "apps/v1beta1", 1, false},
		{"invalid name", "name", "../probe", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidate := make([]any, 0, tc.count)
			for range tc.count {
				ref := map[string]any{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"name":       "contract-probe",
				}
				if tc.field != "" {
					ref[tc.field] = tc.value
				}
				candidate = append(candidate, map[string]any{"output": "query", "resourceRef": ref})
			}
			err := validate.AgainstSchema(&checks, candidate, strfmt.Default)
			if (err == nil) != tc.valid {
				t.Fatalf("schema error %v, want valid %t", err, tc.valid)
			}
		})
	}
}
