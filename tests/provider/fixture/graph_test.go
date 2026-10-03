package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGraphClient exercises authenticated, bounded HTTP exchange rather than matching log text.
func TestGraphClient(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		wantError      bool
	}{
		{"query results", `{"error":false,"result":[{"id":"persistent-lineage-middle","depth":1},{"id":"persistent-lineage-target","depth":2}],"hasMore":false}`, 201, false},
		{"authorization denied", `{"error":true,"errorNum":11,"errorMessage":"sensitive-password"}`, 403, true},
		{"authentication denied", `{"error":true,"errorNum":11}`, 401, true},
		{"redirect", `{}`, 302, true},
		{"unexpected success error", `{"error":true,"errorNum":11}`, 200, true},
		{"malformed JSON", `broken`, 200, true},
		{"oversized body", `{"value":"` + strings.Repeat("x", 65536) + `"}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					user, password, ok := r.BasicAuth()
					if !ok || user != "catalog-reader" || password != "synthetic-reader-original" {
						t.Error("dedicated reader authentication missing")
					}
					if r.Method != http.MethodPost || r.URL.Path != "/_db/catalog/_api/cursor" {
						t.Error("query escaped its fixed database API")
					}
					var query struct {
						Query string `json:"query"`
					}
					if json.NewDecoder(r.Body).Decode(&query) != nil ||
						query.Query != "fixed traversal" {
						t.Error("wrong query body")
					}
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.response))
				}),
			)
			defer server.Close()
			client := &graphClient{
				endpoint: server.URL,
				http:     server.Client(),
				user:     "catalog-reader",
				password: "synthetic-reader-original",
			}
			var out graphCursor
			err := client.request(
				t.Context(),
				"POST",
				"/_db/catalog/_api/cursor",
				map[string]string{"query": "fixed traversal"},
				&out,
			)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "sensitive-password") {
				t.Fatal("database detail leaked")
			}
			if !tc.wantError && (len(out.Result) != 2 || out.HasMore || out.Result[1].Depth != 2) {
				t.Fatal("traversal result lost")
			}
		})
	}
}

// TestGraphAuthorizationDenial refuses success, outages and invalid credentials as write-denial proof.
func TestGraphAuthorizationDenial(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		want         bool
	}{
		{403, 11, true}, {401, 11, false}, {503, 11, false}, {403, 1004, true}, {403, 0, false}, {200, 11, false},
	} {
		if got := graphWriteDenied(
			&graphAPIError{status: tc.status, code: tc.code},
		); got != tc.want {
			t.Errorf("status=%d code=%d: denial=%v", tc.status, tc.code, got)
		}
	}
	if graphWriteDenied(nil) || graphWriteDenied(errors.New("network timeout")) {
		t.Fatal("incomplete evidence accepted")
	}
}

// TestGraphServerMode rejects unknown, read-only and failed mode observations before grant evidence.
func TestGraphServerMode(t *testing.T) {
	for _, mode := range []string{"default", "readonly", "", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet ||
						r.URL.Path != "/_db/catalog/_admin/server/mode" {
						t.Error("wrong server mode observation")
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"mode": mode})
				}),
			)
			defer server.Close()
			client := &graphClient{
				endpoint: server.URL,
				http:     server.Client(),
				user:     "reader",
				password: "synthetic",
			}
			if (graphServerWritable(t.Context(), client) == nil) != (mode == "default") {
				t.Fatal("incorrect writable mode evidence")
			}
		})
	}
}

// TestGraphQueryHandler verifies the published fixed GET and keeps source failures out of responses.
func TestGraphQueryHandler(t *testing.T) {
	for _, tc := range []struct {
		method, path  string
		sourceError   bool
		status, reads int
	}{
		{"GET", "/api/lineage", false, 200, 1},
		{"GET", "/api/lineage", true, 503, 1},
		{"POST", "/api/lineage", false, 405, 0},
		{"GET", "/api/lineage?query=write", false, 400, 0},
		{"GET", "/unknown", false, 404, 0},
		{"GET", "/healthz", false, 200, 0},
		{"GET", "/openapi.json", false, 200, 0},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			reads := 0
			handler := graphQueryHandler(func(ctx context.Context) ([]lineageNode, error) {
				reads++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("query has no deadline")
				}
				if tc.sourceError {
					return nil, errors.New("sensitive-password")
				}
				return []lineageNode{
					{ID: "persistent-lineage-middle", Depth: 1},
					{ID: "persistent-lineage-target", Depth: 2},
				}, nil
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status || reads != tc.reads {
				t.Fatalf("status=%d reads=%d", response.Code, reads)
			}
			if strings.Contains(response.Body.String(), "sensitive-password") {
				t.Fatal("source error leaked")
			}
			if tc.path == "/api/lineage" && !tc.sourceError && tc.method == "GET" {
				if response.Header().Get("Cache-Control") != "no-store" ||
					!strings.Contains(response.Body.String(), `"depth":2`) {
					t.Fatal("published traversal missing")
				}
			}
			if tc.path == "/openapi.json" &&
				!strings.Contains(response.Body.String(), `"/api/lineage"`) {
				t.Fatal("published contract missing")
			}
		})
	}
}
