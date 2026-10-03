package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestMetricsRequiresFreshCompletedObservation catches success inferred from an old readiness gauge.
func TestMetricsRequiresFreshCompletedObservation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"fresh", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 101\n", false},
		{"stale", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 99\n", true},
		{"boundary", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 100\n", true},
		{"wrong readiness", "http_source_ready 0\nhttp_source_last_observation_timestamp_seconds 101\n", true},
		{"missing timestamp", "http_source_ready 1\n", true},
		{"duplicate readiness", "http_source_ready 1\nhttp_source_ready 0\nhttp_source_last_observation_timestamp_seconds 101\n", true},
		{"duplicate timestamp", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 99\nhttp_source_last_observation_timestamp_seconds 101\n", true},
		{"labeled replacement", "http_source_ready{source=\"other\"} 1\nhttp_source_last_observation_timestamp_seconds 101\n", true},
		{"nonfinite", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds NaN\n", true},
		{"future", "http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 9999999999\n", true},
		{"trailing timestamp", "http_source_ready 1 101\nhttp_source_last_observation_timestamp_seconds 101\n", true},
		{"oversized", strings.Repeat("# comment\n", 32769), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, tc.body)
				}),
			)
			t.Cleanup(server.Close)
			err := metrics(
				t.Context(),
				[]string{
					"--url",
					server.URL,
					"--kind",
					"http-source",
					"--ready",
					"1",
					"--since",
					"100",
				},
			)
			if (err != nil) != tc.wantError {
				t.Fatalf("completed observation accepted=%v, want=%v", err == nil, !tc.wantError)
			}
		})
	}
}

// TestContractMetricsSelectsItsOwnSeries prevents another workload's metrics from proving contract health.
func TestContractMetricsSelectsItsOwnSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(
			w,
			"http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 200\ncontract_probe_ready 0\ncontract_probe_last_observation_timestamp_seconds 101\n",
		)
	}))
	t.Cleanup(server.Close)
	args := []string{
		"--url",
		server.URL,
		"--kind",
		"contract-probe",
		"--ready",
		"0",
		"--since",
		"100",
	}
	if err := metrics(t.Context(), args); err != nil {
		t.Fatalf("fresh contract outage was rejected: %v", err)
	}
	args[5] = "1"
	if err := metrics(t.Context(), args); err == nil {
		t.Fatal("healthy HTTP source concealed the failed contract")
	}
}

// TestMetricsRejectsInvalidConfigurationWithoutRequests catches credential/query forwarding before any fetch.
func TestMetricsRejectsInvalidConfigurationWithoutRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	for _, args := range [][]string{
		{"--url", server.URL + "?token=private", "--kind", "http-source", "--ready", "1", "--since", "100"},
		{"--url", server.URL, "--kind", "unknown", "--ready", "1", "--since", "100"},
		{"--url", server.URL, "--kind", "http-source", "--ready", "2", "--since", "100"},
		{"--url", server.URL, "--kind", "http-source", "--ready", "1", "--since", "0"},
		{"--url", server.URL, "--kind", "http-source", "--ready", "1", "--since", "100", "--timeout", "0s"},
	} {
		if err := metrics(t.Context(), args); err == nil {
			t.Fatal("invalid observation configuration was accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid configuration reached the metrics server")
	}
}

// TestMetricsRefusesRedirectsAndUntrustedTLS preserves the independent observer's transport boundary.
func TestMetricsRefusesRedirectsAndUntrustedTLS(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			redirected.Add(1)
			_, _ = io.WriteString(
				w,
				"http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 101\n",
			)
			return
		}
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	args := []string{"--url", server.URL, "--kind", "http-source", "--ready", "1", "--since", "100"}
	if err := metrics(t.Context(), args); err == nil || redirected.Load() != 0 {
		t.Fatal("redirect established metrics acceptance")
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(
			w,
			"http_source_ready 1\nhttp_source_last_observation_timestamp_seconds 101\n",
		)
	}))
	t.Cleanup(tls.Close)
	args[1] = tls.URL
	if err := metrics(t.Context(), args); err == nil {
		t.Fatal("untrusted TLS established metrics acceptance")
	}
}
