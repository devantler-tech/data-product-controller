package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPublisherProcess re-enters the actual command in an isolated subprocess.
func TestPublisherProcess(t *testing.T) {
	if os.Getenv("DPC_PUBLISHER_TEST_PROCESS") != "true" {
		t.Skip("command subprocess entrypoint")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"publisher-review"}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("publisher-review", flag.ExitOnError)
	main()
}

// TestPublisherTransportAndGate catches an unavailable application, accidental presentation activation and plaintext fallback.
func TestPublisherTransportAndGate(t *testing.T) {
	// Malformed presentation settings must neither prevent publisher review nor grant presentation routes.
	for _, setting := range []string{"invalid", "true"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("UI_CONTRACT_ENABLED", setting)
			t.Setenv("UI_APPEARANCE_ENABLED", setting)
			t.Setenv("REGISTRY_DISCOVERY_ENABLED", setting)
			for _, transport := range []string{"gateway", "tls"} {
				for _, enabled := range []string{"", "false", "true"} {
					t.Run(transport+"/"+enabled, func(t *testing.T) {
						address := availableAddress(t)
						args := []string{"--listen-address", address}
						client := &http.Client{Timeout: time.Second}
						scheme := "http"
						if transport == "tls" {
							cert, key, trusted := hostCertificate(t)
							args = append(args, "--tls-cert", cert, "--tls-key", key)
							client.Transport, scheme = trusted, "https"
						} else {
							args = append(args, "--http-behind-gateway")
						}
						startHost(t, args, enabled)
						base := scheme + "://" + address
						waitForHost(t, client, base+"/healthz")
						status, body, headers := hostRequest(
							t,
							client,
							http.MethodGet,
							base+"/healthz",
							"",
						)
						if status != 200 || body != "ok\n" ||
							headers.Get("Cache-Control") != "no-store" {
							t.Fatal("health unavailable or cacheable")
						}
						status, body, headers = hostRequest(
							t,
							client,
							http.MethodHead,
							base+"/healthz",
							"",
						)
						if status != 200 || body != "" || headers.Get("Content-Length") != "3" {
							t.Fatal("HEAD health response changed")
						}
						want := http.StatusNotFound
						if enabled == "true" {
							want = http.StatusOK
						}
						for _, path := range []string{"/", "/index.html", "/publisher-review", "/publisher.html", "/publisher.js", "/publisher.css", "/preflight-report.js", "/metadata.js", "/descriptor.js"} {
							status, body, headers = hostRequest(
								t,
								client,
								http.MethodGet,
								base+path,
								"",
							)
							if status != want {
								t.Fatalf("%s: status=%d want=%d", path, status, want)
							}
							if enabled == "true" &&
								(!strings.Contains(headers.Get("Content-Security-Policy"), "frame-src 'none'") || !strings.Contains(headers.Get("Content-Security-Policy"), "connect-src 'none'")) {
								t.Fatal("publisher host permits active product content")
							}
							if enabled == "true" && (path == "/" || path == "/publisher-review") &&
								(!strings.Contains(body, "preflight-report.js") || strings.Contains(body, "ui-contract.js") || strings.Contains(body, "workspace-navigation\"")) {
								t.Fatal(
									"publisher entrypoint loads presentation or legacy navigation",
								)
							}
						}
						for _, path := range []string{"/ui-contract.js", "/kit.js", "/kit.css", "/api/products", "/missing"} {
							status, _, _ = hostRequest(t, client, http.MethodGet, base+path, "")
							if status != http.StatusNotFound {
								t.Fatalf("unrelated route %s exposed", path)
							}
						}
						status, _, _ = hostRequest(t, client, http.MethodPost, base+"/healthz", "")
						if status != http.StatusMethodNotAllowed {
							t.Fatal("health accepts unsupported method")
						}
						for _, request := range []struct{ suffix, body string }{{"?input=1", ""}, {"", "payload"}} {
							status, _, _ = hostRequest(
								t,
								client,
								http.MethodGet,
								base+"/healthz"+request.suffix,
								request.body,
							)
							if status != http.StatusBadRequest {
								t.Fatal("health accepts input")
							}
						}
						if transport == "tls" {
							response, err := getHost(
								t,
								&http.Client{Timeout: time.Second},
								base+"/healthz",
							)
							if response != nil {
								_ = response.Body.Close()
							}
							if err == nil {
								t.Fatal("TLS accepted untrusted certificate")
							}
						}
					})
				}
			}
		})
	}
}

// TestPublisherRejectsInvalidOptions catches implicit HTTP, ambiguous listeners and malformed release opt-ins.
func TestPublisherRejectsInvalidOptions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		enabled, want string
	}{
		{"TLS default", nil, "", "--tls-cert and --tls-key"},
		{"implicit HTTP", []string{"--http-behind-gateway"}, "", "explicit --listen-address"},
		{"empty host", []string{"--http-behind-gateway", "--listen-address=:8080"}, "", "listener"},
		{"invalid port", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:70000"}, "", "listener"},
		{"ephemeral port", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:0"}, "", "listener"},
		{"mixed TLS", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080", "--tls-cert=cert.pem"}, "", "TLS material"},
		{"positional", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080", "extra"}, "", "options"},
		{"bad opt-in", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080"}, "invalid", "PUBLISHER_PREFLIGHT_ENABLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			output, err := hostCommand(t, ctx, tc.args, tc.enabled).CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("invalid configuration accepted: err=%v output=%s", err, output)
			}
		})
	}
}

// TestPublisherHelp retains discoverable explicit transport options.
func TestPublisherHelp(t *testing.T) {
	output, err := hostCommand(t, t.Context(), []string{"--help"}, "").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "tls-cert") ||
		!strings.Contains(string(output), "http-behind-gateway") {
		t.Fatalf("help lost transport options: %v %s", err, output)
	}
}

// hostCommand re-enters only this test binary's publisher process under the caller's context and gate setting.
func hostCommand(t *testing.T, ctx context.Context, args []string, enabled string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Re-enters only the current test binary with authored disposable fixture arguments.
	command := exec.CommandContext(
		ctx,
		executable,
		append([]string{"-test.run=^TestPublisherProcess$", "--"}, args...)...)
	command.Env = append(
		os.Environ(),
		"DPC_PUBLISHER_TEST_PROCESS=true",
		"PUBLISHER_PREFLIGHT_ENABLED="+enabled,
	)
	return command
}

// startHost registers cancellation and process reaping, reporting subprocess output when tests fail.
func startHost(t *testing.T, args []string, enabled string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	command := hostCommand(t, ctx, args, enabled)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})
}

// waitForHost allows three seconds for the configured transport and closes each successful probe response.
func waitForHost(t *testing.T, client *http.Client, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err := getHost(t, client, address)
		if err == nil {
			_ = response.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("host never accepted the configured transport")
}

// getHost binds a fixture GET to the test context; the caller must close a returned response body.
func getHost(t *testing.T, client *http.Client, address string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G704 -- Only disposable loopback listeners authored by these tests are requested.
	return client.Do(request)
}

// hostRequest captures status, headers and at most 64 KiB from a fixture response, then closes its body.
func hostRequest(
	t *testing.T,
	client *http.Client,
	method, address, body string,
) (int, string, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		address,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G704 -- Only disposable loopback listeners authored by these tests are requested.
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data), response.Header
}
