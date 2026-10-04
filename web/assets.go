// Package web distributes the portable UI protocol and a static compatibility host.
// It has no dependency on Kubernetes or the registry implementation.
package web

import (
	"embed"
	"net/http"
	"strings"
)

// Assets contains the independently hostable compatibility kit and browser library.
//
//go:embed *.js *.html *.css
var Assets embed.FS

// KitHandler serves the portable kit behind its caller's release flag with a restrictive host policy.
func KitHandler(enabled func() bool) http.Handler {
	return KitHandlerWithAppearance(enabled, func() bool { return false })
}

// KitHandlerWithAppearance advertises an independently default-off appearance grant without network requests.
func KitHandlerWithAppearance(enabled, appearanceEnabled func() bool) http.Handler {
	return KitHandlerWithOptions(
		KitOptions{ContractEnabled: enabled, AppearanceEnabled: appearanceEnabled},
	)
}

// KitOptions holds independently default-off presentation and offline discovery release gates.
type KitOptions struct {
	ContractEnabled   func() bool
	AppearanceEnabled func() bool
	DiscoveryEnabled  func() bool
	PublisherEnabled  func() bool
}

// KitHandlerWithOptions enables offline descriptor handoff without introducing registry or network access.
func KitHandlerWithOptions(options KitOptions) http.Handler {
	files := http.FileServerFS(Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publisher := options.PublisherEnabled != nil && options.PublisherEnabled()
		contract := options.ContractEnabled != nil && options.ContractEnabled()
		reportAsset, sharedAsset, kitAsset := false, false, false
		switch r.URL.Path {
		case "/publisher-review",
			"/publisher.html",
			"/publisher.css",
			"/publisher.js",
			"/preflight-report.js":
			reportAsset = true
		case "/ui-contract.js", "/descriptor.js":
			sharedAsset = true
		case "/", "/index.html", "/kit.css", "/kit.js":
			kitAsset = true
		}
		if !(publisher && reportAsset || (publisher || contract) && sharedAsset || contract && kitAsset) ||
			(r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		frames := "'none'"
		if contract && !reportAsset {
			frames = "https:"
		}
		w.Header().
			Set("Content-Security-Policy", "default-src 'self'; connect-src 'none'; frame-src "+frames+"; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/publisher-review" || r.URL.Path == "/publisher.html" {
			page, err := Assets.ReadFile("publisher.html")
			if err != nil {
				http.Error(w, "publisher review unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == http.MethodGet {
				_, _ = w.Write(page)
			}
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := Assets.ReadFile("index.html")
			if err != nil {
				http.Error(w, "kit unavailable", http.StatusInternalServerError)
				return
			}
			body := "<body"
			if options.AppearanceEnabled != nil && options.AppearanceEnabled() {
				body += ` data-appearance-enabled="true"`
			}
			if options.DiscoveryEnabled != nil && options.DiscoveryEnabled() {
				body += ` data-discovery-enabled="true"`
			}
			page = []byte(strings.Replace(string(page), "<body>", body+">", 1))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method == http.MethodGet {
				_, _ = w.Write(page)
			}
			return
		}
		files.ServeHTTP(w, r)
	})
}
