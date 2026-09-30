package dataspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"github.com/devantler-tech/data-product-controller/internal/dataspace"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

// TestControllerSnapshotToProviderCatalog crosses the real producer/consumer
// boundary so a hand-authored JSON fixture cannot conceal profile drift.
func TestControllerSnapshotToProviderCatalog(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../docs/examples/dcat-product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var product datav1.DataProduct
	if err := yaml.Unmarshal(b, &product); err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	if err := datav1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&product).Build()
	handler, err := catalog.NewHandler(
		reader,
		catalog.Options{
			ID:      "urn:example:controller-catalog",
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
	if response.Code != http.StatusOK {
		t.Fatalf("producer failed: %s", response.Body)
	}
	var source struct {
		Datasets []struct {
			ID            string `json:"@id"`
			Distributions []struct {
				ID string `json:"@id"`
			} `json:"dcat:distribution"`
		} `json:"dcat:dataset"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Datasets) != 1 || len(source.Datasets[0].Distributions) != 1 {
		t.Fatal("producer fixture missing dataset/output")
	}
	bindings := bytes.ReplaceAll(
		example(t, "bindings"),
		[]byte("urn:example:harbour\""),
		[]byte(source.Datasets[0].ID+"\""),
	)
	bindings = bytes.ReplaceAll(
		bindings,
		[]byte("urn:example:observations"),
		[]byte(source.Datasets[0].Distributions[0].ID),
	)
	result, err := dataspace.Export(
		bytes.NewReader(response.Body.Bytes()),
		bytes.NewReader(bindings),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(product.Spec.ID)) ||
		!bytes.Contains(result, []byte(product.Spec.Description)) {
		t.Fatal("real publisher metadata was not preserved")
	}
}

func TestAggregateLimitsAndLateFailure(t *testing.T) {
	t.Parallel()
	for _, count := range []int{256, 257} {
		source := object(t, example(t, "catalog"))
		bindings := object(t, example(t, "bindings"))
		var datasets, services, selected []any
		for i := 0; i < count; i++ {
			// Distinct public identities for each dataset and all related resources.
			suffix := []byte("urn:example:" + stringID(i) + "-")
			s := object(t, bytes.ReplaceAll(example(t, "catalog"), []byte("urn:example:"), suffix))
			p := object(t, bytes.ReplaceAll(example(t, "bindings"), []byte("urn:example:"), suffix))
			ds, ok := s["dcat:dataset"].([]any)
			if !ok {
				t.Fatal("fixture datasets")
			}
			ss, ok := s["dcat:service"].([]any)
			if !ok {
				t.Fatal("fixture services")
			}
			bs, ok := p["datasets"].([]any)
			if !ok {
				t.Fatal("fixture bindings")
			}
			// Every offer remains assigned to the one bound participant.
			bs = decodeArray(
				t,
				bytes.ReplaceAll(
					encode(t, bs),
					append(bytes.Clone(suffix), []byte("provider")...),
					[]byte("urn:example:provider"),
				),
			)
			datasets = append(datasets, ds...)
			services = append(services, ss...)
			selected = append(selected, bs...)
		}
		source["dcat:dataset"] = datasets
		source["dcat:service"] = services
		bindings["datasets"] = selected
		b, err := dataspace.Export(
			bytes.NewReader(encode(t, source)),
			bytes.NewReader(encode(t, bindings)),
		)
		if count == 256 && err != nil {
			t.Fatal(err)
		}
		if count == 257 && (err == nil || len(b) != 0) {
			t.Fatal("dataset budget did not reject full snapshot")
		}
		if count == 256 {
			// The last offer fails only after all earlier datasets were processed.
			bad := bytes.ReplaceAll(
				encode(t, bindings),
				[]byte("urn:example:255-harbour-offer"),
				[]byte("urn:example:dsp-catalog"),
			)
			b, err = dataspace.Export(bytes.NewReader(encode(t, source)), bytes.NewReader(bad))
			if err == nil || len(b) != 0 {
				t.Fatal("late collision returned a partial export")
			}
			large := bytes.ReplaceAll(
				encode(t, bindings),
				[]byte("https://connector.example/dsp"),
				[]byte("https://connector.example/"+strings.Repeat("x", 16000)),
			)
			b, err = dataspace.Export(bytes.NewReader(encode(t, source)), bytes.NewReader(large))
			if err == nil || len(b) != 0 ||
				!strings.Contains(err.Error(), "encoded catalog exceeds") {
				t.Fatalf("output amplification budget failed: %v", err)
			}
		}
	}
}

func stringID(i int) string { return fmt.Sprint(i) }

func decodeArray(t *testing.T, b []byte) []any {
	t.Helper()
	var a []any
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
