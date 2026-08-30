package demoproduct_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
)

func TestHandlerPublishesPortableContractAndProductUI(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		path        string
		contentType string
		contains    string
	}{
		{
			name:        "OpenAPI contract",
			path:        "/openapi.json",
			contentType: "application/json",
			contains:    `"title":"Harbour observations"`,
		},
		{
			name:        "decentralized product UI",
			path:        "/ui",
			contentType: "text/html; charset=utf-8",
			contains:    "Explore harbour observations",
		},
	}

	handler := demoproduct.NewHandler()
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, testCase.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", testCase.path, response.Code, http.StatusOK)
			}
			if got := response.Header().Get("Content-Type"); got != testCase.contentType {
				t.Errorf("Content-Type = %q, want %q", got, testCase.contentType)
			}
			if !strings.Contains(response.Body.String(), testCase.contains) {
				t.Errorf("GET %s body does not contain %q", testCase.path, testCase.contains)
			}
		})
	}
}

func TestHandlerQueriesProductData(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/observations?station=nordhavn", nil)
	response := httptest.NewRecorder()
	demoproduct.NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var body struct {
		Items []struct {
			Station string `json:"station"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Station != "nordhavn" {
		t.Fatalf("items = %#v, want only nordhavn", body.Items)
	}
}

func TestHandlerAllowsSandboxedProductUIToReadPublicData(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/observations", nil)
	request.Header.Set("Origin", "null")
	response := httptest.NewRecorder()
	demoproduct.NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want no credential sharing", got)
	}
}

func TestHandlerRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/observations", nil)
	response := httptest.NewRecorder()
	demoproduct.NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
