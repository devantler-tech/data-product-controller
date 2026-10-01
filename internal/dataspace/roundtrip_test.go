package dataspace_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"github.com/devantler-tech/data-product-controller/internal/dataspace"
	"github.com/piprate/json-gold/ld"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

// TestPublisherCatalogSemanticRoundTrip compares the complete independently
// reconstructed graph with a hand-written expectation, including policy meaning.
func TestPublisherCatalogSemanticRoundTrip(t *testing.T) {
	t.Parallel()
	exported := publisherCatalogExport(t)
	wantBytes, err := os.ReadFile("testdata/roundtrip.nq")
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalQuads(t, string(wantBytes))
	got := roundTripQuads(t, exported)
	if got != want {
		t.Fatalf("catalog lost or invented RDF meaning\nwant:\n%s\ngot:\n%s", want, got)
	}

	// These corruptions remain valid JSON-LD. A schema-only or substring check
	// could pass them; the complete graph comparison must detect each one.
	for _, mutation := range []struct {
		name, before, after string
	}{
		{"dataset identity", `"urn:example:catalog-harbour"`, `"urn:example:wrong-dataset"`},
		{"distribution link", `"urn:example:dsp-observations"`, `"urn:example:wrong-distribution"`},
		{"service link", `"urn:example:dsp-service"`, `"urn:example:wrong-service"`},
		{"assigner", `"assigner":"urn:example:provider"`, `"assigner":"urn:example:other-provider"`},
		{"prohibition lost", `"prohibition":[{"action":"http://www.w3.org/ns/odrl/2/sell"}],`, ""},
		{"obligation lost", `"obligation":[{"action":"http://www.w3.org/ns/odrl/2/attribute"}]`, `"obligation":[]`},
		{"numeric string coerced", `"rightOperand":"007"`, `"rightOperand":7`},
		{"boolean string coerced", `"rightOperand":"true"`, `"rightOperand":true`},
		{"IRI string coerced", `"rightOperand":"https://example.com/research"`, `"rightOperand":{"@id":"https://example.com/research"}`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			t.Parallel()
			if !bytes.Contains(exported, []byte(mutation.before)) {
				t.Fatal("mutation did not reach the exported graph")
			}
			changed := bytes.ReplaceAll(exported, []byte(mutation.before), []byte(mutation.after))
			if roundTripQuads(t, changed) == want {
				t.Fatal("semantic comparison accepted corrupted catalog")
			}
		})
	}
}

