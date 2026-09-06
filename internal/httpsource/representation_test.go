package httpsource

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJSONRepresentationStaysBounded keeps HTML-like data in a JSON context without expanding the export.
func TestJSONRepresentationStaysBounded(t *testing.T) {
	t.Parallel()
	payload := `"` + strings.Repeat("<", (1<<20)-2) + `"`
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer source.Close()
	service, _ := fixture(t, source)
	got := request(service.PublicHandler(), http.MethodGet, "/api/data", "")
	if got.Code != 200 || got.Body.Len() > 1<<20 {
		t.Fatalf(
			"export status=%d bytes=%d, want successful JSON within 1 MiB",
			got.Code,
			got.Body.Len(),
		)
	}
	if got.Header().Get("Content-Type") != "application/json" ||
		got.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("HTML-like data must remain in a non-sniffable JSON response")
	}
	var value string
	if err := json.Unmarshal(got.Body.Bytes(), &value); err != nil || len(value) != (1<<20)-2 {
		t.Fatal("source JSON value changed")
	}
}
