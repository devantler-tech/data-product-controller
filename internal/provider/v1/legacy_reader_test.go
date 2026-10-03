package v1

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestLegacyReaderRefusesFullSecretNegotiation covers the separately discovered Crossplane API path.
func TestLegacyReaderRefusesFullSecretNegotiation(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{true, false} {
		t.Run(
			map[bool]string{true: "metadata supported", false: "full object only"}[partial],
			func(t *testing.T) {
				t.Parallel()
				var sources, secrets, fullObjects atomic.Int32
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						var response string
						switch r.URL.Path {
						case "/apis/database.example.org/v1alpha1/namespaces/products/databases/warehouse":
							sources.Add(1)
							response = `{"apiVersion":"database.example.org/v1alpha1","kind":"Database","metadata":{"name":"warehouse","namespace":"products","uid":"source-uid","generation":3},"spec":{"writeConnectionSecretToRef":{"name":"warehouse-connection"}},"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}}`
						case "/api/v1/namespaces/products/secrets/warehouse-connection":
							secrets.Add(1)
							switch {
							case partial:
								response = `{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"warehouse-connection","namespace":"products","ownerReferences":[{"apiVersion":"database.example.org/v1alpha1","kind":"Database","name":"warehouse","uid":"source-uid"}]}}`
							case permitsFullJSON(r.Header.Get("Accept")):
								fullObjects.Add(1)
								response = `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"warehouse-connection","namespace":"products","ownerReferences":[{"apiVersion":"database.example.org/v1alpha1","kind":"Database","name":"warehouse","uid":"source-uid"}]},"data":{"password":"c3ludGhldGlj"}}`
							default:
								w.WriteHeader(http.StatusNotAcceptable)
								response = `{"apiVersion":"v1","kind":"Status","status":"Failure","code":406}`
							}
						default:
							t.Errorf("unexpected API request: %s", r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
							return
						}
						if _, err := w.Write([]byte(response)); err != nil {
							t.Error(err)
						}
					}),
				)
				t.Cleanup(server.Close)
				mapper := meta.NewDefaultRESTMapper(
					[]schema.GroupVersion{
						{Group: "database.example.org", Version: "v1alpha1"},
						{Version: "v1"},
					},
				)
				mapper.Add(
					schema.GroupVersionKind{
						Group:   "database.example.org",
						Version: "v1alpha1",
						Kind:    "Database",
					},
					meta.RESTScopeNamespace,
				)
				mapper.Add(
					schema.GroupVersionKind{Version: "v1", Kind: "Secret"},
					meta.RESTScopeNamespace,
				)
				config := &rest.Config{Host: server.URL, Timeout: 17 * time.Second}
				bounded := MetadataOnlyConfig(config)
				if bounded == config || bounded.Timeout != config.Timeout ||
					config.WrapTransport != nil {
					t.Fatal("caller configuration or timeout changed")
				}
				reader, err := client.New(
					bounded,
					client.Options{Scheme: runtime.NewScheme(), Mapper: mapper},
				)
				if err != nil {
					t.Fatal(err)
				}
				registry := Registry{LegacyReader: reader, Mapper: mapper}
				source := datav1alpha1.ProvisionedSource{
					Adapter: "crossplane/v1",
					ResourceRef: datav1alpha1.ProvisionedResourceReference{
						APIVersion: "database.example.org/v1alpha1",
						Kind:       "Database",
						Name:       "warehouse",
					},
					ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{
						Name: "warehouse-connection",
					},
				}
				for range 2 {
					got := registry.Observe(t.Context(), "products", source)
					if got.Ready != partial || (!partial && got.Reason != "SourceUnavailable") {
						t.Errorf("observation=%+v; metadata supported=%v", got, partial)
					}
				}
				if sources.Load() != 2 || secrets.Load() != 2 || fullObjects.Load() != 0 {
					t.Fatalf(
						"sources=%d metadata=%d full=%d; want two fresh pairs and no full object",
						sources.Load(),
						secrets.Load(),
						fullObjects.Load(),
					)
				}
			},
		)
	}
}
