package httpsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

// TestReleaseGate prevents a disabled connector from reading credentials or contacting a source.
func TestReleaseGate(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			source := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"items":[{"id":1}]}`)
				}),
			)
			defer source.Close()
			flags, err := featureflag.NewClient(
				t.Name(),
				featureflag.NewProvider(map[string]bool{"http-source": enabled}),
			)
			if err != nil {
				t.Fatal(err)
			}
			service, _ := fixture(t, source)
			service.enabled = func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "http-source") }
			response := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
			want := http.StatusNotFound
			if enabled {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("query status = %d, want %d: %s", response.Code, want, response.Body)
			}
			if (calls.Load() != 0) != enabled {
				t.Fatalf("flag %t contacted source %d times", enabled, calls.Load())
			}
			contract := request(service.PublicHandler(), http.MethodGet, "/openapi.json", "")
			if contract.Code != want {
				t.Fatalf("contract status = %d, want %d", contract.Code, want)
			}
		})
	}
	service := NewService(filepath.Join(t.TempDir(), "missing"), nil)
	t.Cleanup(service.Close)
	if got := request(
		service.ManagementHandler(),
		http.MethodGet,
		"/readyz",
		"",
	); got.Code != http.StatusServiceUnavailable ||
		!strings.Contains(got.Body.String(), "FeatureDisabled") {
		t.Fatalf("default readiness = %d %s", got.Code, got.Body)
	}
}

// TestFixedReadOnlyBoundary catches writes, arbitrary URLs, credentials, or caller state reaching upstream.
func TestFixedReadOnlyBoundary(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/export" ||
			r.Header.Get("Authorization") != "Bearer read-only-token" {
			t.Errorf("unexpected upstream request method/path or credential")
		}
		for _, header := range []string{"Cookie", "X-Forwarded-For", "X-Caller", "Range"} {
			if r.Header.Get(header) != "" {
				t.Errorf("forwarded caller header %s", header)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "upstream=private")
		w.Header().Set("X-Source-Detail", "private")
		_, _ = io.WriteString(w, `{"items":[{"id":7}]}`)
	}))
	defer source.Close()
	service, _ := fixture(t, source)
	r := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	for _, header := range []string{"Authorization", "Cookie", "X-Forwarded-For", "X-Caller", "Range"} {
		r.Header.Set(header, "caller-private")
	}
	w := httptest.NewRecorder()
	service.PublicHandler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[{"id":7}]}` {
		t.Fatalf("query = %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing response boundary headers")
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Source-Detail") != "" ||
		w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("upstream or cross-origin authority leaked")
	}
	for _, test := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/data", `{}`, 405},
		{http.MethodPut, "/api/data", `{}`, 405},
		{http.MethodDelete, "/api/data", "", 405},
		{http.MethodHead, "/api/data", "", 405},
		{http.MethodGet, "/api/data?url=https://other.example/export", "", 400},
		{http.MethodGet, "/api/data?", "", 400},
		{http.MethodGet, "/api/data", `{}`, 400},
		{http.MethodGet, "/api/data/other", "", 404},
		{http.MethodGet, "/metrics", "", 404},
		{http.MethodGet, "/readyz", "", 404},
	} {
		got := request(service.PublicHandler(), test.method, test.path, test.body)
		if got.Code != test.want {
			t.Errorf("%s %s = %d, want %d", test.method, test.path, got.Code, test.want)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("rejected requests reached source; calls=%d", calls.Load())
	}
	if got := request(
		service.ManagementHandler(),
		http.MethodGet,
		"/api/data",
		"",
	); got.Code != 404 {
		t.Fatal("management listener exposes source data")
	}
}

