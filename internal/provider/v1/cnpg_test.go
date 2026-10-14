package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCNPGObservation uses a real Kubernetes HTTP client to enforce fresh exact-name GETs and Secret metadata negotiation.
func TestCNPGObservation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason           string
		mutate                 func(*unstructured.Unstructured, *metav1.PartialObjectMetadata)
		sourceCode, secretCode int
	}{
		{name: "ready", reason: "SourceReady"},
		{name: "missing source", sourceCode: 404, reason: "SourceNotFound"},
		{name: "denied source", sourceCode: 403, reason: "SourceAccessDenied"},
		{name: "missing publication", secretCode: 404, reason: "ConnectionNotPublished"},
		{name: "denied publication", secretCode: 403, reason: "SourceAccessDenied"},
		{name: "API failure", sourceCode: 500, reason: "SourceUnavailable"},
		{name: "recreated source", reason: "ConnectionOwnerMismatch", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("new-uid") }},
		{name: "wrong owner group", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].APIVersion = "other.example.org/v1"
		}},
		{name: "missing owner", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) { s.OwnerReferences = nil }},
		{name: "deleting publication", reason: "ConnectionNotPublished", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			s.DeletionTimestamp = &now
		}},
		{name: "deleting source", reason: "SourceDeleting", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			c.SetDeletionTimestamp(&now)
		}},
		{name: "custom bootstrap secret", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			c.Object["spec"] = map[string]any{"instances": int64(1), "bootstrap": map[string]any{"initdb": map[string]any{"secret": map[string]any{"name": "custom"}}}}
		}},
		{name: "incomplete replicas", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, int64(0), "status", "readyInstances")
		}},
		{name: "custom recovery secret", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, map[string]any{"name": "warehouse-app"}, "spec", "bootstrap", "recovery", "secret")
		}},
		{name: "custom base backup secret", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, map[string]any{"name": "warehouse-app"}, "spec", "bootstrap", "pg_basebackup", "secret")
		}},
		{name: "primary changing", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedField(c.Object, "warehouse-2", "status", "targetPrimary")
		}},
		{name: "stale condition", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedSlice(c.Object, []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(2)}}, "status", "conditions")
		}},
		{name: "duplicate ready", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedSlice(c.Object, []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
		}},
		{name: "malformed status", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			c.Object["status"] = "credential-sentinel"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster, secret := cnpgFixture(t)
			if tc.mutate != nil {
				tc.mutate(cluster, secret)
			}
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					paths = append(paths, r.Method+" "+r.URL.Path)
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						t.Errorf("mutation: %s", r.Method)
						w.WriteHeader(405)
						return
					}
					var value any
					var code int
					switch r.URL.Path {
					case "/apis/postgresql.cnpg.io/v1/namespaces/products/clusters/warehouse":
						value = cluster
						code = tc.sourceCode
					case "/api/v1/namespaces/products/secrets/warehouse-app":
						if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
							t.Error("Secret values requested")
							w.WriteHeader(406)
							return
						}
						value = secret
						code = tc.secretCode
					default:
						t.Errorf("unexpected read: %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					if code != 0 {
						w.WriteHeader(code)
						value = map[string]any{
							"kind":       "Status",
							"apiVersion": "v1",
							"status":     "Failure",
							"reason":     "Failure",
							"code":       code,
							"message":    "credential-sentinel",
						}
					}
					if err := json.NewEncoder(w).Encode(value); err != nil {
						t.Error(err)
					}
				}),
			)
			t.Cleanup(server.Close)
			reader := cnpgReader(t, server.URL)
			provider := &Registry{Reader: reader}
			for range 2 {
				got := provider.Observe(t.Context(), "products", cnpgSource())
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if got.Reason != tc.reason || got.Ready != (tc.reason == "SourceReady") ||
					strings.Contains(string(encoded), "credential-sentinel") {
					t.Fatalf("observation=%s want %s", encoded, tc.reason)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.reason == "SourceReady" && len(paths) != 4 {
				t.Fatalf("reads are not fresh: %v", paths)
			}
		})
	}
}

