package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiscoveryTraversal requires a portable continuation, exact identities and complete discovery.
func TestDiscoveryTraversal(t *testing.T) {
	for _, fault := range []string{"", "duplicate", "unsupported", "missing", "rejected", "missing-rejected"} {
		t.Run(fault, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				descriptor := func(name string) map[string]any {
					return map[string]any{
						"apiVersion":         "data-product-descriptor/v1",
						"kind":               "DataProduct",
						"namespace":          "products",
						"name":               name,
						"ready":              true,
						"generation":         1,
						"observedGeneration": 1,
						"health": map[string]any{
							"source": map[string]any{
								"state":              "not-applicable",
								"generation":         1,
								"observedGeneration": 0,
							},
							"connector": map[string]any{
								"state":              "not-applicable",
								"generation":         1,
								"observedGeneration": 0,
							},
							"contracts": map[string]any{
								"state":              "not-applicable",
								"generation":         1,
								"observedGeneration": 0,
							},
							"composition": map[string]any{
								"state":              "ready",
								"generation":         1,
								"observedGeneration": 1,
							},
						},
					}
				}
				if strings.HasPrefix(r.URL.Path, "/api/v2/products/products/") {
					_ = json.NewEncoder(w).
						Encode(descriptor(strings.TrimPrefix(r.URL.Path, "/api/v2/products/products/")))
					return
				}
				if r.URL.Query().Get("limit") != "1" ||
					r.URL.Query().Get("namespace") != "products" {
					http.Error(w, "scope missing", 400)
					return
				}
				name, token := "first", "portable"
				if r.URL.Query().Get("continue") != "" {
					name, token = "second", ""
				}
				if fault == "duplicate" {
					name = "first"
				}
				if fault == "missing" {
					token = ""
				}
				version := "data-product-discovery/v1"
				if fault == "unsupported" {
					version = "unrecognized"
				}
				page := map[string]any{
					"apiVersion": version,
					"products":   []any{descriptor(name)},
					"continue":   token,
					"rejected":   0,
				}
				if fault == "rejected" {
					page["rejected"] = 1
				}
				if fault == "missing-rejected" {
					delete(page, "rejected")
				}
				_ = json.NewEncoder(w).Encode(page)
			})
			first := httptest.NewServer(handler)
			defer first.Close()
			second := httptest.NewServer(handler)
			defer second.Close()
			err := run(
				context.Background(),
				[]string{
					"discovery",
					"--url",
					first.URL + "/api/v2/products",
					"--replica-url",
					second.URL + "/api/v2/products",
					"--namespace",
					"products",
					"--expected",
					"first,second",
				},
			)
			if (err != nil) != (fault != "") {
				t.Fatalf("fault=%q result=%v", fault, err)
			}
		})
	}
}
