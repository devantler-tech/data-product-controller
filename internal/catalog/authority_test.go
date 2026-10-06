package catalog_test

import (
	"net/http"
	"testing"
)

// TestRound13AuditCatalogTransportPorts rejects catalog endpoints without usable TCP ports.
func TestRound13AuditCatalogTransportPorts(t *testing.T) {
	for _, host := range []string{"https://example.test:0/query", "https://example.test:65536/query", "https://example.test:/query"} {
		p := fixture()
		p.Spec.Outputs[0].URL = host
		got := request(t, reader(t, p), true, "urn:example:catalog")
		if got.Code != http.StatusUnprocessableEntity {
			t.Errorf("unusable access endpoint %s returned %d", host, got.Code)
		}
	}
}
