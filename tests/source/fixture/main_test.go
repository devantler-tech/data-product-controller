package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceOutageAndCredentialRotation(t *testing.T) {
	source := &source{}
	for _, step := range []struct {
		mode  string
		token string
		want  int
	}{
		{"", "fixture-token-a", 200},
		{"", "fixture-token-b", 401},
		{"down", "fixture-token-a", 503},
		{"rotated", "fixture-token-a", 401},
		{"", "fixture-token-b", 200},
		{"healthy", "fixture-token-a", 200},
	} {
		if step.mode != "" {
			control := httptest.NewRecorder()
			source.control(control, httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/control/"+step.mode, nil,
			))
			if control.Code != http.StatusNoContent {
				t.Fatalf("control status = %d", control.Code)
			}
		}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/export", nil)
		request.Header.Set("Authorization", "Bearer "+step.token)
		response := httptest.NewRecorder()
		source.export(response, request)
		if response.Code != step.want {
			t.Fatalf("export status = %d, want %d", response.Code, step.want)
		}
		if step.want == http.StatusOK && response.Body.String() != `{"fixture":"source"}` {
			t.Fatal("unexpected synthetic export")
		}
	}
}

func TestProbeRejectsHTTPFailuresAsNetworkDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	if err := probe(t.Context(), []string{"--url", server.URL, "--want-error"}); err == nil {
		t.Fatal("an HTTP response must not prove a network denial")
	}
	server.Close()
	if err := probe(t.Context(), []string{"--url", server.URL, "--want-error"}); err != nil {
		t.Fatalf("closed listener did not satisfy transport failure: %v", err)
	}
}

func TestProbeChecksResponseAndBounds(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		args      []string
		wantError bool
	}{
		{"match", `{"fixture":"source"}`, []string{"--contains", `"fixture":"source"`}, false},
		{"mismatch", `{"fixture":"source"}`, []string{"--contains", "missing"}, true},
		{"oversized", strings.Repeat("x", (1<<20)+1), nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, test.body)
				}),
			)
			t.Cleanup(server.Close)
			args := append([]string{"--url", server.URL, "--want-status", "200"}, test.args...)
			if err := probe(t.Context(), args); (err != nil) != test.wantError {
				t.Fatalf("probe error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestProbeRejectsInvalidURLAsNetworkDenial(t *testing.T) {
	if err := probe(
		t.Context(),
		[]string{"--url", "://invalid", "--want-error"},
	); err == nil {
		t.Fatal("invalid configuration must not prove a network denial")
	}
}

func TestCancelledProbeDoesNotProveNetworkDenial(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := probe(ctx, []string{"--url", "http://127.0.0.1:1", "--want-error"}); err == nil {
		t.Fatal("a canceled probe must not prove a network denial")
	}
}

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/target")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	if err := probe(t.Context(), []string{
		"--url", server.URL + "/redirect", "--want-status", "302",
	}); err != nil {
		t.Fatalf("probe did not preserve the redirect response: %v", err)
	}
}
