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

// TestUIKitProcess runs the actual command in an isolated subprocess.
func TestUIKitProcess(t *testing.T) {
	if os.Getenv("DPC_UI_KIT_TEST_PROCESS") != "true" {
		t.Skip("command subprocess entrypoint")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"ui-kit"}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("ui-kit", flag.ExitOnError)
	main()
}

// TestUIKitTransportAndReleaseGate catches plaintext fallback and health being gated on UI activation.
func TestUIKitTransportAndReleaseGate(t *testing.T) {
	for _, transport := range []string{"tls", "gateway"} {
		for _, enabled := range []string{"", "false", "true"} {
			t.Run(transport+"/"+enabled, func(t *testing.T) {
				address := availableAddress(t)
				args := []string{"--listen-address", address}
				client := &http.Client{Timeout: time.Second}
				scheme := "http"
				if transport == "tls" {
					cert, key, trusted := hostCertificate(t)
					args = append(args, "--tls-cert", cert, "--tls-key", key)
					client.Transport = trusted
					scheme = "https"
				} else {
					args = append(args, "--http-behind-gateway")
				}
				startHost(t, args, enabled)
				base := scheme + "://" + address
				waitForHost(t, client, base+"/healthz")
				status, body, headers := hostRequest(t, client, http.MethodGet, base+"/healthz", "")
				if status != http.StatusOK || body != "ok\n" ||
					headers.Get("Cache-Control") != "no-store" {
					t.Fatalf(
						"bounded health result: status=%d body=%q headers=%v",
						status,
						body,
						headers,
					)
				}
				status, body, headers = hostRequest(t, client, http.MethodHead, base+"/healthz", "")
				if status != http.StatusOK || body != "" || headers.Get("Content-Length") != "3" {
					t.Fatal("HEAD health exposed a body or lost its bounded response headers")
				}
				status, body, headers = hostRequest(t, client, http.MethodGet, base+"/", "")
				if enabled != "true" {
					if status != http.StatusNotFound || strings.Contains(body, "DataProductUI") {
						t.Fatal("disabled host exposed compatibility assets")
					}
				} else if status != http.StatusOK || !strings.Contains(body, "ui-contract.js") ||
					!strings.Contains(
						headers.Get("Content-Security-Policy"),
						"connect-src 'none'",
					) {
					t.Fatal("enabled host lost its embedded assets or no-connection policy")
				}
				status, _, _ = hostRequest(t, client, http.MethodPost, base+"/healthz", "")
				if status != http.StatusMethodNotAllowed {
					t.Fatal("health endpoint accepted an unsupported method")
				}
				for _, request := range []struct{ suffix, body string }{{"?config=1", ""}, {"", "payload"}} {
					status, _, _ = hostRequest(
						t,
						client,
						http.MethodGet,
						base+"/healthz"+request.suffix,
						request.body,
					)
					if status != http.StatusBadRequest {
						t.Fatal("health endpoint accepted input")
					}
				}
				if transport == "tls" {
					untrusted := &http.Client{Timeout: time.Second}
					response, err := getHost(t, untrusted, base+"/healthz")
					if response != nil {
						_ = response.Body.Close()
					}
					if err == nil {
						t.Fatal("TLS host did not require trusted certificates")
					}
				}
			})
		}
	}
}

// TestUIKitHelpPreservesDiscoverableOptions catches discarding the command's public help.
func TestUIKitHelpPreservesDiscoverableOptions(t *testing.T) {
	command := hostCommand(t, t.Context(), []string{"--help"}, "false")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "http-behind-gateway") ||
		!strings.Contains(string(output), "tls-cert") {
		t.Fatalf("help did not describe the transport options: err=%v output=%s", err, output)
	}
}

// TestUIKitRejectsAmbiguousTransport catches implicit wildcard binding and mixed TLS/plaintext options.
func TestUIKitRejectsAmbiguousTransport(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"TLS default", nil, "--tls-cert and --tls-key"},
		{"implicit HTTP listener", []string{"--http-behind-gateway"}, "explicit --listen-address"},
		{"empty host", []string{"--http-behind-gateway", "--listen-address=:8080"}, "listener"},
		{"invalid port", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:70000"}, "listener"},
		{"ephemeral port", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:0"}, "listener"},
		{"TLS certificate with HTTP", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080", "--tls-cert=cert.pem"}, "TLS material"},
		{"TLS key with HTTP", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080", "--tls-key=key.pem"}, "TLS material"},
		{"unexpected positional argument", []string{"--http-behind-gateway", "--listen-address=127.0.0.1:8080", "extra"}, "options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := hostCommand(t, ctx, tc.args, "false")
			output, err := command.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("configuration did not fail before serving: err=%v output=%s", err, output)
			}
		})
	}
}

func hostCommand(t *testing.T, ctx context.Context, args []string, enabled string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Only the current test executable is re-entered with locally authored fixture options.
	command := exec.CommandContext(
		ctx,
		executable,
		append([]string{"-test.run=^TestUIKitProcess$", "--"}, args...)...)
	command.Env = append(
		os.Environ(),
		"DPC_UI_KIT_TEST_PROCESS=true",
		"UI_CONTRACT_ENABLED="+enabled,
		"UI_APPEARANCE_ENABLED=false",
	)
	return command
}

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

func getHost(t *testing.T, client *http.Client, address string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G704 -- Tests supply only their disposable 127.0.0.1 listener; no remote input is consumed.
	return client.Do(request)
}

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
	// #nosec G704 -- Tests supply only their disposable 127.0.0.1 listener; no remote input is consumed.
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