// TestSecretRotationAndRecovery exercises mounted-file replacement and fresh health signals over HTTPS.
func TestSecretRotationAndRecovery(t *testing.T) {
	t.Parallel()
	var expectedToken atomic.Value
	expectedToken.Store("Bearer read-only-token")
	var available atomic.Bool
	available.Store(true)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() || r.Header.Get("Authorization") != expectedToken.Load() {
			http.Error(w, "private-provider-detail", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":"visible-export"}`)
	}))
	defer source.Close()
	service, configPath := fixture(t, source)
	public := httptest.NewServer(service.PublicHandler())
	defer public.Close()
	management := httptest.NewServer(service.ManagementHandler())
	defer management.Close()
	get := func(base, path string, want int) string {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		// #nosec G704 -- Both base URLs are local httptest servers owned by this test.
		response, err := public.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s = %d, want %d: %s", path, response.StatusCode, want, body)
		}
		if strings.Contains(string(body), "private-provider-detail") ||
			strings.Contains(string(body), "read-only-token") ||
			strings.Contains(string(body), source.URL) {
			t.Fatal("source detail leaked")
		}
		return string(body)
	}
	if got := get(
		public.URL,
		"/api/data",
		200,
	); strings.TrimSpace(
		got,
	) != `{"data":"visible-export"}` {
		t.Fatalf("data = %s", got)
	}
	if got := get(management.URL, "/readyz", 200); strings.Contains(got, "visible-export") {
		t.Fatal("probe leaks source data")
	}
	available.Store(false)
	get(public.URL, "/api/data", 502)
	get(management.URL, "/readyz", 503)
	if got := get(management.URL, "/metrics", 200); !strings.Contains(got, "http_source_ready 0") {
		t.Fatalf("outage readiness metric absent: %s", got)
	}
	available.Store(true)
	expectedToken.Store("Bearer rotated-token")
	get(public.URL, "/api/data", 502)
	writeSecret(t, configPath, source.URL+"/export", "rotated-token")
	get(public.URL, "/api/data", 200)
	get(management.URL, "/readyz", 200)
	metrics := get(management.URL, "/metrics", 200)
	for _, expected := range []string{"http_source_ready 1", `http_source_requests_total{operation="query",result="success"} 2`, `http_source_requests_total{operation="query",result="upstream_error"} 2`} {
		if !strings.Contains(metrics, expected) {
			t.Errorf("missing metric %q in %s", expected, metrics)
		}
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	get(management.URL, "/readyz", 503)
	get(management.URL, "/healthz", 200)
	writeSecret(t, configPath, source.URL+"/export", "rotated-token")
	get(management.URL, "/readyz", 200)
}

// TestInvalidSecretFailsClosed catches unsafe endpoint or bearer configuration before any request.
func TestInvalidSecretFailsClosed(t *testing.T) {
	t.Parallel()
	for _, config := range []string{
		`{}`, `null`, `[]`, `{"endpointURL":"http://example.com/export","bearerToken":"private"}`,
		`{"endpointURL":"https://user:private@example.com/export","bearerToken":"private"}`,
		`{"endpointURL":"https://example.com/export?token=private","bearerToken":"private"}`,
		`{"endpointURL":"https://example.com/export#private","bearerToken":"private"}`,
		`{"endpointURL":"https://example.com/export","bearerToken":""}`,
		`{"endpointURL":"https://example.com/export","bearerToken":"private\r\nInjected: bad"}`,
		`{"endpointURL":"https://example.com/export","bearerToken":"private","unexpected":true}`,
		`{"endpointURL":"https://example.com/export","bearerToken":"private"} {}`,
		strings.Repeat(" ", 16385),
	} {
		t.Run(fmt.Sprint(len(config), "-", strings.Index(config, "private")), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewService(path, func(context.Context) bool { return true })
			t.Cleanup(service.Close)
			got := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
			if got.Code != 503 || strings.Contains(got.Body.String(), "private") {
				t.Fatalf("invalid configuration = %d %s", got.Code, got.Body)
			}
		})
	}
}

