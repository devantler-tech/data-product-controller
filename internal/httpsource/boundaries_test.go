package httpsource

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestQuerySaturationLeavesReadinessCapacity keeps a busy, healthy source eligible for data routing.
func TestQuerySaturationLeavesReadinessCapacity(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 4 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer source.Close()
	defer close(release)
	service, _ := fixture(t, source)
	done := make(chan struct{}, 4)
	for range 4 {
		go func() { request(service.PublicHandler(), http.MethodGet, "/api/data", ""); done <- struct{}{} }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("source requests did not arrive")
		}
	}
	probe := request(service.ManagementHandler(), http.MethodGet, "/readyz", "")
	if probe.Code != http.StatusOK {
		t.Fatalf("query saturation failed readiness: %d %s", probe.Code, probe.Body)
	}
}

// TestProjectedDirectoryRotation follows Kubernetes' stable file symlink through an atomic data-dir swap.
func TestProjectedDirectoryRotation(t *testing.T) {
	t.Parallel()
	var token atomic.Value
	token.Store("Bearer read-only-token")
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != token.Load() {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer source.Close()
	service, original := fixture(t, source)
	root := filepath.Dir(original)
	for _, generation := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(root, generation), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSecret(
		t,
		filepath.Join(root, "first", "config.json"),
		source.URL+"/export",
		"read-only-token",
	)
	writeSecret(
		t,
		filepath.Join(root, "second", "config.json"),
		source.URL+"/export",
		"rotated-token",
	)
	if err := os.Symlink("first", filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/config.json", filepath.Join(root, "projected.json")); err != nil {
		t.Fatal(err)
	}
	service.configPath = filepath.Join(root, "projected.json")
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 200 {
		t.Fatalf("first generation = %d", got.Code)
	}
	token.Store("Bearer rotated-token")
	if err := os.Symlink("second", filepath.Join(root, "..data-new")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(root, "..data-new"),
		filepath.Join(root, "..data"),
	); err != nil {
		t.Fatal(err)
	}
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 200 {
		t.Fatalf("rotated generation = %d", got.Code)
	}
}

// TestAmbientProxyNeverReceivesSourceCredentials exercises a non-local authority through the real client.
func TestAmbientProxyNeverReceivesSourceCredentials(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { proxied.Add(1); w.WriteHeader(502) },
		),
	)
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer read-only-token" {
			t.Error("source did not receive its read-only credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer source.Close()
	service, configPath := fixture(t, source)
	address, err := url.Parse(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("missing source transport")
	}
	transport.TLSClientConfig.ServerName = address.Hostname()
	transport.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		if destination == "source.example.test:443" {
			destination = address.Host
		}
		return (&net.Dialer{}).DialContext(ctx, network, destination)
	}
	writeSecret(t, configPath, "https://source.example.test/export", "read-only-token")
	if got := request(service.PublicHandler(), http.MethodGet, "/api/data", ""); got.Code != 200 {
		t.Fatalf("query = %d", got.Code)
	}
	if proxied.Load() != 0 {
		t.Fatal("source request reached the ambient proxy")
	}
}

// TestStreamingLimitsAndCancellation bounds bodies without Content-Length and cancels disconnected callers.
func TestStreamingLimitsAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("chunked size", func(t *testing.T) {
		t.Parallel()
		source := httptest.NewTLSServer(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				_, _ = io.WriteString(w, `"`+strings.Repeat("x", (1<<20)+1)+`"`)
			}),
		)
		defer source.Close()
		service, _ := fixture(t, source)
		if got := request(
			service.PublicHandler(),
			http.MethodGet,
			"/api/data",
			"",
		); got.Code != 502 ||
			strings.Contains(got.Body.String(), "xxxx") {
			t.Fatalf("oversized stream = %d %s", got.Code, got.Body)
		}
	})
	t.Run("caller cancelled", func(t *testing.T) {
		t.Parallel()
		entered := make(chan struct{})
		cancelled := make(chan struct{})
		source := httptest.NewTLSServer(
			http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
				close(cancelled)
			}),
		)
		defer source.Close()
		service, _ := fixture(t, source)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			service.PublicHandler().
				ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/data", nil))
			close(done)
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("source not contacted")
		}
		cancel()
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("caller cancellation did not reach source")
		}
		<-done
	})
}