// publisherCatalogExport reads a real product example through the actual HTTP
// catalog. Provider bindings select one output, leaving other valid data behind.
func publisherCatalogExport(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../docs/examples/dcat-product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var product datav1.DataProduct
	if err := yaml.Unmarshal(b, &product); err != nil {
		t.Fatal(err)
	}
	product.Spec.Outputs = append(product.Spec.Outputs, datav1.OutputPort{
		Name:        "archive",
		Protocol:    datav1.ProtocolOpenAPI,
		URL:         "https://example.com/harbour/archive",
		ContractURL: "https://example.com/harbour/archive.json",
		MediaType:   "application/json",
	})
	unselected := product.DeepCopy()
	unselected.Name = "unselected"
	unselected.Spec.ID = "urn:example:unselected"
	unselected.Spec.Name = "Unselected archive"
	unselected.Spec.Description = "This dataset must never supply metadata for the selected product."
	unselected.Spec.Version = "v9.9.9"
	scheme := runtime.NewScheme()
	if err := datav1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&product, unselected).Build()
	handler, err := catalog.NewHandler(reader, catalog.Options{
		ID: "urn:example:controller-catalog", Enabled: func(context.Context) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/catalog", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("publisher catalog: %d %s", response.Code, response.Body)
	}
	source := object(t, response.Body.Bytes())
	datasets, ok := source["dcat:dataset"].([]any)
	if !ok || len(datasets) != 2 {
		t.Fatal("fixture must publish both datasets before provider selection")
	}
	var selectedID string
	for _, entry := range datasets {
		dataset, ok := entry.(map[string]any)
		if !ok {
			t.Fatal("invalid source dataset")
		}
		if dataset["@id"] != "urn:example:catalog-harbour" {
			continue
		}
		outputs, ok := dataset["dcat:distribution"].([]any)
		if !ok || len(outputs) != 2 {
			t.Fatal("fixture must publish both outputs before provider selection")
		}
		for _, output := range outputs {
			distribution, ok := output.(map[string]any)
			if !ok {
				t.Fatal("invalid source distribution")
			}
			if distribution["dcterms:title"] == "observations" {
				selectedID, ok = distribution["@id"].(string)
				if !ok {
					t.Fatal("selected output has no public identity")
				}
			}
		}
	}
	if selectedID == "" {
		t.Fatal("publisher omitted the selected observation output")
	}
	bindings := object(t, example(t, "bindings"))
	selections, ok := bindings["datasets"].([]any)
	if !ok || len(selections) != 1 {
		t.Fatal("invalid reference bindings")
	}
	selection, ok := selections[0].(map[string]any)
	if !ok {
		t.Fatal("invalid reference selection")
	}
	selection["id"] = "urn:example:catalog-harbour"
	selection["distributions"] = []any{map[string]any{
		"sourceId": selectedID, "id": "urn:example:dsp-observations",
		"format": "https://example.com/transfer/harbour-pull",
	}}
	selection["offers"] = []any{object(t, []byte(`{
		"@id":"urn:example:harbour-offer", "@type":"Offer", "assigner":"urn:example:provider",
		"permission":[{"action":"use","constraint":[
			{"leftOperand":"http://www.w3.org/ns/odrl/2/purpose","operator":"eq","rightOperand":"research"},
			{"leftOperand":"http://www.w3.org/ns/odrl/2/count","operator":"lteq","rightOperand":"007"},
			{"leftOperand":"https://example.com/approved","operator":"eq","rightOperand":"true"},
			{"leftOperand":"https://example.com/project","operator":"isAnyOf","rightOperand":"https://example.com/research"}
		]}],
		"prohibition":[{"action":"http://www.w3.org/ns/odrl/2/sell"}],
		"obligation":[{"action":"http://www.w3.org/ns/odrl/2/attribute"}]
	}`))}
	result, err := dataspace.Export(
		bytes.NewReader(response.Body.Bytes()),
		bytes.NewReader(encode(t, bindings)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// roundTripQuads reconstructs JSON-LD from RDF with pinned offline contexts,
// then canonicalizes it without depending on exporter ordering or blank-node IDs.
func roundTripQuads(t *testing.T, document []byte) string {
	t.Helper()
	loader := offlineContexts{docs: map[string]any{}}
	for _, name := range []string{"context", "odrl-profile"} {
		b, err := standards.ReadFile("testdata/dsp-2025-1-err2/" + name + ".jsonld")
		if err != nil {
			t.Fatal(err)
		}
		loader.docs["https://w3id.org/dspace/2025/1/"+name+".jsonld"] = object(t, b)
	}
	options := ld.NewJsonLdOptions("")
	options.DocumentLoader = loader
	options.Format = "application/n-quads"
	processor := ld.NewJsonLdProcessor()
	rdf, err := processor.ToRDF(object(t, document), options)
	if err != nil {
		t.Fatal(err)
	}
	quads, ok := rdf.(string)
	if !ok {
		t.Fatal("independent consumer returned no RDF")
	}
	decoded, err := processor.FromRDF(quads, options)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := processor.ToRDF(decoded, options)
	if err != nil {
		t.Fatal(err)
	}
	quads, ok = reencoded.(string)
	if !ok {
		t.Fatal("round-trip consumer returned no RDF")
	}
	return canonicalQuads(t, quads)
}

// canonicalQuads normalizes a nonempty RDF graph so triple ordering and blank-node
// identifiers cannot hide a change in its meaning.
func canonicalQuads(t *testing.T, quads string) string {
	t.Helper()
	options := ld.NewJsonLdOptions("")
	options.InputFormat = "application/n-quads"
	options.Format = "application/n-quads"
	options.Algorithm = "URDNA2015"
	result, err := ld.NewJsonLdProcessor().Normalize(quads, options)
	if err != nil {
		t.Fatal(err)
	}
	canonical, ok := result.(string)
	if !ok || strings.TrimSpace(canonical) == "" {
		t.Fatal("canonicalization returned no graph")
	}
	return canonical
}
