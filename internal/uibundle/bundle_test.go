package uibundle_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/devantler-tech/data-product-controller/internal/uibundle"
)

// TestDocumentReferencesTheExactImmutableAsset prevents stale or mismatched release bytes being served.
func TestDocumentReferencesTheExactImmutableAsset(t *testing.T) {
	t.Parallel()
	var previous string
	for _, script := range []string{"previous release", "current release"} {
		files := fstest.MapFS{
			"index.html": {Data: []byte(`<script src="/assets/app.js"></script>`)},
			"app.js":     {Data: []byte(script)},
		}
		bundle, err := uibundle.Load(files, "index.html", uibundle.Source{
			FS: files, Path: "app.js", ContentType: "text/javascript; charset=utf-8",
		})
		if err != nil {
			t.Fatal(err)
		}
		document := httptest.NewRecorder()
		bundle.ServeHTML(document)
		if document.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("release document may be cached independently of its assets")
		}
		match := regexp.MustCompile(`/assets/(app-[a-f0-9]{64}\.js)`).
			FindStringSubmatch(document.Body.String())
		if len(match) != 2 || match[1] == previous {
			t.Fatalf("changed asset did not get a new fingerprint: %s", document.Body.String())
		}
		previous = match[1]
		for _, name := range []string{match[1], "app.js"} {
			response := httptest.NewRecorder()
			bundle.ServeAsset(
				response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/"+name, nil),
				name,
			)
			if response.Code != http.StatusOK || response.Body.String() != script {
				t.Fatalf("asset %s did not serve the release bytes", name)
			}
			wantCache := "no-store"
			if name == match[1] {
				wantCache = "public, max-age=31536000, immutable"
			}
			if response.Header().Get("Cache-Control") != wantCache ||
				response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
				t.Fatalf("incorrect asset response headers: %v", response.Header())
			}
		}
		for _, unknown := range []string{"app-" + strings.Repeat("0", 64) + ".js", "index.html", "../app.js"} {
			response := httptest.NewRecorder()
			bundle.ServeAsset(
				response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/unknown", nil),
				unknown,
			)
			if response.Code != http.StatusNotFound {
				t.Fatalf("uncompiled asset name %q served with status %d", unknown, response.Code)
			}
		}
	}
}
