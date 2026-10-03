package registry

import (
	"embed"
	"net/http"
	"net/url"
)

//go:embed schema/*.json
var discoverySchemas embed.FS

// discoverySchema serves bundled contracts without fetching schemas or contexts from the network.
func (s *server) discoverySchema(writer http.ResponseWriter, request *http.Request) {
	if !s.discoveryAllowed(writer, request) {
		return
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) > 1 || len(values["type"]) > 1 {
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-schema",
			"Select descriptor or discovery schema.",
		)
		return
	}
	name := values.Get("type")
	if name == "" && len(values) == 0 {
		name = "descriptor"
	}
	if name != "descriptor" && name != "discovery" {
		discoveryFailure(
			writer,
			http.StatusBadRequest,
			"invalid-schema",
			"Select descriptor or discovery schema.",
		)
		return
	}
	var data []byte
	switch name {
	case "descriptor":
		data, err = discoverySchemas.ReadFile("schema/descriptor-v1.json")
	case "discovery":
		data, err = discoverySchemas.ReadFile("schema/discovery-v1.json")
	}
	if err != nil {
		readFailure(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/schema+json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = writer.Write(data)
}
