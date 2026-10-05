package contractprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type pausedObservationGauge struct {
	prometheus.Gauge
	entered, release chan struct{}
}

func (g *pausedObservationGauge) SetToCurrentTime() {
	g.Set(200)
	close(g.entered)
	<-g.release
}

// TestMetricsScrapeKeepsCompletedObservation forces a scrape inside the result publication.
func TestMetricsScrapeKeepsCompletedObservation(t *testing.T) {
	t.Parallel()
	s := NewService("https://example.test/schema", func(context.Context) bool { return false })
	defer s.Close()
	s.ready.Set(1)
	s.observed.Set(100)
	paused := &pausedObservationGauge{
		Gauge:   s.observed,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	s.observed = paused
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil),
		)
	}()
	<-paused.entered
	metrics := make(chan string, 1)
	go func() { metrics <- request(t, s, http.MethodGet, "/metrics").Body.String() }()
	select {
	case body := <-metrics:
		if strings.Contains(body, "contract_probe_ready 1") &&
			strings.Contains(body, "contract_probe_last_observation_timestamp_seconds 200") {
			t.Error("scrape combined the previous result with a new failed observation timestamp")
		}
	case <-time.After(100 * time.Millisecond):
	}
	close(paused.release)
	<-done
	body := request(t, s, http.MethodGet, "/metrics").Body.String()
	if !strings.Contains(body, "contract_probe_ready 0") ||
		!strings.Contains(body, "contract_probe_last_observation_timestamp_seconds 200") ||
		!strings.Contains(body, `contract_probe_requests_total{result="FeatureDisabled"} 1`) {
		t.Fatalf("completed observation did not publish its coherent result: %s", body)
	}
}
