package contractprobe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type cancelTransport func(*http.Request) (*http.Response, error)

func (f cancelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cancellingBody struct {
	reader    io.Reader
	cancel    context.CancelFunc
	atClose   bool
	readError bool
}

func (b *cancellingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if !b.atClose {
		b.cancel()
		if b.readError {
			return n, context.Canceled
		}
	}
	return n, err
}

func (b *cancellingBody) Close() error {
	if b.atClose {
		b.cancel()
	}
	return nil
}

// TestCallerCancellationKeepsCompletedObservation covers successful and failing completion boundaries.
func TestCallerCancellationKeepsCompletedObservation(t *testing.T) {
	for _, boundary := range []string{"before", "transport-error", "headers", "read-error", "read-success", "close"} {
		t.Run(boundary, func(t *testing.T) {
			upstream := httptest.NewTLSServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "contract") },
				),
			)
			defer upstream.Close()
			service := trustedService(t, upstream)
			if got := request(t, service, "GET", "/readyz"); got.Code != 200 {
				t.Fatal(got.Body)
			}
			before := observationLines(request(t, service, "GET", "/metrics").Body.String())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if boundary == "before" {
				cancel()
			}
			service.client.Transport = cancelTransport(func(*http.Request) (*http.Response, error) {
				if boundary == "transport-error" {
					cancel()
					return nil, errors.New("transport failed")
				}
				response := &http.Response{
					StatusCode: 200,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("contract")),
				}
				switch boundary {
				case "headers":
					cancel()
					response.StatusCode = 503
				case "read-error", "read-success", "close":
					response.Body = &cancellingBody{
						reader:    strings.NewReader("contract"),
						cancel:    cancel,
						atClose:   boundary == "close",
						readError: boundary == "read-error",
					}
				}
				return response, nil
			})
			response := httptest.NewRecorder()
			service.ServeHTTP(response, httptest.NewRequestWithContext(ctx, "GET", "/readyz", nil))
			after := observationLines(request(t, service, "GET", "/metrics").Body.String())
			if after != before {
				t.Fatalf(
					"caller cancellation replaced completed health: before=%q after=%q",
					before,
					after,
				)
			}
			if len(service.slots) != 0 {
				t.Fatal("cancelled probe retained capacity")
			}
			service.client.Transport = cancelTransport(
				func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			)
			if got := request(
				t,
				service,
				"GET",
				"/readyz",
			); got.Code != 503 ||
				!strings.Contains(got.Body.String(), "ContractUnavailable") {
				t.Fatal("independent timeout lost its failure")
			}
			if !strings.Contains(
				observationLines(request(t, service, "GET", "/metrics").Body.String()),
				"contract_probe_ready 0",
			) {
				t.Fatal("timeout did not withdraw health")
			}
		})
	}
}

func observationLines(metrics string) string {
	var result []string
	for line := range strings.SplitSeq(metrics, "\n") {
		if strings.HasPrefix(line, "contract_probe_ready ") ||
			strings.HasPrefix(line, "contract_probe_last_observation_timestamp_seconds ") {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
