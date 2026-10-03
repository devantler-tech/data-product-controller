package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// metrics requires a fresh completed observation from one independently managed workload.
func metrics(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("metrics", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("url", "", "Management metrics endpoint")
	kind := flags.String("kind", "", "http-source or contract-probe")
	ready := flags.Int("ready", -1, "Expected readiness gauge: zero or one")
	since := flags.Int64("since", 0, "Observation must be newer than this Unix second")
	timeout := flags.Duration("timeout", 10*time.Second, "Bounded request timeout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid metrics options")
	}
	endpoint, err := url.Parse(*address)
	if err != nil || len(*address) > 2048 || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(*kind != "http-source" && *kind != "contract-probe") ||
		(*ready != 0 && *ready != 1) || *since <= 0 || *timeout <= 0 || *timeout > time.Minute {
		return errors.New("invalid metrics configuration")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("invalid metrics request")
	}
	client := newClient(*timeout)
	defer client.CloseIdleConnections()
	// #nosec G704 -- Only the owned disposable harness supplies this management URL; time/body are bounded, with no redirects, proxies or credentials.
	response, err := client.Do(request)
	if err != nil {
		return errors.New("metrics transport unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" {
		return errors.New("invalid metrics response")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		return errors.New("metrics response unavailable or too large")
	}
	prefix := "http_source"
	if *kind == "contract-probe" {
		prefix = "contract_probe"
	}
	if err := completedMetrics(string(body), prefix, *ready, *since); err != nil {
		return err
	}
	fmt.Println("metrics passed: fresh completed observation")
	return nil
}

// completedMetrics selects exact unlabeled gauges without accepting duplicates or nonfinite values.
func completedMetrics(body, prefix string, ready int, since int64) error {
	readyName, timestampName := prefix+"_ready", prefix+"_last_observation_timestamp_seconds"
	values := make(map[string]float64, 2)
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 8<<10)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		name := fields[0]
		if strings.HasPrefix(name, readyName+"{") || strings.HasPrefix(name, timestampName+"{") {
			return errors.New("invalid observation metrics")
		}
		if name != readyName && name != timestampName {
			continue
		}
		if len(fields) != 2 {
			return errors.New("invalid observation metrics")
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		_, duplicate := values[name]
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || duplicate {
			return errors.New("invalid observation metrics")
		}
		values[name] = value
	}
	if scanner.Err() != nil || len(values) != 2 {
		return errors.New("incomplete observation metrics")
	}
	if values[readyName] != float64(ready) || values[timestampName] <= float64(since) ||
		values[timestampName] > float64(time.Now().Unix()+1) {
		return errors.New("completed observation does not match this phase")
	}
	return nil
}
