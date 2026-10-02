package v1alpha1

import (
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// TestEngineSchema checks field preservation and scalar validation; hosted API-server tests enforce the CEL matrix.
func TestEngineSchema(t *testing.T) {
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
	source := crd.Spec.Versions[0].Schema.OpenAPI.Properties["spec"].Properties["source"]
	engine, exists := source.Properties["engine"]
	if !exists {
		t.Fatal("engine selection is pruned by the installed CRD")
	}
	t.Run("Graph adapter is preserved and admitted", func(t *testing.T) {
		err := validate.AgainstSchema(&source, map[string]any{
			"adapter": "arangodb/v1",
			"engine": map[string]any{
				"apiVersion": "engine-provider/v1",
				"type":       "graph",
				"provider":   "native",
			},
			"resourceRef": map[string]any{
				"apiVersion": "database.arangodb.com/v1",
				"kind":       "ArangoDeployment",
				"name":       "lineage",
			},
			"connectionSecretRef": map[string]any{"name": "lineage-reader"},
		}, strfmt.Default)
		if err != nil {
			t.Fatalf("supported Graph scalar schema rejected: %v", err)
		}
	})
	for _, tc := range []struct {
		name, version, kind, provider string
		valid                         bool
	}{
		{"supported scalars", "engine-provider/v1", "sql", "native", true},
		{"unknown version", "engine-provider/v2", "sql", "native", false},
		{"unknown model", "engine-provider/v1", "csv", "native", false},
		{"unknown provider", "engine-provider/v1", "sql", "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validate.AgainstSchema(
				&engine,
				map[string]any{"apiVersion": tc.version, "type": tc.kind, "provider": tc.provider},
				strfmt.Default,
			)
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%t", err, tc.valid)
			}
		})
	}
}
