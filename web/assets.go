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
	files := http.FileServerFS(Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		w.Header().
			Set("Content-Security-Policy", "default-src 'self'; connect-src 'none'; frame-src https:; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := Assets.ReadFile("index.html")
			if err != nil {
				http.Error(w, "kit unavailable", http.StatusInternalServerError)
				return
			}
			if appearanceEnabled != nil && appearanceEnabled() {
				page = []byte(
					strings.Replace(
						string(page),
						"<body>",
						`<body data-appearance-enabled="true">`,
						1,
					),
				)
			}
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
