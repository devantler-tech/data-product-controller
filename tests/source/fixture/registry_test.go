package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeSelectsExactRegistryProduct(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantError bool
	}{
		{"exact", `{"products":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"Ready"}}]}`, false},
		{"other ready product", `{"products":[{"namespace":"products","name":"other","ready":true},{"namespace":"products","name":"existing-export","ready":false}]}`, true},
		{"wrong namespace", `{"products":[{"namespace":"other","name":"existing-export","ready":true}]}`, true},
		{"missing boolean", `{"products":[{"namespace":"products","name":"existing-export"}]}`, true},
		{"duplicate identity", `{"products":[{"namespace":"products","name":"existing-export","ready":true},{"namespace":"products","name":"existing-export","ready":true}]}`, true},
		{"wrong reason", `{"products":[{"namespace":"products","name":"existing-export","ready":true,"readiness":{"reason":"OldReady"}}]}`, true},
		{"malformed", `{"products":`, true},
		{"trailing document", `{"products":[]} {"ready":true}`, true},
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
