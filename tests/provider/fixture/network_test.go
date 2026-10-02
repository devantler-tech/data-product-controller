package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
)

func TestNetworkDenialRequiresTimeout(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"timeout", &url.Error{Op: "Get", Err: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}}, true},
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
