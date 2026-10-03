package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestHybridInvalidSelectionRejectsBeforeReads fences unsupported combinations and operator credentials.
func TestHybridInvalidSelectionRejectsBeforeReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		mutate       func(*datav1alpha1.ProvisionedSource)
	}{
		{"SQL hybrid", "EngineProviderUnsupported", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Type = "sql" }},
		{"native provider", "EngineProviderUnsupported", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Provider = "native" }},
		{"legacy adapter", "EngineProviderUnsupported", func(s *datav1alpha1.ProvisionedSource) { s.Adapter = "cnpg/v1" }},
		{"unknown contract", "EngineProviderUnsupported", func(s *datav1alpha1.ProvisionedSource) { s.Engine.APIVersion = "engine-provider/v2" }},
		{"unknown API", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.APIVersion = "postgresql.cnpg.io/v2" }},
		{"wrong resource", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.Kind = "Database" }},
		{"missing source", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.Name = "" }},
		{"missing publication", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "" }},
		{"bootstrap owner", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-app" }},
		{"superuser", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-superuser" }},
		{"replication", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-replication" }},
		{"server key", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-server" }},
		{"authority key", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-ca" }},
		{"client key", "SourceInvalid", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-client" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) },
				),
			)
			t.Cleanup(server.Close)
			source := hybridDocumentSource()
			tc.mutate(&source)
			got := (&Registry{Reader: cnpgReader(t, server.URL)}).Observe(
				t.Context(),
				"products",
				source,
			)
			if got.Ready || got.Reason != tc.reason || calls.Load() != 0 {
				t.Fatalf(
					"observation=%+v reads=%d, want %s without reads",
					got,
					calls.Load(),
					tc.reason,
				)
			}
		})
	}
}

// TestHybridDocumentObservation requires fresh exact-name reads and a generation-bound read-only publication.
func TestHybridDocumentObservation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		mutate       func(*unstructured.Unstructured, *metav1.PartialObjectMetadata)
		secretReads  int32
	}{
		{name: "JSONB reader", reason: "SourceReady", secretReads: 1},
		{name: "previous running image", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, "ghcr.io/cloudnative-pg/postgresql:16", "status", "image")
		}},
		{name: "running image not reported", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			unstructured.RemoveNestedField(c.Object, "status", "image")
		}},
		{name: "mutable image", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			setHybridImage(t, c, "ghcr.io/cloudnative-pg/postgresql:17.11-minimal-trixie")
		}},
		{name: "external image", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			setHybridImage(t, c, "example.invalid/postgresql@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712")
		}},
		{name: "different release digest", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			setHybridImage(t, c, "ghcr.io/cloudnative-pg/postgresql@sha256:"+strings.Repeat("a", 64))
		}},
		{name: "wrong tag with authentic digest", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			setHybridImage(t, c, "ghcr.io/cloudnative-pg/postgresql:17.12-minimal-trixie@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712")
		}},
		{name: "untagged supported digest", reason: "SourceReady", secretReads: 1, mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			setHybridImage(t, c, "ghcr.io/cloudnative-pg/postgresql@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712")
		}},
		{name: "catalog indirection", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, map[string]any{"name": "mutable-catalog", "major": int64(17)}, "spec", "imageCatalogRef")
		}},
		{name: "null catalog declaration", reason: "SourceProfileUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, nil, "spec", "imageCatalogRef")
		}},
		{name: "unready source", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, int64(0), "status", "readyInstances")
		}},
		{name: "deleting source", reason: "SourceDeleting", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			c.SetDeletionTimestamp(&now)
		}},
		{name: "no publication contract", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) { s.Annotations = nil }},
		{name: "writer publication", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.Annotations["data.devantler.tech/cnpg-hybrid-access"] = "read-write"
		}},
		{name: "privileged user", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.Annotations["data.devantler.tech/cnpg-hybrid-user"] = "postgres"
		}},
		{name: "unknown capability", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.Annotations["data.devantler.tech/cnpg-hybrid-capability"] = "jsonb/v2"
		}},
		{name: "graph capability on document reader", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.Annotations["data.devantler.tech/cnpg-hybrid-capability"] = "age/1.7.0"
		}},
		{name: "invalid table", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.Annotations["data.devantler.tech/cnpg-hybrid-table"] = "documents;drop"
		}},
		{name: "stale source generation", reason: "ConnectionPublicationUnsupported", secretReads: 1, mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetGeneration(4) }},
		{name: "recreated source", reason: "ConnectionOwnerMismatch", secretReads: 1, mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("replacement-uid") }},
		{name: "wrong owner API", reason: "ConnectionOwnerMismatch", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].APIVersion = "example.invalid/v1"
		}},
		{name: "deleted publication", reason: "ConnectionNotPublished", secretReads: 1, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			s.DeletionTimestamp = &now
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster, secret := hybridDocumentFixture(t)
			if tc.mutate != nil {
				tc.mutate(cluster, secret)
			}
			var clusterReads, secretReads atomic.Int32
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						t.Errorf("provider mutation: %s", r.Method)
						w.WriteHeader(405)
						return
					}
					var value any
					switch r.URL.Path {
					case "/apis/postgresql.cnpg.io/v1/namespaces/products/clusters/warehouse":
						clusterReads.Add(1)
						value = cluster
					case "/api/v1/namespaces/products/secrets/warehouse-reader":
						secretReads.Add(1)
						if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
							t.Error("Secret values requested")
							w.WriteHeader(406)
							return
						}
						value = secret
					default:
						t.Errorf("unexpected read: %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					if err := json.NewEncoder(w).Encode(value); err != nil {
						t.Error(err)
					}
				}),
			)
			t.Cleanup(server.Close)
			provider := &Registry{Reader: cnpgReader(t, server.URL)}
			for range 2 {
				got := provider.Observe(t.Context(), "products", hybridDocumentSource())
				if got.Reason != tc.reason || got.Ready != (tc.reason == "SourceReady") {
					t.Fatalf("observation=%+v, want %s", got, tc.reason)
				}
			}
			if clusterReads.Load() != 2 || secretReads.Load() != 2*tc.secretReads {
				t.Fatalf("reads: Cluster=%d Secret=%d", clusterReads.Load(), secretReads.Load())
			}
		})
	}
}

