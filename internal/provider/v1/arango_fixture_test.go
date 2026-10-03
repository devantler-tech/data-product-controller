package v1

import (
	"maps"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"
)

// The upstream operator uses these labels for both Pod inspection and Service routing.
// An override can leave a running server invisible to reconciliation and its clients.
func TestArangoFixturePreservesOperatorSelectors(t *testing.T) {
	data, err := os.ReadFile("../../../tests/provider/arango.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Metadata struct{ Name string }
		Spec     struct {
			Single struct{ Labels map[string]string }
		}
	}
	if err := yaml.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	reserved := labels.Set{
		"app":               "arangodb",
		"arango_deployment": fixture.Metadata.Name,
		"role":              "single",
	}
	podLabels := maps.Clone(reserved)
	maps.Copy(podLabels, fixture.Spec.Single.Labels)
	if !labels.SelectorFromSet(reserved).Matches(podLabels) {
		t.Fatal("fixture overrides labels required by the operator and database Service")
	}
}
