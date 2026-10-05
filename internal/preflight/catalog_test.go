package preflight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDatasetPreflightAgreesWithPublishedCatalog(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*data.DataProduct){
		"short-urn": func(p *data.DataProduct) { p.Spec.ID = "urn:x:y" },
		"long-output": func(p *data.DataProduct) {
			p.Spec.Outputs[0].URL = "https://example.test/" + strings.Repeat("x", 2049-len("https://example.test/"))
		},
		"long-contract": func(p *data.DataProduct) {
			p.Spec.Outputs[0].ContractURL = "https://example.test/" + strings.Repeat("x", 2049-len("https://example.test/"))
		},
		"wrong-profile": func(p *data.DataProduct) { p.Annotations["data.devantler.tech/dcat-type"] = "dataset" },
		"empty-profile": func(p *data.DataProduct) { p.Annotations["data.devantler.tech/dcat-type"] = "" },
		"valid":         func(*data.DataProduct) {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := product("dataset")
			p.Annotations = map[string]string{"data.devantler.tech/dcat-type": "Dataset"}
			mutate(&p)
			scheme := runtime.NewScheme()
			if err := data.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&p).Build()
			handler, err := catalog.NewHandler(
				reader,
				catalog.Options{
					ID:      "urn:example:catalog",
					Enabled: func(context.Context) bool { return true },
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/catalog", nil),
			)
			valid := name == "valid"
			if (response.Code == http.StatusOK) != valid {
				t.Fatalf("catalog status = %d: %s", response.Code, response.Body)
			}
			if !valid &&
				(response.Code != 422 || strings.Contains(response.Body.String(), "dcat:dataset")) {
				t.Fatal("catalog returned partial metadata")
			}
			wire := bundle(t, p)
			legacy, current := check(t, wire), selected(t, wire)
			if valid {
				if !legacy.Valid || !legacy.Complete || !current.Valid || !current.Complete ||
					len(legacy.Descriptors) != 1 || len(current.Descriptors) != 1 ||
					!strings.Contains(strings.Join(legacy.RequiredFeatures, ","), "dcat-catalog") {
					t.Fatal("publishable Dataset rejected")
				}
			} else {
				if legacy.Valid || legacy.Complete || current.Valid || current.Complete ||
					len(legacy.Descriptors) != 0 || len(current.Descriptors) != 0 {
					t.Fatal("preflight admitted an unpublishable Dataset")
				}
				for _, report := range []any{legacy, current} {
					encoded, err := json.Marshal(report)
					if err != nil || strings.Contains(string(encoded), p.Spec.Outputs[0].URL) {
						t.Fatal("rejected endpoint escaped diagnostics")
					}
				}
			}
			// Dataset-specific limits do not change ordinary registry publication.
			if name == "short-urn" || name == "long-output" || name == "long-contract" {
				p.Annotations = nil
				unannotated := check(t, bundle(t, p))
				if !unannotated.Valid || !unannotated.Complete {
					t.Fatalf(
						"ordinary product lost registry admission: %+v",
						unannotated.Diagnostics,
					)
				}
			}
		})
	}
}
