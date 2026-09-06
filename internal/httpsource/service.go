// Package httpsource serves a bounded, read-only export from an existing HTTPS source.
package httpsource

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	requestTimeout   = 5 * time.Second
	maxResponseBytes = 1 << 20
)

// Service separates the source's public query API from management endpoints.
type Service struct {
	client     *http.Client
	enabled    func(context.Context) bool
	configPath string
	queries    chan struct{}
	probes     chan struct{}
	metrics    *prometheus.Registry
	requests   *prometheus.CounterVec
	ready      prometheus.Gauge
	observed   prometheus.Gauge
}

// NewService constructs a default-off source connector.
func NewService(configPath string, enabled func(context.Context) bool) *Service {
	metrics := prometheus.NewRegistry()
	service := &Service{
		configPath: configPath,
		enabled:    enabled,
		queries:    make(chan struct{}, 4),
		probes:     make(chan struct{}, 1),
		metrics:    metrics,
		client: &http.Client{
			Timeout:       requestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				// A source credential must never be sent through an ambient HTTP proxy.
				Proxy:                  nil,
				DialContext:            (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
				TLSHandshakeTimeout:    3 * time.Second,
				ResponseHeaderTimeout:  requestTimeout,
				IdleConnTimeout:        30 * time.Second,
				MaxIdleConns:           5,
				MaxIdleConnsPerHost:    5,
				MaxConnsPerHost:        5,
				MaxResponseHeaderBytes: 16 << 10,
				DisableCompression:     true,
			},
		},
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_source_requests_total",
			Help: "Bounded source accesses by operation and result.",
		}, []string{"operation", "result"}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_source_ready",
			Help: "Whether the most recent completed source access succeeded; zero before observation.",
		}),
		observed: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_source_last_observation_timestamp_seconds",
			Help: "Unix time of the most recent completed source access or configuration check.",
		}),
	}
	metrics.MustRegister(service.requests, service.ready, service.observed)
	return service
}

// PublicHandler serves the data API and its external contract.
func (s *Service) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", s.query)
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if !s.isEnabled(r.Context()) {
			http.NotFound(w, r)
			return
		}
		if !readRequest(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAPI)
	})
	return responseBoundary(mux)
}

// ManagementHandler serves health probes and metrics without data access.
func (s *Service) ManagementHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if readRequest(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})
	mux.HandleFunc("/readyz", s.readiness)
	metrics := promhttp.HandlerFor(s.metrics, promhttp.HandlerOpts{})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if readRequest(w, r) {
			metrics.ServeHTTP(w, r)
		}
	})
	return responseBoundary(mux)
}

// Close releases the connector's idle source connections.
func (s *Service) Close() { s.client.CloseIdleConnections() }

// isEnabled treats an absent release-flag evaluator as disabled.
func (s *Service) isEnabled(ctx context.Context) bool { return s.enabled != nil && s.enabled(ctx) }

// query publishes a complete validated export or a fixed error without upstream details.
func (s *Service) query(w http.ResponseWriter, r *http.Request) {
	if !s.isEnabled(r.Context()) {
		s.record("query", "disabled")
		http.NotFound(w, r)
		return
	}
	if !readRequest(w, r) {
		return
	}
	body, result := s.fetch(r.Context(), s.queries)
	s.record("query", result)
	switch result {
	case "success":
		w.Header().Set("Content-Type", "application/json")
		// #nosec G705 -- fetch validates complete UTF-8 JSON before publication. The response
		// is application/json with nosniff, never HTML; preserving bytes keeps its size bounded.
		_, _ = w.Write(body)
	case "configuration_error", "busy":
		http.Error(w, "SourceUnavailable", http.StatusServiceUnavailable)
	default:
		http.Error(w, "SourceUnavailable", http.StatusBadGateway)
	}
}

// readiness observes source access independently of consumer query capacity and discards source data.
func (s *Service) readiness(w http.ResponseWriter, r *http.Request) {
	if !readRequest(w, r) {
		return
	}
	result := "disabled"
	if s.isEnabled(r.Context()) {
		// A dedicated slot keeps query saturation from removing healthy pods from data routing.
		_, result = s.fetch(r.Context(), s.probes)
	}
	s.record("probe", result)
	w.Header().Set("Content-Type", "application/json")
	if result == "success" {
		_, _ = io.WriteString(w, `{"ready":true,"reason":"SourceReady"}`)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	if result == "disabled" {
		_, _ = io.WriteString(w, `{"ready":false,"reason":"FeatureDisabled"}`)
		return
	}
	_, _ = io.WriteString(w, `{"ready":false,"reason":"SourceUnavailable"}`)
}

// fetch uses fresh Secret configuration for one bounded source read, without queueing excess work.
func (s *Service) fetch(ctx context.Context, slots chan struct{}) ([]byte, string) {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return nil, "busy"
	}
	config, err := readConfig(s.configPath)
	if err != nil {
		return nil, "configuration_error"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.endpointURL, nil)
	if err != nil {
		return nil, "configuration_error"
	}
	request.Header.Set("Authorization", "Bearer "+config.bearerToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	// #nosec G704 -- Only a validated HTTPS URL from the operator's Secret is used. Callers
	// cannot choose destinations; the workload's NetworkPolicy restricts source egress.
	response, err := s.client.Do(request)
	if err != nil {
		return nil, "upstream_error"
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, "upstream_error"
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	encoding := response.Header.Get("Content-Encoding")
	if err != nil || contentType != "application/json" ||
		(encoding != "" && encoding != "identity") ||
		response.ContentLength > maxResponseBytes {
		return nil, "invalid_response"
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, "upstream_error"
	}
	if len(body) > maxResponseBytes || !utf8.Valid(body) || !json.Valid(body) {
		return nil, "invalid_response"
	}
	return body, "success"
}

// record updates fixed-label counters and readiness while preserving the last observation when busy.
func (s *Service) record(operation, result string) {
	s.requests.WithLabelValues(operation, result).Inc()
	if result == "busy" {
		return
	}
	s.observed.SetToCurrentTime()
	if result == "success" {
		s.ready.Set(1)
	} else {
		s.ready.Set(0)
	}
}

// readRequest rejects writes and caller-supplied query or body inputs before contacting a source.
func readRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "ReadOnly", http.StatusMethodNotAllowed)
		return false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 ||
		len(r.TransferEncoding) != 0 {
		http.Error(w, "QueryParametersAndBodiesNotSupported", http.StatusBadRequest)
		return false
	}
	return true
}

// responseBoundary prevents caching and MIME sniffing for successful and rejected requests alike.
func responseBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
