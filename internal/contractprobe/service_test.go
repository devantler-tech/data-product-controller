package contractprobe

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

// TestReachabilityFailuresAndRecovery requires complete, bounded successful responses, with no response leakage.
func TestReachabilityFailuresAndRecovery(t *testing.T) {
	t.Parallel()
	var mode atomic.Int32
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" ||
				r.Header.Get("Accept-Encoding") != "identity" {
				t.Error("probe sent an unexpected method, credential, or encoding")
			}
			switch mode.Load() {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
			case 2:
				return
			case 3:
				_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
			case 4:
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = io.WriteString(w, "compressed sentinel")
			case 5:
				w.Header().Set("Content-Length", "100")
				_, _ = io.WriteString(w, "short")
			default:
				_, _ = io.WriteString(w, `{"openapi":"3.1.0","private":"response sentinel"}`)
			}
		}),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	for _, tc := range []struct {
		mode   int32
		status int
		reason string
	}{
		{0, 200, "ContractReachable"},
		{1, 503, "ContractUnavailable"},
		{2, 503, "ContractInvalidResponse"},
		{3, 503, "ContractInvalidResponse"},
		{4, 503, "ContractInvalidResponse"},
		{5, 503, "ContractUnavailable"},
		{0, 200, "ContractReachable"},
	} {
		mode.Store(tc.mode)
		response := request(t, service, http.MethodGet, "/readyz")
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.reason) ||
			strings.Contains(response.Body.String(), "sentinel") {
			t.Fatalf("mode %d: %d %s", tc.mode, response.Code, response.Body)
		}
		metrics := request(t, service, http.MethodGet, "/metrics").Body.String()
		wantGauge := "contract_probe_ready 0"
		if tc.status == 200 {
			wantGauge = "contract_probe_ready 1"
		}
		if !strings.Contains(metrics, wantGauge) || strings.Contains(metrics, upstream.URL) ||
			strings.Contains(metrics, "sentinel") {
			t.Fatalf("incorrect or unsafe metrics: %s", metrics)
		}
	}
}

// TestProbeOpenFeatureAndRequestBoundary prevents disabled, malformed or management requests from accessing contracts.
func TestProbeOpenFeatureAndRequestBoundary(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = io.WriteString(w, "contract")
		}),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	for _, enabled := range []bool{false, true} {
		flags, err := featureflag.NewClient(
			t.Name()+strings.ToLower(map[bool]string{false: "off", true: "on"}[enabled]),
			featureflag.NewProvider(map[string]bool{"contract-readiness": enabled}),
		)
		if err != nil {
			t.Fatal(err)
		}
		service.enabled = func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "contract-readiness") }
		before := requests.Load()
		for _, tc := range []struct {
			method, path string
			status       int
		}{
			{"GET", "/healthz", 200},
			{"GET", "/metrics", 200},
			{"POST", "/readyz", 405},
			{"GET", "/readyz?url=https://other.invalid", 400},
			{"GET", "/readyz?", 400},
			{"GET", "/unknown", 404},
		} {
			response := request(t, service, tc.method, tc.path)
			if response.Code != tc.status {
				t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
			}
		}
		if requests.Load() != before {
			t.Fatal("management or rejected request fetched contract")
		}
		response := request(t, service, "GET", "/readyz")
		if enabled && (response.Code != 200 || requests.Load() != before+1) {
			t.Fatal("enabled probe did not fetch contract")
		}
		if !enabled &&
			(response.Code != 503 || requests.Load() != before || !strings.Contains(response.Body.String(), "FeatureDisabled")) {
			t.Fatal("disabled probe fetched contract or appeared ready")
		}
	}
}

