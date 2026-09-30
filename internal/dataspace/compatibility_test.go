package dataspace_test

import (
	"bytes"
	"embed"
	"errors"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
	"github.com/piprate/json-gold/ld"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed testdata/dsp-2025-1-err2/*.json testdata/dsp-2025-1-err2/*.jsonld
var standards embed.FS

type offlineSchemas struct{}

func (offlineSchemas) Load(string) (any, error) {
	return nil, errors.New("schema network loading forbidden")
}

type offlineContexts struct{ docs map[string]any }

func (l offlineContexts) LoadDocument(u string) (*ld.RemoteDocument, error) {
	d, ok := l.docs[u]
	if !ok {
		return nil, errors.New("context not pinned")
	}
	return &ld.RemoteDocument{DocumentURL: u, Document: d}, nil
}

// TestOfficialSchemasAndIndependentRDF tests wire syntax and meaning separately;
// the official schemas alone do not verify JSON-LD term expansion.
func TestOfficialSchemasAndIndependentRDF(t *testing.T) {
	t.Parallel()
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineSchemas{})
	for _, name := range []string{"catalog-schema", "dataset-schema", "contract-schema", "context-schema"} {
		b, err := standards.ReadFile("testdata/dsp-2025-1-err2/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		doc := object(t, b)
		id, ok := doc["$id"].(string)
		if !ok {
			t.Fatal("schema ID missing")
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := c.Compile("https://w3id.org/dspace/2025/1/catalog/catalog-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	loader := offlineContexts{docs: map[string]any{}}
	for _, name := range []string{"context", "odrl-profile"} {
		b, err := standards.ReadFile("testdata/dsp-2025-1-err2/" + name + ".jsonld")
		if err != nil {
			t.Fatal(err)
		}
		loader.docs["https://w3id.org/dspace/2025/1/"+name+".jsonld"] = object(t, b)
	}
	for _, empty := range []bool{false, true} {
		bindings := object(t, example(t, "bindings"))
		if empty {
			bindings["datasets"] = []any{}
		}
		b, err := dataspace.Export(
			bytes.NewReader(example(t, "catalog")),
			bytes.NewReader(encode(t, bindings)),
		)
		if err != nil {
			t.Fatal(err)
		}
		doc := object(t, b)
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("official schema: %v", err)
		}
		options := ld.NewJsonLdOptions("")
		options.DocumentLoader = loader
		options.Format = "application/n-quads"
		rdf, err := ld.NewJsonLdProcessor().ToRDF(doc, options)
		if err != nil {
			t.Fatal(err)
		}
		quads, ok := rdf.(string)
		if !ok {
			t.Fatal("RDF consumer returned no N-Quads")
		}
		required := []string{
			`<urn:example:dsp-catalog> <https://w3id.org/dspace/2025/1/participantId> <urn:example:provider>`,
			`<urn:example:dsp-catalog> <http://www.w3.org/ns/dcat#service> <urn:example:dsp-service>`,
		}
		if !empty {
			required = append(
				required,
				`<urn:example:harbour> <http://www.w3.org/ns/odrl/2/hasPolicy> <urn:example:harbour-offer>`,
				`<urn:example:harbour-offer> <http://www.w3.org/ns/odrl/2/assigner> <urn:example:provider>`,
				`<urn:example:dsp-observations> <http://www.w3.org/ns/dcat#accessService> <urn:example:dsp-service>`,
				`<urn:example:dsp-observations> <http://purl.org/dc/terms/format> <https://example.com/transfer/harbour-pull>`,
				`<http://www.w3.org/ns/odrl/2/action> <http://www.w3.org/ns/odrl/2/use>`,
				`<http://www.w3.org/ns/odrl/2/action> <http://www.w3.org/ns/odrl/2/sell>`,
				`<http://www.w3.org/ns/odrl/2/leftOperand> <http://www.w3.org/ns/odrl/2/purpose>`,
				`<http://www.w3.org/ns/odrl/2/rightOperand> "research"`,
			)
		}
		for _, want := range required {
			if !strings.Contains(quads, want) {
				t.Errorf("missing RDF relationship %s\n%s", want, quads)
			}
		}
		for _, unwanted := range []string{"https://data.example/observations", "http://www.w3.org/ns/odrl/2/target", "application/json"} {
			if strings.Contains(quads, unwanted) {
				t.Errorf("invented or source-only claim %s", unwanted)
			}
		}
	}
}
