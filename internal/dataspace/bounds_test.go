package dataspace_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

func TestInvalidSourceAndJSON(t *testing.T) {
	t.Parallel()
	base := example(t, "catalog")
	cases := map[string][]byte{
		"remapped context": bytes.ReplaceAll(
			base,
			[]byte("http://www.w3.org/ns/dcat#"),
			[]byte("https://evil.example/"),
		),
		"multiple values": append(bytes.Clone(base), []byte(` {}`)...),
		"invalid UTF8":    append(bytes.Clone(base), 0xff),
		"depth":           []byte(strings.Repeat("[", 34) + strings.Repeat("]", 34)),
		"null":            []byte("null"),
		"duplicate field": bytes.Replace(
			base,
			[]byte(`"@type": "dcat:Catalog"`),
			[]byte(`"@type":"dcat:Catalog","@type":"dcat:Catalog"`),
			1,
		),
		"lossy escaped Unicode": bytes.ReplaceAll(
			base,
			[]byte("Harbour observations"),
			[]byte(`Harbour\ud800`),
		),
		"nested remapping": bytes.ReplaceAll(
			base,
			[]byte(`"dcterms:title": "Harbour observations",`),
			[]byte(`"@context":{"dcat":"https://evil.example/"},"dcterms:title":"Harbour",`),
		),
		"case alias": bytes.ReplaceAll(
			base,
			[]byte("dcterms:title"),
			[]byte("dcterms:Title"),
		),
		"field size": bytes.ReplaceAll(
			base,
			[]byte("Harbour observations"),
			[]byte(strings.Repeat("x", (16<<10)+1)),
		),
		"bytes": []byte(strings.Repeat(" ", (2<<20)+1)),
		"source identity conflict": bytes.ReplaceAll(
			base,
			[]byte("urn:example:query-service"),
			[]byte("urn:example:harbour"),
		),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, err := dataspace.Export(
				bytes.NewReader(input),
				bytes.NewReader(example(t, "bindings")),
			)
			if err == nil || len(b) > 0 {
				t.Fatalf("invalid input published: %v", err)
			}
		})
	}
}

func TestRejectsNonIRICharacters(t *testing.T) {
	t.Parallel()
	for _, char := range []string{"{", "}", "<", ">", "|", "^", "`", "[", "]", "\u00a0", "\u0085", "\u2003"} {
		t.Run(fmt.Sprintf("%U", []rune(char)[0]), func(t *testing.T) {
			t.Parallel()
			for _, old := range []string{"https://connector.example/dsp", "https://example.com/transfer/harbour-pull", "urn:example:harbour-offer"} {
				input := bytes.ReplaceAll(
					example(t, "bindings"),
					[]byte(old),
					[]byte("https://example.com/term"+char),
				)
				b, err := dataspace.Export(
					bytes.NewReader(example(t, "catalog")),
					bytes.NewReader(input),
				)
				if err == nil || len(b) > 0 {
					t.Errorf("invalid IRI accepted for %s", old)
				}
			}
		})
	}
}

func TestAcceptsEncodedPathAndIPv6(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"https://[2001:db8::1]/dsp", "https://example.com/path%5B1%5D"} {
		input := bytes.ReplaceAll(
			example(t, "bindings"),
			[]byte("https://connector.example/dsp"),
			[]byte(endpoint),
		)
		if _, err := dataspace.Export(
			bytes.NewReader(example(t, "catalog")),
			bytes.NewReader(input),
		); err != nil {
			t.Fatalf("valid public endpoint rejected: %v", err)
		}
	}
}

func TestBindingPolicyLimits(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, map[string]any){
		"null rules":  func(_ *testing.T, o map[string]any) { o["prohibition"] = nil },
		"empty rules": func(_ *testing.T, o map[string]any) { o["prohibition"] = []any{} },
		"too many rules": func(_ *testing.T, o map[string]any) {
			var rules []any
			for range 33 {
				rules = append(rules, map[string]any{"action": "use"})
			}
			o["prohibition"] = rules
		},
		"empty constraint": func(t *testing.T, o map[string]any) {
			t.Helper()
			firstObject(t, firstObject(t, o["permission"])["constraint"])["rightOperand"] = ""
		},
		"numeric constraint": func(t *testing.T, o map[string]any) {
			t.Helper()
			firstObject(t, firstObject(t, o["permission"])["constraint"])["rightOperand"] = 123
		},
		"ambiguous operator": func(t *testing.T, o map[string]any) {
			t.Helper()
			firstObject(t, firstObject(t, o["permission"])["constraint"])["operator"] = "unknown"
		},
		"obligation alone": func(_ *testing.T, o map[string]any) {
			o["obligation"] = o["permission"]
			delete(o, "permission")
			delete(o, "prohibition")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := object(t, example(t, "bindings"))
			mutate(t, firstObject(t, firstObject(t, input["datasets"])["offers"]))
			b, err := dataspace.Export(
				bytes.NewReader(example(t, "catalog")),
				bytes.NewReader(encode(t, input)),
			)
			if err == nil || len(b) > 0 {
				t.Fatalf("unsafe policy published: %v", err)
			}
		})
	}
}

func FuzzExportNeverReturnsPartialJSON(f *testing.F) {
	f.Add([]byte(`{}`), []byte(`{}`))
	f.Fuzz(func(t *testing.T, catalog, bindings []byte) {
		b, err := dataspace.Export(bytes.NewReader(catalog), bytes.NewReader(bindings))
		if err != nil && len(b) > 0 {
			t.Fatal("error returned partial catalog")
		}
	})
}
