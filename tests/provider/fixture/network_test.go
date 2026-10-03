package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestNetworkDenialRequiresTimeout distinguishes TCP isolation from unrelated request failures.
func TestNetworkDenialRequiresTimeout(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"timeout", &url.Error{Op: "Get", Err: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}}, true},
		{"DNS timeout", &net.DNSError{IsTimeout: true}, false},
		{"dial DNS timeout", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{IsTimeout: true}}, false},
		{"TCP read timeout", &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}, false},
		{"UDP dial timeout", &net.OpError{Op: "dial", Net: "udp", Err: context.DeadlineExceeded}, false},
		{"request deadline", context.DeadlineExceeded, false},
		{"untrusted certificate", errors.New("certificate unknown"), false},
		{"HTTP response", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkDenial(tt.err); got != tt.want {
				t.Fatalf("networkDenial = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNetworkDenialRejectsHTTPResponseTimeout exercises an established connection that stalls.
func TestNetworkDenialRejectsHTTPResponseTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	transport := probeTransport(nil)
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 20 * time.Millisecond}
	response, err := client.Get(server.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("expected a timeout while waiting for an HTTP response")
	}
	if networkDenial(err) {
		t.Fatal("an established HTTP connection was incorrectly accepted as network-policy denial")
	}
}
