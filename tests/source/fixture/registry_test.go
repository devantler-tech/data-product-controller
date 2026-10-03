package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeSelectsExactRegistryProduct(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantError bool
	}{
		{"exact", `{"products":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"Ready"}}]}`, false},
		{"distinct Unicode metadata", `{"products":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"Ready"},"metadata":{"résumé":"value","元":"value"}}]}`, false},
		{"other ready product", `{"products":[{"namespace":"products","name":"other","ready":true},{"namespace":"products","name":"existing-export","ready":false}]}`, true},
		{"wrong namespace", `{"products":[{"namespace":"other","name":"existing-export","ready":true}]}`, true},
		{"missing boolean", `{"products":[{"namespace":"products","name":"existing-export"}]}`, true},
		{"duplicate identity", `{"products":[{"namespace":"products","name":"existing-export","ready":true},{"namespace":"products","name":"existing-export","ready":true}]}`, true},
		{"wrong reason", `{"products":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"OldReady"}}]}`, true},
		{"malformed", `{"products":`, true},
		{"trailing document", `{"products":[]} {"ready":true}`, true},
		{"duplicate inventory", `{"products":[],"products":[{"namespace":"products","name":"existing-export","ready":true}]}`, true},
		{"duplicate readiness", `{"products":[{"namespace":"products","name":"existing-export","ready":false,"ready":true}]}`, true},
		{"case variant readiness", `{"products":[{"namespace":"products","name":"existing-export","ready":false,"Ready":true}]}`, true},
		{"Unicode folded inventory", `{"products":[{"namespace":"products","name":"existing-export","ready":false}],"product\u017f":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"Ready"}}]}`, true},
		{"Unicode folded namespace", `{"products":[{"namespace":"other","name\u017fpace":"products","name":"existing-export","ready":true,"readiness":{"reason":"Ready"}}]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, test.body)
				}),
			)
			t.Cleanup(server.Close)
			err := probe(
				t.Context(),
				[]string{
					"--url",
					server.URL,
					"--registry-product",
					"products/existing-export",
					"--registry-ready",
					"true",
					"--registry-reason",
					"Ready",
				},
			)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

// TestRegistryJSONRetainsBounds verifies syntax and nesting checks around Unicode folding.
func TestRegistryJSONRetainsBounds(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantError bool
	}{
		{"maximum depth", strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64), false},
		{"excessive depth", strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), true},
		{"exact duplicate", `{"field":false,"field":true}`, true},
		{"ASCII folded duplicate", `{"field":false,"FIELD":true}`, true},
		{"trailing document", `{} {}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := registryJSON([]byte(test.body))
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestProbeRegistryOptionsFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(
			w,
			`{"products":[{"namespace":"products","name":"existing-export","ready":false}]}`,
		)
	}))
	t.Cleanup(server.Close)
	for _, args := range [][]string{
		{"--registry-product", "products/existing-export"},
		{"--registry-ready", "false"},
		{"--registry-reason", "Ready"},
		{"--registry-product", "products/existing-export", "--registry-ready", "unknown"},
		{"--registry-product", "products/existing-export/extra", "--registry-ready", "false"},
		{"--registry-product", "products/existing-export", "--registry-ready", "false", "--want-error"},
	} {
		if err := probe(t.Context(), append([]string{"--url", server.URL}, args...)); err == nil {
			t.Fatalf("accepted invalid registry options %v", args)
		}
	}
	if err := probe(
		t.Context(),
		[]string{
			"--url",
			server.URL,
			"--registry-product",
			"products/existing-export",
			"--registry-ready",
			"false",
		},
	); err != nil {
		t.Fatal(err)
	}
}
