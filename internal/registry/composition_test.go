package registry

import (
	"encoding/json"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDescriptorCompositionFreshness catches publishing obsolete lineage as current evidence.
func TestDescriptorCompositionFreshness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		generation int64
		condition  bool
		want       bool
	}{
		{"fresh", 2, true, true}, {"stale", 1, true, false}, {"absent", 2, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &datav1alpha1.DataProduct{ObjectMeta: metav1.ObjectMeta{Generation: 2}}
			p.Status.Inputs = []datav1alpha1.InputStatus{
				{
					Name: "observations",
					ProductRef: datav1alpha1.ProductReference{
						Name:      "harbour",
						Namespace: "products",
						Output:    "query",
					},
					Version: "v1.2.0",
					Ready:   true,
					Reason:  "InputReady",
				},
			}
			if tc.condition {
				p.Status.Conditions = []metav1.Condition{
					{
						Type:               datav1alpha1.ConditionCompositionReady,
						Status:             metav1.ConditionTrue,
						ObservedGeneration: tc.generation,
						Reason:             "CompositionVerified",
					},
				}
			}
			data, err := json.Marshal(descriptorFor(p))
			if err != nil {
				t.Fatal(err)
			}
			var descriptor map[string]json.RawMessage
			if err := json.Unmarshal(data, &descriptor); err != nil {
				t.Fatal(err)
			}
			_, present := descriptor["lineage"]
			if present != tc.want {
				t.Fatalf("lineage presence = %t, want %t: %s", present, tc.want, data)
			}
			if tc.want {
				var edges []datav1alpha1.InputStatus
				if err := json.Unmarshal(descriptor["lineage"], &edges); err != nil {
					t.Fatal(err)
				}
				if len(edges) != 1 || edges[0].Version != "v1.2.0" ||
					edges[0].ProductRef.Namespace != "products" {
					t.Fatalf("lineage = %+v", edges)
				}
			}
		})
	}
}
