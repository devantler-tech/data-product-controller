package contractprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type auditBlockedScrapeWriter struct {
	header           http.Header
	entered, release chan struct{}
	first            bool
}

// Header supplies the metrics response headers without releasing its blocked body write.
func (w *auditBlockedScrapeWriter) Header() http.Header { return w.header }

// WriteHeader accepts the response status while leaving the body-write barrier intact.
func (w *auditBlockedScrapeWriter) WriteHeader(int) {}

// Write holds the first response body write until the test releases its barrier.
func (w *auditBlockedScrapeWriter) Write(b []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return len(b), nil
}

// TestAuditScrapeResponseWriteDoesNotBlockObservation keeps readiness independent of a stalled metrics client.
func TestAuditScrapeResponseWriteDoesNotBlockObservation(t *testing.T) {
	s := NewService("https://example.test/schema", func(context.Context) bool { return false })
	defer s.Close()
	w := &auditBlockedScrapeWriter{
		header:  http.Header{},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	scraped := make(chan struct{})
	go func() {
		s.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
		close(scraped)
	}()
	<-w.entered
	observed := make(chan struct{})
	go func() {
		s.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil),
		)
		close(observed)
	}()
	blocked := false
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		blocked = true
	}
	close(w.release)
	<-scraped
	if blocked {
		<-observed
		t.Fatal("completed observation blocked behind an unrelated metrics HTTP response write")
	}
}
