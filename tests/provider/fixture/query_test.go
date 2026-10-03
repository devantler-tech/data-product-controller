package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPublishedDocumentQuery checks the fixed response and sanitized unavailable-source behavior.
func TestPublishedDocumentQuery(t *testing.T) {
	for _, tt := range []struct {
		name, method, path string
		failure            error
		status             int
	}{
		{"read", "GET", "/api/documents", nil, http.StatusOK},
		{"outage", "GET", "/api/documents", errors.New("private-password-and-record"), http.StatusServiceUnavailable},
		{"insert", "POST", "/api/documents", nil, http.StatusMethodNotAllowed},
		{"arbitrary query", "GET", "/api/documents?query=all", nil, http.StatusBadRequest},
		{"unknown route", "GET", "/other", nil, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			handler := queryHandler(func(ctx context.Context) ([]document, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("database query has no five-second deadline")
				}
				return []document{{ID: "retained", Value: "persistent-document"}}, tt.failure
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d", response.Code, tt.status)
			}
			body := response.Body.String()
			if strings.Contains(body, "private-password-and-record") {
				t.Fatal("backend details escaped through the contract")
			}
			if tt.status == http.StatusOK &&
				body != "{\"documents\":[{\"id\":\"retained\",\"value\":\"persistent-document\"}]}\n" {
				t.Fatalf("unexpected published records: %s", body)
			}
			if tt.status >= 400 && calls != 0 && tt.failure == nil {
				t.Fatal("invalid request reached the database")
			}
		})
	}
}

// TestPublishedDocumentContract checks the published query schema without reading the database.
func TestPublishedDocumentContract(t *testing.T) {
	handler := queryHandler(func(context.Context) ([]document, error) {
		t.Fatal("contract fetch queried the database")
		return nil, nil
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/openapi.json", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"/api/documents"`) ||
		!strings.Contains(response.Body.String(), `"openapi":"3.1.0"`) {
		t.Fatalf("missing standard query contract: %d %s", response.Code, response.Body.String())
	}
}
