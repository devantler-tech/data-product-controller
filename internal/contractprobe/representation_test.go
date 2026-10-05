package contractprobe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompleteContentCodingHeaders(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		values []string
		reason string
	}{
		"absent":            {nil, "ContractReachable"},
		"identity":          {[]string{"identity"}, "ContractReachable"},
		"second-coding":     {[]string{"identity", "gzip"}, "ContractInvalidResponse"},
		"repeated-identity": {[]string{"identity", "identity"}, "ContractInvalidResponse"},
		"coding-list":       {[]string{"identity, gzip"}, "ContractInvalidResponse"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					for _, value := range tc.values {
						w.Header().Add("Content-Encoding", value)
					}
					_, _ = io.WriteString(w, "contract")
				}),
			)
			defer upstream.Close()
			service := trustedService(t, upstream)
			response := request(t, service, http.MethodGet, "/readyz")
			if !strings.Contains(response.Body.String(), tc.reason) {
				t.Fatalf(
					"coding response = %d %s, want %s",
					response.Code,
					response.Body,
					tc.reason,
				)
			}
		})
	}
}

func TestLiteralProbeTarget(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"/contract#", "/contract\\literal", "/contract%23encoded", "/contract%5Cencoded"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					_, _ = io.WriteString(w, "contract")
				}),
			)
			defer upstream.Close()
			service := trustedService(t, upstream)
			service.target = upstream.URL + suffix
			response := request(t, service, http.MethodGet, "/readyz")
			invalid := strings.ContainsAny(suffix, "#\\")
			reason, count := "ContractReachable", int32(1)
			if invalid {
				reason, count = "ContractConfigurationInvalid", 0
			}
			if !strings.Contains(response.Body.String(), reason) || calls.Load() != count {
				t.Fatalf(
					"literal target response = %d %s, calls %d, want %s/%d",
					response.Code,
					response.Body,
					calls.Load(),
					reason,
					count,
				)
			}
		})
	}
}
