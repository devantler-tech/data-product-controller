package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// discovery observes native paging across replicas and checks every expected exact descriptor.
func discovery(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("discovery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("url", "", "First replica discovery endpoint")
	replica := flags.String("replica-url", "", "Second replica discovery endpoint")
	namespace := flags.String("namespace", "", "Expected namespace")
	expectedNames := flags.String("expected", "", "Comma-separated expected product names")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid discovery options")
	}
	endpoints := make([]*url.URL, 2)
	for index, raw := range []string{*address, *replica} {
		endpoint, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || endpoint.Hostname() == "" || endpoint.User != nil ||
			endpoint.Fragment != "" ||
			endpoint.RawQuery != "" ||
			(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
			endpoint.Path != "/api/v2/products" {
			return errors.New("invalid discovery endpoint")
		}
		endpoints[index] = endpoint
	}
	label := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	if len(*namespace) > 63 || !label.MatchString(*namespace) {
		return errors.New("invalid discovery namespace")
	}
	names := strings.Split(*expectedNames, ",")
	if len(names) > 100 {
		return errors.New("discovery expectation is too large")
	}
	expected := map[string]bool{}
	for _, name := range names {
		if len(name) > 63 || !label.MatchString(name) || expected[name] {
			return errors.New("invalid discovery expectation")
		}
		expected[name] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := newClient(10 * time.Second)
	defer client.CloseIdleConnections()
	seen := map[string]bool{}
	cursors := map[string]bool{}
	token := ""
	for pageIndex := 0; pageIndex <= 100; pageIndex++ {
		endpoint := *endpoints[pageIndex%2]
		query := url.Values{"namespace": {*namespace}, "limit": {"1"}}
		if token != "" {
			query.Set("continue", token)
		}
		endpoint.RawQuery = query.Encode()
		body, err := discoveryRead(ctx, client, &endpoint)
		if err != nil {
			return err
		}
		var page struct {
			APIVersion string            `json:"apiVersion"`
			Products   []json.RawMessage `json:"products"`
			Continue   string            `json:"continue"`
			Rejected   *int              `json:"rejected"`
		}
		if json.Unmarshal(body, &page) != nil || page.APIVersion != "data-product-discovery/v1" ||
			page.Products == nil ||
			page.Rejected == nil || *page.Rejected != 0 ||
			len(page.Products) > 1 ||
			len(page.Continue) > 16<<10 {
			return errors.New("invalid bounded discovery page")
		}
		for _, raw := range page.Products {
			name, err := discoveryIdentity(raw, *namespace)
			if err != nil || !expected[name] || seen[name] {
				return errors.New("unexpected, duplicate or invalid discovery identity")
			}
			seen[name] = true
		}
		if page.Continue == "" {
			break
		}
		if cursors[page.Continue] || pageIndex == 100 {
			return errors.New("discovery did not terminate within its bound")
		}
		cursors[page.Continue] = true
		token = page.Continue
	}
	if len(seen) != len(expected) {
		return errors.New("discovery omitted an expected product")
	}
	for _, name := range names {
		endpoint := *endpoints[0]
		endpoint.Path += "/" + *namespace + "/" + name
		body, err := discoveryRead(ctx, client, &endpoint)
		if err != nil {
			return err
		}
		actual, err := discoveryIdentity(body, *namespace)
		if err != nil || actual != name {
			return errors.New("exact descriptor identity mismatch")
		}
	}
	fmt.Printf("discovery passed: %d products, portable continuation and exact lookup\n", len(seen))
	return nil
}

// discoveryRead accepts only a bounded successful response from the synthetic test's explicit endpoint.
func discoveryRead(ctx context.Context, client *http.Client, endpoint *url.URL) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("invalid discovery request")
	}
	// #nosec G704 -- Owned ephemeral-cluster CLI endpoints, no credentials, redirects or ambient proxies.
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("discovery transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return nil, errors.New("discovery body exceeds bounds")
	}
	return body, nil
}

// discoveryIdentity verifies public version, current aggregate readiness and all applicable health observations.
func discoveryIdentity(raw []byte, namespace string) (string, error) {
	var descriptor struct {
		APIVersion, Kind, Namespace, Name string
		Ready                             *bool
		Generation, ObservedGeneration    *int64
		Health                            map[string]struct {
			State                          string
			Generation, ObservedGeneration int64
		}
	}
	if len(raw) > 64<<10 || json.Unmarshal(raw, &descriptor) != nil ||
		descriptor.APIVersion != "data-product-descriptor/v1" || descriptor.Kind != "DataProduct" ||
		descriptor.Namespace != namespace || descriptor.Name == "" || descriptor.Ready == nil ||
		descriptor.Generation == nil || descriptor.ObservedGeneration == nil ||
		(*descriptor.Ready && *descriptor.Generation != *descriptor.ObservedGeneration) {
		return "", errors.New("invalid public descriptor")
	}
	states := map[string]bool{
		"ready":          true,
		"not-ready":      true,
		"stale":          true,
		"unobserved":     true,
		"disabled":       true,
		"not-applicable": true,
	}
	for _, key := range []string{"source", "connector", "contracts", "composition"} {
		check, ok := descriptor.Health[key]
		if !ok || !states[check.State] || check.Generation != *descriptor.Generation ||
			(check.State == "ready" && check.ObservedGeneration != *descriptor.Generation) {
			return "", errors.New("invalid health observation")
		}
	}
	return descriptor.Name, nil
}
