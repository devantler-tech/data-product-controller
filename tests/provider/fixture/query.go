package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type document struct {
	ID    string `bson:"_id"   json:"id"`
	Value string `bson:"value" json:"value"`
}

func queryHandler(read func(context.Context) ([]document, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" {
			http.Error(w, "query parameters are unsupported", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/openapi.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(
				[]byte(
					`{"openapi":"3.1.0","info":{"title":"Document catalog","version":"1.0.0"},"paths":{"/api/documents":{"get":{"responses":{"200":{"description":"Seeded documents","content":{"application/json":{"schema":{"type":"object","required":["documents"],"properties":{"documents":{"type":"array","maxItems":1,"items":{"type":"object","required":["id","value"],"properties":{"id":{"type":"string"},"value":{"type":"string"}}}}}}}}},"503":{"description":"Source unavailable"}}}}}}`,
				),
			)
		case "/api/documents":
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			records, err := read(ctx)
			if err != nil {
				http.Error(w, "source unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(struct {
				Documents []document `json:"documents"`
			}{records})
		default:
			http.NotFound(w, r)
		}
	})
}
