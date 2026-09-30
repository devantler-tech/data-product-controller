// Package web distributes the portable UI protocol and a static compatibility host.
// It has no dependency on Kubernetes or the registry implementation.
package web

import (
	"embed"
	"net/http"
)

// Assets contains the independently hostable compatibility kit and browser library.
//
//go:embed *.js *.html *.css
var Assets embed.FS

// KitHandler serves the portable kit behind its caller's release flag with a restrictive host policy.
func KitHandler(enabled func() bool) http.Handler {
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
		files.ServeHTTP(w, r)
	})
}
