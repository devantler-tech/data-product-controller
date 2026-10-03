package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	registryProduct := flags.String(
		"registry-product",
		"",
		"Exact namespace/name in a registry response",
	)
	registryReady := flags.String(
		"registry-ready",
		"",
		"Required registry readiness: true or false",
	)
	registryReason := flags.String(
		"registry-reason",
		"",
		"Optional exact registry readiness reason",
	)
	registryAbsent := flags.String(
		"registry-absent",
		"",
		"Exact namespace/name that must be absent",
	)
	timeout := flags.Duration("timeout", 10*time.Second, "Bounded request timeout")
	wantError := flags.Bool("want-error", false, "Require a transport failure")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid probe options")
	}
	registryConfigured := *registryProduct != "" || *registryReady != "" || *registryReason != ""
	if *registryAbsent != "" &&
		(registryConfigured || *wantError || !registryIdentity(*registryAbsent)) {
		return errors.New("invalid registry absence configuration")
	}
	if registryConfigured && (*wantError || strings.Count(*registryProduct, "/") != 1 ||
		strings.HasPrefix(*registryProduct, "/") || strings.HasSuffix(*registryProduct, "/") ||
		len(*registryProduct) > 512 || (*registryReady != "true" && *registryReady != "false") || len(*registryReason) > 256) {
		return errors.New("invalid registry probe configuration")
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
	if registryConfigured {
		if err := verifyRegistryProduct(
			body,
			*registryProduct,
			*registryReady == "true",
			*registryReason,
		); err != nil {
			return err
		}
	}
	if *registryAbsent != "" {
		if err := verifyRegistryAbsent(body, *registryAbsent); err != nil {
			return err
		}
	}
	fmt.Printf("probe passed: status=%d\n", response.StatusCode)
	return nil
}

func registryIdentity(identity string) bool {
	return len(identity) <= 512 && strings.Count(identity, "/") == 1 &&
		!strings.HasPrefix(identity, "/") && !strings.HasSuffix(identity, "/")
}

func verifyRegistryAbsent(body []byte, identity string) error {
	if err := registryJSON(body); err != nil {
		return err
	}
	var response struct {
		Products *[]struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"products"`
	}
	if err := json.Unmarshal(
		body,
		&response,
	); err != nil || response.Products == nil ||
		len(*response.Products) > 256 {
		return errors.New("registry absence response unavailable or invalid")
	}
	for _, product := range *response.Products {
		if product.Namespace == "" || product.Name == "" ||
			!registryIdentity(product.Namespace+"/"+product.Name) {
			return errors.New("registry product identity unavailable")
		}
		if product.Namespace+"/"+product.Name == identity {
			return errors.New("registry product remains present")
		}
	}
	return nil
}

func verifyRegistryProduct(body []byte, identity string, ready bool, reason string) error {
	if err := registryJSON(body); err != nil {
		return err
	}
	var response struct {
		Products *[]struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Ready     *bool  `json:"ready"`
			Readiness struct {
				Reason string `json:"reason"`
			} `json:"readiness"`
		} `json:"products"`
	}
	if err := json.Unmarshal(
		body,
		&response,
	); err != nil || response.Products == nil ||
		len(*response.Products) > 256 {
		return errors.New("registry response unavailable or invalid")
	}
	matches := 0
	for _, product := range *response.Products {
		if product.Namespace+"/"+product.Name != identity {
			continue
		}
		matches++
		if product.Ready == nil || *product.Ready != ready ||
			(reason != "" && product.Readiness.Reason != reason) {
			return errors.New("registry product readiness mismatch")
		}
	}
	if matches != 1 {
		return errors.New("registry product identity absent or ambiguous")
	}
	return nil
}