// setHybridImage keeps desired and reported images equal so allow-list tests reach the profile check.
func setHybridImage(t *testing.T, cluster *unstructured.Unstructured, image string) {
	t.Helper()
	for _, path := range [][]string{{"spec", "imageName"}, {"status", "image"}} {
		if err := unstructured.SetNestedField(cluster.Object, image, path...); err != nil {
			t.Fatal(err)
		}
	}
}

// hybridDocumentSource uses a separate application reader rather than the bootstrap owner's generated Secret.
func hybridDocumentSource() datav1alpha1.ProvisionedSource {
	source := cnpgSource()
	source.Adapter = "cnpg-hybrid/v1"
	source.Engine.Type = "document"
	source.Engine.Provider = "cnpg-hybrid"
	source.ConnectionSecretRef.Name = "warehouse-reader"
	return source
}

// hybridDocumentFixture binds publisher-declared capability to the observed source generation and UID.
func hybridDocumentFixture(
	t *testing.T,
) (*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	t.Helper()
	cluster, secret := cnpgFixture(t)
	_ = unstructured.SetNestedField(
		cluster.Object,
		"ghcr.io/cloudnative-pg/postgresql:17.11-minimal-trixie@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712",
		"spec",
		"imageName",
	)
	_ = unstructured.SetNestedField(
		cluster.Object,
		"ghcr.io/cloudnative-pg/postgresql:17.11-minimal-trixie@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712",
		"status",
		"image",
	)
	secret.Name = "warehouse-reader"
	secret.Annotations = map[string]string{
		"data.devantler.tech/cnpg-hybrid-publication":       "v1",
		"data.devantler.tech/cnpg-hybrid-access":            "read-only",
		"data.devantler.tech/cnpg-hybrid-capability":        "jsonb/v1",
		"data.devantler.tech/cnpg-hybrid-source-generation": "3",
		"data.devantler.tech/cnpg-hybrid-database":          "catalog",
		"data.devantler.tech/cnpg-hybrid-user":              "document_reader",
		"data.devantler.tech/cnpg-hybrid-schema":            "public",
		"data.devantler.tech/cnpg-hybrid-table":             "documents",
		"data.devantler.tech/cnpg-hybrid-column":            "payload",
	}
	return cluster, secret
}
