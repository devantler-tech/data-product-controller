package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// TestArangoObservation catches missing Graph dispatch, stale readiness and credential-value reads.
func TestArangoObservation(t *testing.T) {
	t.Parallel()
	cluster, secret := arangoFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Errorf("provider mutation: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var value any
		switch r.URL.Path {
		case "/apis/database.arangodb.com/v1/namespaces/products/arangodeployments/lineage":
			value = cluster
		case "/api/v1/namespaces/products/secrets/lineage-reader":
			if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
				t.Error("Secret values requested")
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			value = secret
		default:
			t.Errorf("unexpected discovery or resource read: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	reader, err := NewEngineReader(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	got := (&Registry{Reader: reader}).Observe(t.Context(), "products", arangoSource())
	if !got.Ready || got.Reason != "SourceReady" {
		t.Fatalf("Graph observation=%+v, want SourceReady", got)
	}
}

// arangoSource selects the same namespaced source and publication as the synthetic fixture.
func arangoSource() datav1alpha1.ProvisionedSource {
	return datav1alpha1.ProvisionedSource{
		Adapter: "arangodb/v1",
		Engine: &datav1alpha1.EngineSelection{
			APIVersion: "engine-provider/v1",
			Type:       "graph",
			Provider:   "native",
		},
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "database.arangodb.com/v1",
			Kind:       "ArangoDeployment",
			Name:       "lineage",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "lineage-reader"},
	}
}

// arangoFixture describes synthetic operator status, not an ArangoDB installation.
func arangoFixture(t *testing.T) (*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	t.Helper()
	cluster := &unstructured.Unstructured{}
	data, err := os.ReadFile("../../../tests/source/fixtures/arango-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	secret := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lineage-reader",
			Namespace: "products",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "database.arangodb.com/v1",
					Kind:       "ArangoDeployment",
					Name:       "lineage",
					UID:        "graph-uid",
				},
			},
			Annotations: map[string]string{
				"data.devantler.tech/arango-publication": "v1",
				"data.devantler.tech/arango-user":        "catalog-reader",
				"data.devantler.tech/arango-database":    "catalog",
				"data.devantler.tech/arango-graph":       "lineage",
				"data.devantler.tech/arango-access":      "read-only",
				"data.devantler.tech/arango-collections": "products,relations",
			},
		},
	}
	return cluster, secret
}
