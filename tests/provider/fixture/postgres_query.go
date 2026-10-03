package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type postgresRecord struct {
	ID    string `json:"id"`
	Value string `json:"value,omitempty"`
	Depth int    `json:"depth,omitempty"`
}

// postgresQueryHandler exposes only one fixed read contract for its declared model.
func postgresQueryHandler(model string, read func(context.Context) ([]postgresRecord, error)) http.Handler {
	path, field := postgresQueryRoute(model)
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
		if path == "" {
			http.Error(w, "source unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/openapi.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(postgresOpenAPI(model, path, field))
		case path:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			records, err := read(ctx)
			if err != nil || len(records) > 2 {
				http.Error(w, "source unavailable", http.StatusServiceUnavailable)
				return
			}
			for _, record := range records {
				if len(record.ID) > 128 || len(record.Value) > 128 {
					http.Error(w, "source unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{field: records})
		default:
			http.NotFound(w, r)
		}
	})
}

// postgresQueryRoute binds a model to one fixed route and response field.
func postgresQueryRoute(model string) (string, string) {
	switch model {
	case "sql":
		return "/api/rows", "rows"
	case "document":
		return "/api/documents", "documents"
	case "graph":
		return "/api/lineage", "lineage"
	default:
		return "", ""
	}
}

// postgresOpenAPI describes the fixed bounded read without exposing database configuration.
func postgresOpenAPI(model, path, field string) map[string]any {
	properties := map[string]any{"id": map[string]any{"type": "string", "maxLength": 128}}
	required := []string{"id", "value"}
	if model == "graph" {
		properties["depth"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 2}
		required = []string{"id", "depth"}
	} else {
		properties["value"] = map[string]any{"type": "string", "maxLength": 128}
	}
	return map[string]any{
		"openapi": "3.1.0", "info": map[string]string{"title": "PostgreSQL " + model + " catalog", "version": "1.0.0"},
		"paths": map[string]any{path: map[string]any{"get": map[string]any{"responses": map[string]any{
			"200": map[string]any{"description": "Retained catalog records", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
				"type": "object", "required": []string{field}, "properties": map[string]any{field: map[string]any{
					"type": "array", "maxItems": 2, "items": map[string]any{"type": "object", "required": required, "properties": properties},
				}},
			}}}},
			"503": map[string]string{"description": "Source unavailable"},
		}}}},
	}
}

// postgresWriteDenied distinguishes an effective privilege denial from read-only mode, outage or syntax errors.
func postgresWriteDenied(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && failure.Code == "42501"
}

// postgresAuthenticationDenied accepts only PostgreSQL's invalid-password result for stale credentials.
func postgresAuthenticationDenied(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && failure.Code == "28P01"
}
