package httpsource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type pausedObservationGauge struct {
	prometheus.Gauge
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (g *pausedObservationGauge) SetToCurrentTime() {
	call := g.calls.Add(1)
	g.Set(float64(call) * 100)
	if call == 1 {
		close(g.entered)
		<-g.release
	}
}

func publicHealthSample(t *testing.T, handler http.Handler) (float64, float64) {
	t.Helper()
	response := request(handler, http.MethodGet, "/metrics", "")
	return responseHealthSample(t, response)
}

func responseHealthSample(t *testing.T, response *httptest.ResponseRecorder) (float64, float64) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", response.Code)
	}
	values := make(map[string]float64)
	for line := range strings.SplitSeq(response.Body.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 ||
			(fields[0] != "http_source_ready" && fields[0] != "http_source_last_observation_timestamp_seconds") {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		values[fields[0]] = value
	}
	if len(values) != 2 {
		t.Fatalf("incomplete health sample: %v", values)
	}
	return values["http_source_ready"], values["http_source_last_observation_timestamp_seconds"]
}

func pausedHealthService(t *testing.T) (*Service, *pausedObservationGauge, func()) {
	t.Helper()
	service := NewService("", func(context.Context) bool { return true })
	gauge := &pausedObservationGauge{
		Gauge:   service.observed,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	service.observed = gauge
	var once sync.Once
	release := func() { once.Do(func() { close(gauge.release) }) }
	t.Cleanup(release)
	t.Cleanup(service.Close)
	return service, gauge, release
}

func TestConcurrentObservationsPublishOneCompletedSample(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"success", "upstream_error"} {
		t.Run(first, func(t *testing.T) {
			t.Parallel()
			service, gauge, release := pausedHealthService(t)
			second, expected := "upstream_error", float64(0)
			if first != "success" {
				second, expected = "success", 1
			}
			firstDone := make(chan struct{})
			go func() { service.record("query", first); close(firstDone) }()
			<-gauge.entered
			secondStarted, secondDone := make(chan struct{}), make(chan struct{})
			go func() { close(secondStarted); service.record("probe", second); close(secondDone) }()
			<-secondStarted
			// The old independent writers can finish the second observation during this pause.
			// A coherent publisher instead blocks it until the first sample is complete.
			select {
			case <-secondDone:
			case <-time.After(500 * time.Millisecond):
			}
			release()
			<-firstDone
			<-secondDone
			ready, observed := publicHealthSample(t, service.ManagementHandler())
			if ready != expected || observed != 200 {
				t.Fatalf(
					"mixed completed sample = (%v,%v), want (%v,200)",
					ready,
					observed,
					expected,
				)
			}
		})
	}
}

func TestMetricsScrapeCannotCollectHalfAnObservation(t *testing.T) {
	t.Parallel()
	service, gauge, release := pausedHealthService(t)
	gauge.Set(50)
	service.ready.Set(0)
	done := make(chan struct{})
	go func() { service.record("query", "success"); close(done) }()
	<-gauge.entered
	scraped := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		scraped <- request(service.ManagementHandler(), http.MethodGet, "/metrics", "")
	}()
	select {
	case got := <-scraped:
		ready, observed := responseHealthSample(t, got)
		if ready != 0 || observed != 50 {
			t.Errorf("scrape exposed unfinished observation: (%v,%v)", ready, observed)
		}
		scraped = nil
	case <-time.After(500 * time.Millisecond):
	}
	release()
	<-done
	if scraped != nil {
		ready, observed := responseHealthSample(t, <-scraped)
		if ready != 1 || observed != 100 {
			t.Errorf("joined scrape = (%v,%v)", ready, observed)
		}
	}
	ready, observed := publicHealthSample(t, service.ManagementHandler())
	if ready != 1 || observed != 100 {
		t.Fatalf("completed scrape = (%v,%v), want (1,100)", ready, observed)
	}
}

type pausedSerializationGauge struct {
	prometheus.Gauge
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *pausedSerializationGauge) Collect(ch chan<- prometheus.Metric) { ch <- g }

func (g *pausedSerializationGauge) Write(metric *dto.Metric) error {
	err := g.Gauge.Write(metric)
	g.once.Do(func() { close(g.entered); <-g.release })
	return err
}

type orderedObservationGauge struct {
	prometheus.Gauge
	readySerialized <-chan struct{}
}

func (g *orderedObservationGauge) Collect(ch chan<- prometheus.Metric) {
	<-g.readySerialized
	ch <- g
}

func (g *orderedObservationGauge) SetToCurrentTime() { g.Set(100) }

func TestMetricsSerializationRetainsOneCompletedSample(t *testing.T) {
	t.Parallel()
	service := NewService("", func(context.Context) bool { return true })
	t.Cleanup(service.Close)
	service.ready.Set(0)
	service.observed.Set(50)
	gauge := &pausedSerializationGauge{
		Gauge:   service.ready,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	observedGauge := &orderedObservationGauge{
		Gauge:           service.observed,
		readySerialized: gauge.entered,
	}
	service.observed = observedGauge
	var once sync.Once
	release := func() { once.Do(func() { close(gauge.release) }) }
	t.Cleanup(release)
	// Pause the real registry after it has serialized readiness. Updating a
	// mutable metric after Collect returns must not alter this scrape's timestamp.
	service.metrics = prometheus.NewRegistry()
	service.metrics.MustRegister(service.requests, gauge, observedGauge)
	scraped := make(chan *httptest.ResponseRecorder, 1)
	go func() { scraped <- request(service.ManagementHandler(), http.MethodGet, "/metrics", "") }()
	<-gauge.entered
	done := make(chan struct{})
	go func() { service.record("query", "success"); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
	release()
	ready, observed := responseHealthSample(t, <-scraped)
	if ready != 0 || observed != 50 {
		t.Errorf("serialization mixed completed samples: (%v,%v), want (0,50)", ready, observed)
	}
	<-done
	ready, observed = publicHealthSample(t, service.ManagementHandler())
	if ready != 1 || observed != 100 {
		t.Fatalf("new completed sample missing: (%v,%v)", ready, observed)
	}
}
