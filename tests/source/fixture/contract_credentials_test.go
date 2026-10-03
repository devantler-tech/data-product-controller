package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestContractPublicationRejectsCredentialHeaders ensures real probe acceptance cannot hide credential forwarding.
func TestContractPublicationRejectsCredentialHeaders(t *testing.T) {
	fixture := &source{}
	server := httptest.NewServer(http.HandlerFunc(fixture.contract))
	t.Cleanup(server.Close)
	client := newClient(time.Second)
	t.Cleanup(client.CloseIdleConnections)
	for _, tc := range []struct {
		name, header, value string
		want                int
	}{
		{"credential free", "", "", http.StatusOK},
		{"authorization", "Authorization", "Bearer fixture-token-a", http.StatusBadRequest},
		{"empty authorization", "Authorization", "", http.StatusBadRequest},
		{"cookie", "Cookie", "session=fixture-session", http.StatusBadRequest},
		{"empty cookie", "Cookie", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.header != "" {
				request.Header.Set(tc.header, tc.value)
			}
			// #nosec G704 -- URL belongs to this test's disposable loopback server; requests have no redirects/proxies and a one-second timeout.
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want {
				t.Fatalf("contract status=%d, want=%d", response.StatusCode, tc.want)
			}
			if tc.want == http.StatusOK && !strings.Contains(string(body), `"openapi":"3.1.0"`) {
				t.Fatal("credential-free request did not receive the contract")
			}
			if tc.want != http.StatusOK && len(body) != 0 {
				t.Fatal("credential rejection exposed response details")
			}
		})
	}
}
