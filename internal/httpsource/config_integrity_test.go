package httpsource

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSourceConfigRejectsAmbiguousDeclarations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, declaration string }{
		{"duplicate-endpoint", `{"endpointURL":"https://private.invalid/export","endpointURL":%q,"bearerToken":"read-only-token"}`},
		{"duplicate-token", `{"endpointURL":%q,"bearerToken":"private-first","bearerToken":"read-only-token"}`},
		{"escaped-duplicate", `{"endpointURL":%q,"bearerToken":"private-first","bearer\u0054oken":"read-only-token"}`},
		{"empty-fragment", `{"endpointURL":%q,"bearerToken":"read-only-token"}`},
		{"invalid-utf8", `{"endpointURL":%q,"bearerToken":"read-only-token"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			source := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}),
			)
			defer source.Close()
			service, path := fixture(t, source)
			endpoint := source.URL + "/export"
			if test.name == "empty-fragment" {
				endpoint += "#"
			}
			declaration := fmt.Sprintf(test.declaration, endpoint)
			if test.name == "invalid-utf8" {
				declaration = strings.Replace(
					declaration,
					"/export",
					"/export"+string([]byte{0xff}),
					1,
				)
			}
			if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
				t.Fatal(err)
			}
			response := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
			if response.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
				t.Fatalf(
					"invalid configuration: status=%d source calls=%d",
					response.Code,
					calls.Load(),
				)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("configuration diagnostic leaked")
			}
		})
	}
	for _, path := range []string{"/export", "/caf\u00e9", "/export%23part"} {
		t.Run("valid"+path, func(t *testing.T) {
			t.Parallel()
			source := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}),
			)
			defer source.Close()
			service, configPath := fixture(t, source)
			writeSecret(t, configPath, source.URL+path, "read-only-token")
			if got := request(
				service.PublicHandler(),
				http.MethodGet,
				"/api/data",
				"",
			); got.Code != 200 {
				t.Fatalf("valid endpoint failed: %d", got.Code)
			}
		})
	}
}

func TestSourceConfigFIFOReleasesQueryAndProbeCapacity(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"query", "probe"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir() + "/config"
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewService(path, func(context.Context) bool { return true })
			t.Cleanup(service.Close)
			handler, endpoint := service.PublicHandler(), "/api/data"
			if operation == "probe" {
				handler, endpoint = service.ManagementHandler(), "/readyz"
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(handler, http.MethodGet, endpoint, "") }()
			var response *httptest.ResponseRecorder
			prompt := true
			select {
			case response = <-done:
			case <-time.After(400 * time.Millisecond):
				prompt = false
				// Release a broken blocking opener so the regression never strands a worker.
				// #nosec G304 G703 -- This FIFO is created only in the test's private temporary directory.
				writer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write([]byte("{}"))
				_ = writer.Close()
				select {
				case response = <-done:
				case <-time.After(time.Second):
					t.Fatal("FIFO worker did not terminate")
				}
			}
			if !prompt || response.Code != 503 || len(service.queries) != 0 ||
				len(service.probes) != 0 {
				t.Fatalf(
					"FIFO prompt=%t status=%d occupied query/probe=%d/%d",
					prompt,
					response.Code,
					len(service.queries),
					len(service.probes),
				)
			}
		})
	}
}

func TestCancelledQueryPreservesCompletedSourceHealth(t *testing.T) {
	t.Parallel()
	var phase atomic.Int32
	started, cancelled := make(chan struct{}), make(chan struct{})
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if phase.Load() == 1 {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer source.Close()
	service, _ := fixture(t, source)
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 200 {
		t.Fatal("initial healthy query failed")
	}
	before := sourceHealthMetrics(t, service)
	if before.ready != 1 || before.observed <= 0 {
		t.Fatal("healthy source metrics did not record the completed initial query")
	}
	phase.Store(1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		service.PublicHandler().
			ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/data", nil))
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not reach source")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled query retained worker")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("source request was not cancelled")
	}
	after := sourceHealthMetrics(t, service)
	if after != before {
		t.Fatalf("caller cancellation changed completed health: before=%v after=%v", before, after)
	}
	if len(service.queries) != 0 {
		t.Fatal("caller cancellation retained query capacity")
	}
	metrics := request(service.ManagementHandler(), http.MethodGet, "/metrics", "").Body.String()
	if !strings.Contains(
		metrics,
		`http_source_requests_total{operation="query",result="cancelled"} 1`,
	) {
		t.Fatal("missing bounded caller-cancellation count")
	}
}

func TestConnectorTimeoutWithdrawsCompletedSourceHealth(t *testing.T) {
	t.Parallel()
	var hold atomic.Bool
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold.Load() {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer source.Close()
	service, _ := fixture(t, source)
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 200 {
		t.Fatal("initial healthy query failed")
	}
	hold.Store(true)
	service.client.Timeout = 50 * time.Millisecond
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 502 {
		t.Fatalf("connector timeout status=%d", got.Code)
	}
	if got := sourceHealthMetrics(t, service); got.ready != 0 {
		t.Fatal("connector timeout did not withdraw source readiness")
	}
}

func TestCancellationAtResponseBoundaryPreservesSourceHealth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, contentType, body string
		status                  int
		cancelOnBody            bool
	}{
		{"status", "application/json", `{}`, 503, false},
		{"headers", "text/plain", `{}`, 200, false},
		{"empty-body", "application/json", ``, 200, true},
		{"invalid-body", "application/json", `{`, 200, true},
		{"valid-body", "application/json", `{}`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{}`)
				}),
			)
			defer source.Close()
			service, _ := fixture(t, source)
			if got := request(
				service.PublicHandler(),
				http.MethodGet,
				"/api/data",
				"",
			); got.Code != 200 {
				t.Fatal("initial healthy query failed")
			}
			before := sourceHealthMetrics(t, service)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service.client.CloseIdleConnections()
			service.client.Transport = responseBoundaryTransport(
				func(*http.Request) (*http.Response, error) {
					body := io.NopCloser(strings.NewReader(test.body))
					if test.cancelOnBody {
						body = &cancelledResponseBody{ReadCloser: body, cancel: cancel}
					} else {
						cancel()
					}
					return &http.Response{
						StatusCode: test.status,
						Header:     http.Header{"Content-Type": []string{test.contentType}},
						Body:       body,
					}, nil
				},
			)
			service.PublicHandler().ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/data", nil))
			metrics := request(
				service.ManagementHandler(),
				http.MethodGet,
				"/metrics",
				"",
			).Body.String()
			if after := sourceHealthMetrics(t, service); after != before ||
				!strings.Contains(
					metrics,
					`http_source_requests_total{operation="query",result="cancelled"} 1`,
				) ||
				len(service.queries) != 0 {
				t.Fatalf(
					"response-boundary cancellation changed health or retained work: before=%v after=%v metrics=%s",
					before,
					after,
					metrics,
				)
			}
		})
	}
}

type responseBoundaryTransport func(*http.Request) (*http.Response, error)

func (transport responseBoundaryTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

type cancelledResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelledResponseBody) Read(buffer []byte) (int, error) {
	body.cancel()
	return body.ReadCloser.Read(buffer)
}

type completedSourceHealth struct{ ready, observed float64 }

func sourceHealthMetrics(t *testing.T, service *Service) completedSourceHealth {
	t.Helper()
	families, err := service.metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var result completedSourceHealth
	for _, family := range families {
		switch family.GetName() {
		case "http_source_ready":
			result.ready = family.GetMetric()[0].GetGauge().GetValue()
		case "http_source_last_observation_timestamp_seconds":
			result.observed = family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	return result
}
