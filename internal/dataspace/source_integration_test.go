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
	controllerRoundTrip(t, &product, "urn:example:controller-catalog", http.StatusOK)
	t.Run("deliberate replacement scalar", func(t *testing.T) {
		changed := product.DeepCopy()
		changed.Spec.Description = "Sensor label � retained"
		controllerRoundTrip(t, changed, "urn:example:controller-catalog", http.StatusOK)
	})
	for _, tc := range []struct {
		name   string
		mutate func(*datav1.DataProduct, string)
	}{
		{"output", func(p *datav1.DataProduct, u string) { p.Spec.Outputs[0].URL = u }},
		{"contract", func(p *datav1.DataProduct, u string) { p.Spec.Outputs[0].ContractURL = u }},
		{"owner", func(p *datav1.DataProduct, u string) { p.Spec.Owner.URL = u }},
		{"documentation", func(p *datav1.DataProduct, u string) { p.Spec.DocumentationURL = u }},
		{"identity", func(p *datav1.DataProduct, u string) { p.Spec.ID = u }},
	} {
		for _, urlCase := range []struct {
			name, url string
			status    int
		}{
			{"raw path", "https://example.test/item[0]", http.StatusUnprocessableEntity},
			{"raw query", "https://example.test/query?fields[]=temperature", http.StatusUnprocessableEntity},
			{"raw fragment", "https://example.test/openapi#part[0]", http.StatusUnprocessableEntity},
			{"encoded", "https://example.test/item%5B0%5D?fields%5B%5D=temperature#part%5B0%5D", http.StatusOK},
			{"IPv6", "https://[2001:db8::1]/query", http.StatusOK},
		} {
			t.Run(tc.name+"/"+urlCase.name, func(t *testing.T) {
				t.Parallel()
				changed := product.DeepCopy()
				tc.mutate(changed, urlCase.url)
				controllerRoundTrip(t, changed, "urn:example:controller-catalog", urlCase.status)
			})
		}
	}
	for _, id := range []string{"https://example.test/catalog[0]", "https://example.test/catalog?group[]=data", "https://example.test/catalog#part[0]"} {
		if _, err := catalog.NewHandler(nil, catalog.Options{ID: id}); err == nil {
			t.Fatalf("configured catalog accepted incompatible ID: %q", id)
		}
	}
	controllerRoundTrip(
		t,
		&product,
		"https://[2001:db8::1]/catalog%5B0%5D#part%5B0%5D",
		http.StatusOK,
	)
}

func controllerRoundTrip(t *testing.T, product *datav1.DataProduct, id string, wantStatus int) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := datav1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
	handler, err := catalog.NewHandler(
		reader,
		catalog.Options{
			ID:      id,
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
	if response.Code != wantStatus {
		t.Fatalf("producer failed: %s", response.Body)
	}
	if wantStatus != http.StatusOK {
		if strings.Contains(response.Body.String(), "dcat:dataset") {
			t.Fatal("rejected metadata exposed a partial catalog")
		}
		return
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

// TestAggregateLimitsAndLateFailure checks full-size graphs and errors after earlier valid datasets.
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

// stringID creates a deterministic distinct fixture identity suffix.
func stringID(i int) string { return fmt.Sprint(i) }

// decodeArray restores generated fixture lists after replacing their provider identity.
func decodeArray(t *testing.T, b []byte) []any {
	t.Helper()
	var a []any
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
