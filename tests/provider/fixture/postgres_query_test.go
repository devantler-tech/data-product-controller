package main

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestPostgresQueryContract protects fixed routes, bounded reads and sanitized failures for each model.
func TestPostgresQueryContract(t *testing.T) {
	for _, model := range []struct{ name, path, field string }{
		{"sql", "/api/rows", "rows"}, {"document", "/api/documents", "documents"}, {"graph", "/api/lineage", "lineage"},
	} {
		for _, tc := range []struct {
			method, suffix string
			fail           bool
			status, reads  int
		}{
			{"GET", "", false, 200, 1}, {"GET", "", true, 503, 1}, {"POST", "", false, 405, 0}, {"GET", "?query=write", false, 400, 0},
		} {
			t.Run(model.name+tc.method+tc.suffix+fmt.Sprint(tc.fail), func(t *testing.T) {
				calls := 0
				handler := postgresQueryHandler(model.name, func(ctx context.Context) ([]postgresRecord, error) {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 5*time.Second {
						t.Error("PostgreSQL read has no five-second deadline")
					}
					if tc.fail {
						return nil, errors.New("credential-and-record-sentinel")
					}
					return []postgresRecord{{ID: "retained", Value: "persistent-row"}}, nil
				})
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(tc.method, model.path+tc.suffix, nil))
				if response.Code != tc.status || calls != tc.reads {
					t.Fatalf("status=%d reads=%d, want %d/%d", response.Code, calls, tc.status, tc.reads)
				}
				if strings.Contains(response.Body.String(), "credential-and-record-sentinel") {
					t.Fatal("backend failure escaped through the query contract")
				}
				if tc.status == 200 && (!strings.Contains(response.Body.String(), `"`+model.field+`":`) || response.Header().Get("Cache-Control") != "no-store") {
					t.Fatal("declared response model or cache policy lost")
				}
			})
		}
		handler := postgresQueryHandler(model.name, func(context.Context) ([]postgresRecord, error) {
			t.Fatal("contract request read the database")
			return nil, nil
		})
		for _, path := range []string{"/openapi.json", "/healthz", "/unpublished"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			want := 200
			if path == "/unpublished" {
				want = 404
			}
			if response.Code != want {
				t.Fatalf("%s=%d, want %d", path, response.Code, want)
			}
			if path == "/openapi.json" && (!strings.Contains(response.Body.String(), `"openapi":"3.1.0"`) || !strings.Contains(response.Body.String(), `"`+model.path+`"`)) {
				t.Fatal("standard published query contract missing")
			}
		}
	}
}

// TestPostgresDenials requires PostgreSQL's authorization/authentication codes, never transport or unrelated failures.
func TestPostgresDenials(t *testing.T) {
	for _, tc := range []struct {
		code        string
		write, auth bool
	}{
		{"42501", true, false}, {"28P01", false, true}, {"25006", false, false}, {"08006", false, false}, {"57014", false, false}, {"42P01", false, false},
	} {
		err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: tc.code, Message: "private-payload"})
		if postgresWriteDenied(err) != tc.write || postgresAuthenticationDenied(err) != tc.auth {
			t.Errorf("incorrect denial classification for %s", tc.code)
		}
	}
	for _, err := range []error{nil, context.DeadlineExceeded, errors.New("private-payload")} {
		if postgresWriteDenied(err) || postgresAuthenticationDenied(err) {
			t.Fatal("incomplete PostgreSQL evidence counted as denial")
		}
	}
}
