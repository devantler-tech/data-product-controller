package main

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

// probeTransport makes connection establishment expire before the whole HTTP request.
// A client-wide cancellation otherwise hides the TCP dial error needed by denial evidence.
func probeTransport(config *tls.Config) *http.Transport {
	return &http.Transport{
		TLSClientConfig:     config,
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSHandshakeTimeout: 3 * time.Second,
	}
}

func networkDenial(err error) bool {
	var failure *net.OpError
	var dnsFailure *net.DNSError
	return errors.As(err, &failure) && failure.Op == "dial" && failure.Net == "tcp" &&
		!errors.As(err, &dnsFailure) && failure.Timeout()
}