// TestUpstreamResponseBoundary rejects redirects, oversized bodies, malformed JSON, and error detail.
func TestUpstreamResponseBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, contentType, body, encoding string
		status                            int
	}{
		{name: "redirect", status: 302},
		{name: "unauthorized", status: 401, body: "private-provider-detail"},
		{name: "partial", status: 206, contentType: "application/json", body: `{}`},
		{name: "html", status: 200, contentType: "text/html", body: `<script>private</script>`},
		{name: "invalid JSON", status: 200, contentType: "application/json", body: `{"private":`},
		{name: "invalid UTF-8", status: 200, contentType: "application/json", body: string([]byte{'"', 0xff, '"'})},
		{name: "multiple values", status: 200, contentType: "application/json", body: `{} {}`},
		{name: "too large", status: 200, contentType: "application/json", body: `"` + strings.Repeat("x", 1<<20) + `"`},
		{name: "compressed", status: 200, contentType: "application/json", encoding: "gzip", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var redirected atomic.Int32
			target := httptest.NewTLSServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(200) },
				),
			)
			defer target.Close()
			source := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", test.contentType)
					w.Header().Set("Content-Encoding", test.encoding)
					w.Header().Set("Location", target.URL)
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
				}),
			)
			defer source.Close()
			service, _ := fixture(t, source)
			got := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
			if got.Code != 502 || strings.Contains(got.Body.String(), "private") ||
				got.Header().Get("Location") != "" {
				t.Fatalf("upstream failure = %d %s", got.Code, got.Body)
			}
			if redirected.Load() != 0 {
				t.Fatal("followed upstream redirect")
			}
		})
	}
}

// TestRequestDeadlineAndConcurrency bounds slow sources and refuses excess work without queueing it.
func TestRequestDeadlineAndConcurrency(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer source.Close()
	service, _ := fixture(t, source)
	service.client.Timeout = 500 * time.Millisecond
	started := time.Now()
	got := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
	if got.Code != 502 || time.Since(started) > 2*time.Second {
		t.Fatalf("deadline = %d after %v", got.Code, time.Since(started))
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed request did not reach the source")
	}
	service.client.Timeout = 5 * time.Second
	done := make(chan int, 4)
	for range 4 {
		go func() { done <- request(service.PublicHandler(), http.MethodGet, "/api/data", "").Code }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("requests failed to start")
		}
	}
	got = request(service.PublicHandler(), http.MethodGet, "/api/data", "")
	close(release)
	if got.Code != 503 {
		t.Errorf("overload = %d, want 503", got.Code)
	}
	for range 4 {
		if code := <-done; code != 200 {
			t.Errorf("accepted query = %d", code)
		}
	}
}

// TestTLSVerification rejects sources with an untrusted certificate through the production client.
func TestTLSVerification(t *testing.T) {
	t.Parallel()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer source.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	writeSecret(t, path, source.URL+"/export", "read-only-token")
	service := NewService(path, func(context.Context) bool { return true })
	t.Cleanup(service.Close)
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 502 {
		t.Fatalf("untrusted TLS source = %d", got.Code)
	}
}

// TestOpenAPIContract publishes only the connector API and never source connection information.
func TestOpenAPIContract(t *testing.T) {
	t.Parallel()
	service := NewService(
		filepath.Join(t.TempDir(), "missing"),
		func(context.Context) bool { return true },
	)
	t.Cleanup(service.Close)
	got := request(service.PublicHandler(), http.MethodGet, "/openapi.json", "")
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &document) != nil {
		t.Fatalf("contract = %d %s", got.Code, got.Body)
	}
	if document.OpenAPI != "3.1.0" || len(document.Paths) != 1 ||
		len(document.Paths["/api/data"]) != 1 ||
		document.Paths["/api/data"]["get"] == nil {
		t.Fatalf("unexpected public contract: %s", got.Body)
	}
	if strings.Contains(got.Body.String(), "bearerToken") ||
		strings.Contains(got.Body.String(), "endpointURL") {
		t.Fatal("contract exposes source connection fields")
	}
}

// fixture trusts only the local HTTPS source certificate, retaining the production transport boundaries.
func fixture(t *testing.T, source *httptest.Server) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	writeSecret(t, path, source.URL+"/export", "read-only-token")
	service := NewService(path, func(context.Context) bool { return true })
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("connector transport is not configurable for test certificates")
	}
	sourceTransport, ok := source.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("test source has no TLS transport")
	}
	transport.TLSClientConfig = sourceTransport.TLSClientConfig.Clone()
	t.Cleanup(service.Close)
	return service, path
}

// writeSecret atomically replaces one mounted configuration file, like a projected Secret update.
func writeSecret(t *testing.T, path, endpoint, token string) {
	t.Helper()
	data, err := json.Marshal(map[string]string{"endpointURL": endpoint, "bearerToken": token})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".new", data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

// request exercises an HTTP handler while retaining exact response headers and body for assertions.
func request(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	handler.ServeHTTP(w, r)
	return w
}