// TestProbeRejectsUnsafeConfigurationAndTLS requires HTTPS without embedded credentials, query secrets, or untrusted certificates.
func TestProbeRejectsUnsafeConfigurationAndTLS(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); _, _ = io.WriteString(w, "contract") },
		),
	)
	defer upstream.Close()
	for _, target := range []string{"", "http://example.com/schema", upstream.URL + "?token=sentinel", upstream.URL + "#fragment", "https://user@example.com/schema", "https://", upstream.URL + "?", upstream.URL + "/$(OTHER)"} {
		service := NewService(target, func(context.Context) bool { return true })
		response := request(t, service, "GET", "/readyz")
		service.Close()
		if response.Code != 503 ||
			!strings.Contains(response.Body.String(), "ContractConfigurationInvalid") {
			t.Fatalf("accepted unsafe URL: %d %s", response.Code, response.Body)
		}
	}
	service := NewService(upstream.URL, func(context.Context) bool { return true })
	defer service.Close()
	if response := request(t, service, "GET", "/readyz"); response.Code != 503 {
		t.Fatal("accepted untrusted certificate")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid configuration reached upstream")
	}
}

// TestProbeNeverFollowsRedirects ensures neither the body nor another destination is reached.
func TestProbeNeverFollowsRedirects(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	destination := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			redirected.Add(1)
			_, _ = io.WriteString(w, "unexpected")
		}),
	)
	defer destination.Close()
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) },
		),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	if response := request(
		t, service,
		"GET",
		"/readyz",
	); response.Code != 503 ||
		redirected.Load() != 0 {
		t.Fatal("redirect was accepted")
	}
}

// TestProbeBoundsConcurrencyAndCancellation prevents readiness callers from queueing unlimited network work.
func TestProbeBoundsConcurrencyAndCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(
			func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() },
		),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		service.ServeHTTP(response, httptest.NewRequestWithContext(ctx, "GET", "/readyz", nil))
		done <- response
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe never reached upstream")
	}
	response := request(t, service, "GET", "/readyz")
	if response.Code != 503 || !strings.Contains(response.Body.String(), "ContractProbeBusy") {
		t.Fatal("excess probe was queued")
	}
	cancel()
	select {
	case response = <-done:
		if response.Code != 503 {
			t.Fatal("cancelled request appeared ready")
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not bound network work")
	}
}

func trustedService(t *testing.T, upstream *httptest.Server) *Service {
	t.Helper()
	service := NewService(upstream.URL, func(context.Context) bool { return true })
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("probe transport must preserve TLS validation")
	}
	transport.TLSClientConfig.RootCAs = pool
	t.Cleanup(service.Close)
	return service
}

func request(t *testing.T, service *Service, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	service.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), method, path, nil))
	return response
}

// TestProbeIgnoresAmbientProxy prevents environment proxy settings from changing the operator's network boundary.
func TestProbeIgnoresAmbientProxy(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { proxyRequests.Add(1); w.WriteHeader(502) },
		),
	)
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "contract") },
		),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	service.target = "https://contract.example.test/schema"
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport")
	}
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	if response := request(
		t, service,
		"GET",
		"/readyz",
	); response.Code != 200 ||
		proxyRequests.Load() != 0 {
		t.Fatal("ambient proxy affected contract fetch")
	}
}

// TestSlowContractTimesOut checks that the total deadline also bounds a stalled response body.
func TestSlowContractTimesOut(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			if _, err := io.WriteString(w, "partial"); err != nil {
				t.Error(err)
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
				return
			}
			<-r.Context().Done()
		}),
	)
	defer upstream.Close()
	service := trustedService(t, upstream)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	started := time.Now()
	service.ServeHTTP(response, httptest.NewRequestWithContext(ctx, "GET", "/readyz", nil))
	if response.Code != 503 || time.Since(started) > 7*time.Second {
		t.Fatalf(
			"probe exceeded its network deadline: %d after %s",
			response.Code,
			time.Since(started),
		)
	}
}

// TestProbeRejectsRequestBodies prevents management callers from supplying upstream inputs.
func TestProbeRejectsRequestBodies(t *testing.T) {
	t.Parallel()
	service := NewService("https://unreachable.invalid", func(context.Context) bool { return true })
	defer service.Close()
	response := httptest.NewRecorder()
	service.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), "GET", "/readyz", strings.NewReader("input")),
	)
	if response.Code != 400 {
		t.Fatalf("request body accepted: %d", response.Code)
	}
}