// TestProviderRejectsUnsupportedDispatchBeforeReading protects direct callers as well as admission.
func TestProviderRejectsUnsupportedDispatchBeforeReading(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*datav1alpha1.ProvisionedSource)
	}{
		{"graph", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Type = "graph" }},
		{"hybrid", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Provider = "cnpg-hybrid" }},
		{"unknown version", func(s *datav1alpha1.ProvisionedSource) { s.Engine.APIVersion = "engine-provider/v2" }},
		{"missing engine", func(s *datav1alpha1.ProvisionedSource) { s.Engine = nil }},
		{"wrong API", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.APIVersion = "other.example.org/v1" }},
		{"wrong Secret", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "warehouse-superuser" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := cnpgSource()
			tc.change(&source)
			got := (&Registry{}).Observe(t.Context(), "products", source)
			if got.Ready ||
				(got.Reason != "EngineProviderUnsupported" && got.Reason != "SourceInvalid") {
				t.Fatalf("unsupported dispatch=%+v", got)
			}
		})
	}
}

// TestProviderHonorsCancellation prevents an unavailable API from occupying reconciliation indefinitely.
func TestProviderHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	server := httptest.NewServer(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }),
	)
	t.Cleanup(server.Close)
	started := time.Now()
	got := (&Registry{Reader: cnpgReader(t, server.URL)}).Observe(ctx, "products", cnpgSource())
	if got.Ready || got.Reason != "SourceUnavailable" || time.Since(started) > time.Second {
		t.Fatalf("cancellation=%+v elapsed=%s", got, time.Since(started))
	}
}

// TestColdProviderReaderCancellation models the manager's cold uncached reader with unavailable discovery.
func TestColdProviderReaderCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || r.URL.Path == "/apis" ||
			r.URL.Path == "/apis/postgresql.cnpg.io/v1" {
			t.Error("typed observation performed discovery outside its deadline")
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	reader, err := NewEngineReader(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := (&Registry{Reader: reader}).Observe(ctx, "products", cnpgSource())
	if got.Ready || got.Reason != "SourceUnavailable" || time.Since(started) > time.Second {
		t.Fatalf("cold API observation=%+v elapsed=%s", got, time.Since(started))
	}
}

// cnpgSource selects the supported native SQL adapter and its generated application publication.
func cnpgSource() datav1alpha1.ProvisionedSource {
	return datav1alpha1.ProvisionedSource{
		Adapter: "cnpg/v1",
		Engine: &datav1alpha1.EngineSelection{
			APIVersion: "engine-provider/v1",
			Type:       "sql",
			Provider:   "native",
		},
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "postgresql.cnpg.io/v1",
			Kind:       "Cluster",
			Name:       "warehouse",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "warehouse-app"},
	}
}

// cnpgFixture models healthy operator status without inventing mandatory observed generations.
func cnpgFixture(t *testing.T) (*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	t.Helper()
	cluster := &unstructured.Unstructured{}
	if err := cluster.UnmarshalJSON(
		[]byte(
			`{"apiVersion":"postgresql.cnpg.io/v1","kind":"Cluster","metadata":{"name":"warehouse","namespace":"products","uid":"cluster-uid","generation":3},"spec":{"instances":1},"status":{"instances":1,"readyInstances":1,"currentPrimary":"warehouse-1","targetPrimary":"warehouse-1","conditions":[{"type":"Ready","status":"True"}]}}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	secret := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "warehouse-app",
			Namespace: "products",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "postgresql.cnpg.io/v1",
					Kind:       "Cluster",
					Name:       "warehouse",
					UID:        "cluster-uid",
				},
			},
		},
	}
	return cluster, secret
}

// cnpgReader exercises real HTTP encoding and metadata negotiation with fixed fixture API mappings.
func cnpgReader(t *testing.T, host string) client.Reader {
	t.Helper()
	reader, err := NewEngineReader(&rest.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
