package v1

import (
	"mime"
	"net/http"
	"strings"
)

const metadataOnlyAccept = "application/vnd.kubernetes.protobuf;as=PartialObjectMetadata;g=meta.k8s.io;v=v1,application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1"

// metadataOnlyTransport removes client-go's full-object fallback from single-object metadata GETs.
// Source GETs retain their normal representation. Unsupported metadata negotiation fails closed.
type metadataOnlyTransport struct{ next http.RoundTripper }

// RoundTrip clones only metadata GETs so neither caller requests nor shared headers are modified.
func (t metadataOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet && requestsObjectMetadata(request.Header) {
		request = request.Clone(request.Context())
		request.Header.Set("Accept", metadataOnlyAccept)
	}
	return t.next.RoundTrip(request)
}

// requestsObjectMetadata distinguishes single-object negotiation from list and ordinary representations.
func requestsObjectMetadata(header http.Header) bool {
	for _, value := range header.Values("Accept") {
		for _, entry := range strings.Split(value, ",") {
			_, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
			if err == nil && parameters["as"] == "PartialObjectMetadata" &&
				parameters["g"] == "meta.k8s.io" && parameters["v"] == "v1" {
				return true
			}
		}
	}
	return false
}
