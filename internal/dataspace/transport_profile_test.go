package dataspace_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

// TestConnectorServicePortProfile exercises the exported provider base, not only URL parsing.
func TestConnectorServicePortProfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		endpoint string
		valid    bool
	}{
		{"https://connector.example:0/dsp", false},
		{"https://connector.example:65536/dsp", false},
		{"https://connector.example:/dsp", false},
		{"https://connector.example:1/dsp", true},
		{"https://connector.example:443/dsp", true},
		{"https://connector.example:65535/dsp", true},
		{"https://[2001:db8::1]:443/dsp", true},
		{"https://connector.example/path%5B1%5D", true},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			t.Parallel()
			bindings := bytes.ReplaceAll(example(t, "bindings"),
				[]byte("https://connector.example/dsp"), []byte(tc.endpoint))
			output, err := dataspace.Export(
				bytes.NewReader(example(t, "catalog")),
				bytes.NewReader(bindings),
			)
			if (err == nil) != tc.valid || (!tc.valid && len(output) != 0) {
				t.Fatalf("valid=%v error=%v output bytes=%d", tc.valid, err, len(output))
			}
		})
	}
}

// TestResourceIdentityByteProfile keeps source and provider identifiers inside the shared DCAT profile.
func TestResourceIdentityByteProfile(t *testing.T) {
	t.Parallel()
	for _, original := range []string{
		"urn:example:source-catalog", "urn:example:harbour", "urn:example:observations",
		"urn:example:query-service", "urn:example:dsp-catalog", "urn:example:provider",
		"urn:example:dsp-service", "urn:example:harbour-offer", "urn:example:dsp-observations",
	} {
		t.Run(original, func(t *testing.T) {
			t.Parallel()
			for _, prefix := range []string{"urn:example:", "https://identity.example/"} {
				for _, length := range []int{2048, 2049} {
					identity := prefix + strings.Repeat("x", length-len(prefix))
					catalog := bytes.ReplaceAll(
						example(t, "catalog"),
						[]byte(`"`+original+`"`),
						[]byte(`"`+identity+`"`),
					)
					bindings := bytes.ReplaceAll(
						example(t, "bindings"),
						[]byte(`"`+original+`"`),
						[]byte(`"`+identity+`"`),
					)
					output, err := dataspace.Export(
						bytes.NewReader(catalog),
						bytes.NewReader(bindings),
					)
					if (err == nil) != (length == 2048) || (err != nil && len(output) != 0) {
						t.Fatalf(
							"identity length=%d prefix=%s error=%v output bytes=%d",
							length,
							prefix,
							err,
							len(output),
						)
					}
				}
			}
		})
	}
}
