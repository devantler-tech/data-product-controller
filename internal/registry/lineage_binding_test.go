package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestRound13AuditLineageAssociation excludes lineage that differs from the declared input binding.
func TestRound13AuditLineageAssociation(t *testing.T) {
	for _, scenario := range []string{"undeclared-input", "different-producer", "different-output"} {
		t.Run(scenario, func(t *testing.T) {
			p := schemaFixtureProduct()
			switch scenario {
			case "undeclared-input":
				p.Status.Inputs[0].Name = "unpublished"
			case "different-producer":
				p.Status.Inputs[0].ProductRef.Name = "different-producer"
			case "different-output":
				p.Status.Inputs[0].Output.Name = "different-output"
			}
			reader := discoveryReader{
				list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
					discoveryProductList(t, out).Items = []data.DataProduct{*p}
					return nil
				},
			}
			got := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products")
			var page discoveryPage
			if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &page) != nil {
				t.Fatal("unexpected API failure")
			}
			if len(page.Products) != 0 || page.Rejected != 1 {
				t.Fatalf(
					"published disconnected lineage: descriptors=%d rejected=%d",
					len(page.Products),
					page.Rejected,
				)
			}
		})
	}
}
