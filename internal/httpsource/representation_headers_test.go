package httpsource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompleteRepresentationHeaders(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		types, codings []string
		status         int
	}{
		"ordinary":          {[]string{"application/json"}, nil, 200},
		"identity":          {[]string{"application/json; charset=utf-8"}, []string{"identity"}, 200},
		"second-coding":     {[]string{"application/json"}, []string{"identity", "gzip"}, 502},
		"repeated-identity": {[]string{"application/json"}, []string{"identity", "identity"}, 502},
		"coding-list":       {[]string{"application/json"}, []string{"identity, gzip"}, 502},
		"second-type":       {[]string{"application/json", "text/html"}, nil, 502},
		"repeated-type":     {[]string{"application/json", "application/json"}, nil, 502},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					for _, value := range tc.types {
						w.Header().Add("Content-Type", value)
					}
					for _, value := range tc.codings {
						w.Header().Add("Content-Encoding", value)
					}
					_, _ = io.WriteString(w, `{"items":[]}`)
				}),
			)
			defer upstream.Close()
			service, _ := fixture(t, upstream)
			response := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
			if response.Code != tc.status {
				t.Fatalf("representation response = %d, want %d", response.Code, tc.status)
			}
		})
	}
}
