package demoproduct

import (
	"encoding/json"
	"net/http"

	"github.com/devantler-tech/kweft/internal/uibundle"
)

const releaseSourceURL = "https://api.github.com/repos/devantler-tech/ksail/releases"

// registerReleaseSurface publishes an independent browser view of a fixed public source.
// Only the consumer's browser reads GitHub; this process and the controller read no release data.
func registerReleaseSurface(mux *http.ServeMux, options HandlerOptions) {
	bundle, err := uibundle.Load(
		uiFiles,
		"ui/releases.html",
		uibundle.Source{
			FS:          uiFiles,
			Path:        "ui/product.css",
			ContentType: "text/css; charset=utf-8",
		},
		uibundle.Source{
			FS:          uiFiles,
			Path:        "ui/product.js",
			ContentType: "text/javascript; charset=utf-8",
		},
	)
	serve := func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "release interface unavailable", http.StatusInternalServerError)
			return
		}
		connectSources := "'self' " + releaseSourceURL
		if options.PublicBaseURL != "" {
			connectSources = options.PublicBaseURL + "/ksail-releases/ui-contract-config " + releaseSourceURL
		}
		w.Header().
			Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src "+connectSources+"; frame-ancestors https:; object-src 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/ksail-releases/ui" {
			bundle.ServeHTML(w)
			return
		}
		bundle.ServeAsset(w, r, r.PathValue("asset"))
	}
	mux.HandleFunc("GET /ksail-releases/ui", serve)
	mux.HandleFunc("GET /ksail-releases/assets/{asset}", serve)
	mux.HandleFunc(
		"GET /ksail-releases/ui-contract-config",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_ = json.NewEncoder(w).Encode(struct {
				HostOrigins       []string `json:"hostOrigins"`
				AppearanceEnabled bool     `json:"appearanceEnabled"`
			}{options.HostOrigins, options.AppearanceEnabled})
		},
	)
	mux.HandleFunc(
		"GET /ksail-releases/openapi.json",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_, _ = w.Write([]byte(releaseOpenAPI))
		},
	)
}

const releaseOpenAPI = `{"openapi":"3.1.0","info":{"title":"KSail release history","version":"1.0.0","description":"Public release metadata from KSail's existing GitHub API. The product browser reads one default page without credentials; the controller does not read release records."},"servers":[{"url":"https://api.github.com"}],"paths":{"/repos/devantler-tech/ksail/releases":{"get":{"operationId":"listKSailReleases","summary":"Read published KSail releases","responses":{"200":{"description":"One page of public release metadata. GitHub's own API controls pagination and rate limits.","content":{"application/json":{"schema":{"type":"array","items":{"type":"object","required":["tag_name","published_at","draft","prerelease"],"properties":{"tag_name":{"type":"string"},"published_at":{"type":["string","null"],"format":"date-time"},"draft":{"type":"boolean"},"prerelease":{"type":"boolean"}}}}}}},"403":{"description":"The public source is unavailable or its request allowance is exhausted."},"429":{"description":"The public source has limited requests."}}}}}}`
