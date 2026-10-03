package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestProbeRequiresExactRegistryAbsence rejects malformed inventories and decoys that could hide the selected product.
func TestProbeRequiresExactRegistryAbsence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		accepted   bool
	}{
		{"empty registry", `{"products":[]}`, true},
		{"other products", `{"products":[{"namespace":"products","name":"other"}]}`, true},
		{"still present", `{"products":[{"namespace":"products","name":"existing-export","ready":false}]}`, false},
		{"nested empty decoy", `{"metadata":{"products":[]},"products":[{"namespace":"products","name":"existing-export"}]}`, false},
		{"missing inventory", `{}`, false},
		{"null inventory", `{"products":null}`, false},
		{"ambiguous documents", `{"products":[]} {"products":[]}`, false},
		{"duplicate inventory key", `{"products":[{"namespace":"products","name":"existing-export"}],"products":[]}`, false},
		{"incomplete identity", `{"products":[{}]}`, false},
		{"null product", `{"products":[null]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, tc.body)
				}),
			)
			t.Cleanup(server.Close)
			err := probe(
				t.Context(),
				[]string{"--url", server.URL, "--registry-absent", "products/existing-export"},
			)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted = %v, want %v: %v", err == nil, tc.accepted, err)
			}
		})
	}
}
