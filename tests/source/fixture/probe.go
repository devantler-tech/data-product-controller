package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            (&net.Dialer{Timeout: timeout}).DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:    timeout,
			ResponseHeaderTimeout:  timeout,
			MaxResponseHeaderBytes: 32 << 10,
			MaxConnsPerHost:        1,
			DisableCompression:     true,
		},
	}
}

func probe(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("url", "", "HTTP or HTTPS endpoint to probe")
	status := flags.Int("want-status", http.StatusOK, "Expected response status")
	contains := flags.String("contains", "", "Expected response substring")
	timeout := flags.Duration("timeout", 10*time.Second, "Bounded request timeout")
	wantError := flags.Bool("want-error", false, "Require a transport failure")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid probe options")
	}
	endpoint, err := url.Parse(*address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		*timeout <= 0 || *timeout > time.Minute || *status < 100 || *status > 599 {
		return errors.New("invalid probe configuration")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("invalid probe request")
	}
	client := newClient(*timeout)
	defer client.CloseIdleConnections()
	// #nosec G704 -- The ephemeral test harness supplies this intentional CLI-directed URL,
	// never an HTTP caller. The request has bounded time/body and no redirects or ambient proxy.
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("probe canceled")
		}
		if *wantError {
			fmt.Println("probe passed: transport failure")
			return nil
		}
		return errors.New("probe transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if *wantError {
		return errors.New("probe received an HTTP response instead of a transport failure")
	}
	if response.StatusCode != *status {
		return fmt.Errorf("probe status mismatch: got %d, want %d", response.StatusCode, *status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return errors.New("probe body unavailable or too large")
	}
	if !strings.Contains(string(body), *contains) {
		return errors.New("probe response did not contain expected text")
	}
	fmt.Printf("probe passed: status=%d\n", response.StatusCode)
	return nil
}
