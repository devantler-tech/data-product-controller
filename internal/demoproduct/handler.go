// Package demoproduct provides a self-contained example data product.
package demoproduct

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

//go:embed ui/*
var uiFiles embed.FS

type observation struct {
	Station     string    `json:"station"`
	Temperature float64   `json:"temperatureCelsius"`
	Salinity    float64   `json:"salinityPsu"`
	ObservedAt  time.Time `json:"observedAt"`
}

var observations = []observation{
	{
		Station:     "nordhavn",
		Temperature: 16.8,
		Salinity:    14.2,
		ObservedAt:  time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC),
	},
	{
		Station:     "refshaleoen",
		Temperature: 17.1,
		Salinity:    13.9,
		ObservedAt:  time.Date(2026, time.August, 29, 12, 5, 0, 0, time.UTC),
	},
}

// NewHandler returns the example product's API, contract, and decentralized UI.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/observations", observationsHandler)
	mux.HandleFunc("/openapi.json", openAPIHandler)
	mux.HandleFunc("/ui", uiHandler)
	mux.HandleFunc("/assets/product.css", assetHandler("product.css", "text/css; charset=utf-8"))
	mux.HandleFunc(
		"/assets/product.js",
		assetHandler("product.js", "text/javascript; charset=utf-8"),
	)
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)

			return
		}
		response.WriteHeader(http.StatusOK)
	})

	return mux
}

func observationsHandler(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	station := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("station")))
	items := make([]observation, 0, len(observations))
	for _, item := range observations {
		if station == "" || item.Station == station {
			items = append(items, item)
		}
	}

	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(response).Encode(struct {
		Items []observation `json:"items"`
	}{Items: items})
}

func openAPIHandler(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(openAPIDocument))
}

func uiHandler(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	serveUIFile(response, "index.html", "text/html; charset=utf-8")
}

func assetHandler(name, contentType string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		serveUIFile(response, name, contentType)
	}
}

func serveUIFile(response http.ResponseWriter, name, contentType string) {
	content, err := uiFiles.ReadFile("ui/" + name)
	if err != nil {
		http.Error(response, "product interface unavailable", http.StatusInternalServerError)

		return
	}

	response.Header().Set("Content-Type", contentType)
	response.Header().Set(
		"Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors https:; object-src 'none'; base-uri 'none'",
	)
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(content)
}

const openAPIDocument = `{"openapi":"3.1.0","info":{"title":"Harbour observations","version":"1.0.0"},"paths":{"/api/observations":{"get":{"summary":"Query harbour observations","parameters":[{"name":"station","in":"query","schema":{"type":"string"}}],"responses":{"200":{"description":"Matching observations","content":{"application/json":{"schema":{"type":"object","required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","required":["station","temperatureCelsius","salinityPsu","observedAt"],"properties":{"station":{"type":"string"},"temperatureCelsius":{"type":"number"},"salinityPsu":{"type":"number"},"observedAt":{"type":"string","format":"date-time"}}}}}}}}}}}}}`
