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

func (w *auditBlockedScrapeWriter) Header() http.Header { return w.header }
func (w *auditBlockedScrapeWriter) WriteHeader(int)     {}
func (w *auditBlockedScrapeWriter) Write(b []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return len(b), nil
}

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
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}
	close(w.release)
	<-scraped
	if blocked {
		<-observed
		t.Fatal("completed observation blocked behind an unrelated metrics HTTP response write")
	}
}
