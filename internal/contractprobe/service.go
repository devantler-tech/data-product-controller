// Package contractprobe observes an operator-selected public contract outside the controller.
package contractprobe

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Service serves private contract reachability probes and metrics.
type Service struct {
	client   *http.Client
	enabled  func(context.Context) bool
	target   string
	slots    chan struct{}
	metrics  http.Handler
	requests *prometheus.CounterVec
	ready    prometheus.Gauge
	observed prometheus.Gauge
}

// NewService constructs a default-off probe with fixed network and resource boundaries.
func NewService(target string, enabled func(context.Context) bool) *Service {
	registry := prometheus.NewRegistry()
	service := &Service{
		target:  target,
		enabled: enabled,
		slots:   make(chan struct{}, 1),
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				Proxy:                  nil,
				DialContext:            (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
				TLSHandshakeTimeout:    3 * time.Second,
				ResponseHeaderTimeout:  5 * time.Second,
				IdleConnTimeout:        30 * time.Second,
				MaxConnsPerHost:        1,
				MaxIdleConns:           1,
				MaxIdleConnsPerHost:    1,
				MaxResponseHeaderBytes: 16 << 10,
				DisableCompression:     true,
			},
		},
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "contract_probe_requests_total",
				Help: "Contract probe results with a fixed reason vocabulary.",
			},
			[]string{"result"},
		),
		ready: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "contract_probe_ready",
				Help: "Whether the latest completed contract check succeeded; zero before observation.",
			},
		),
		observed: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "contract_probe_last_observation_timestamp_seconds",
				Help: "Unix time of the latest completed contract check.",
			},
		),
	}
	registry.MustRegister(service.requests, service.ready, service.observed)
	service.metrics = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return service
}

// Close releases idle connections.
func (s *Service) Close() { s.client.CloseIdleConnections() }

// ServeHTTP exposes read-only management endpoints without returning upstream bytes or URLs.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "ReadOnly", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 ||
		len(r.TransferEncoding) != 0 {
		http.Error(w, "QueryParametersAndBodiesNotSupported", http.StatusBadRequest)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
	case "/metrics":
		s.metrics.ServeHTTP(w, r)
	case "/readyz":
		s.readiness(w, r)
	default:
		http.NotFound(w, r)
	}
}

// readiness limits concurrent probes and preserves the last completed observation when busy.
func (s *Service) readiness(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		s.requests.WithLabelValues("ContractProbeBusy").Inc()
		http.Error(w, "ContractProbeBusy", http.StatusServiceUnavailable)
		return
	}
	reason := "FeatureDisabled"
	if s.enabled != nil && s.enabled(r.Context()) {
		reason = s.fetch(r.Context())
	}
	s.requests.WithLabelValues(reason).Inc()
	s.observed.SetToCurrentTime()
	s.ready.Set(0)
	if reason == "ContractReachable" {
		s.ready.Set(1)
		_, _ = io.WriteString(w, "ContractReachable")
		return
	}
	http.Error(w, reason, http.StatusServiceUnavailable)
}

// fetch discards a complete bounded response; the target is immutable operator configuration.
func (s *Service) fetch(ctx context.Context) string {
	target, err := url.Parse(s.target)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || strings.Contains(s.target, "$(") ||
		target.RawQuery != "" || target.ForceQuery ||
		target.Fragment != "" ||
		target.Opaque != "" {
		return "ContractConfigurationInvalid"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.target, nil)
	if err != nil {
		return "ContractConfigurationInvalid"
	}
	request.Header.Set("Accept-Encoding", "identity")
	// #nosec G704 -- A fixed operator-configured HTTPS URL is used, never caller input. TLS,
	// redirects, proxies and size/time/concurrency are bounded; NetworkPolicy scopes egress.
	response, err := s.client.Do(request)
	if err != nil {
		return "ContractUnavailable"
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "ContractUnavailable"
	}
	encoding := response.Header.Get("Content-Encoding")
	if response.ContentLength > 1<<20 || (encoding != "" && encoding != "identity") {
		return "ContractInvalidResponse"
	}
	size, err := io.Copy(io.Discard, io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return "ContractUnavailable"
	}
	if size == 0 || size > 1<<20 {
		return "ContractInvalidResponse"
	}
	return "ContractReachable"
}
