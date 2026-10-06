package dataspace_test

import (
	"bytes"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

// TestRound13SourceServiceGraph rejects incomplete, orphaned and shared service associations.
func TestRound13SourceServiceGraph(t *testing.T) {
	for _, which := range []string{"missing title", "empty title", "different title", "unreferenced service", "shared output service"} {
		t.Run(which, func(t *testing.T) {
			source := object(t, example(t, "catalog"))
			services, ok := source["dcat:service"].([]any)
			if !ok || len(services) == 0 {
				t.Fatal("fixture has no services")
			}
			s := firstObject(t, services)
			switch which {
			case "missing title":
				delete(s, "dcterms:title")
			case "empty title":
				s["dcterms:title"] = ""
			case "different title":
				s["dcterms:title"] = "other-output"
			case "unreferenced service":
				extra := object(t, encode(t, s))
				extra["@id"] = "urn:example:orphan-service"
				source["dcat:service"] = append(services, extra)
			case "shared output service":
				datasets, ok := source["dcat:dataset"].([]any)
				if !ok {
					t.Fatal("fixture has no datasets")
				}
				d := firstObject(t, datasets)
				distributions, ok := d["dcat:distribution"].([]any)
				if !ok {
					t.Fatal("fixture has no distributions")
				}
				extra := object(t, encode(t, distributions[0]))
				extra["@id"] = "urn:example:second-distribution"
				d["dcat:distribution"] = append(distributions, extra)
			}
			b, err := dataspace.Export(
				bytes.NewReader(encode(t, source)),
				bytes.NewReader(example(t, "bindings")),
			)
			if err == nil || len(b) != 0 {
				t.Errorf("incomplete source graph accepted: output=%d bytes error=%v", len(b), err)
			}
		})
	}
	b, err := dataspace.Export(
		bytes.NewReader(example(t, "catalog")),
		bytes.NewReader(example(t, "bindings")),
	)
	if err != nil || len(b) == 0 {
		t.Fatalf("valid source rejected: %v", err)
	}
}
