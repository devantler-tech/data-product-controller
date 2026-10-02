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
)

// TestPerconaObservation exercises the production HTTP reader, dispatch and publication boundary.
func TestPerconaObservation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason           string
		mutate                 func(*unstructured.Unstructured, *metav1.PartialObjectMetadata)
		sourceCode, secretCode int
	}{
		{name: "ready", reason: "SourceReady"},
		{name: "operator API absent", sourceCode: 404, reason: "SourceNotFound"},
		{name: "source denied", sourceCode: 403, reason: "SourceAccessDenied"},
		{name: "API unavailable", sourceCode: 503, reason: "SourceUnavailable"},
		{name: "publication absent", secretCode: 404, reason: "ConnectionNotPublished"},
		{name: "publication denied", secretCode: 403, reason: "SourceAccessDenied"},
		{name: "publication failed", secretCode: 500, reason: "SourceUnavailable"},
		{name: "missing status", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { delete(c.Object, "status") }},
		{name: "initializing", reason: "SourceNotReady", mutate: perconaField("initializing", "status", "state")},
		{name: "operator error", reason: "SourceFailed", mutate: perconaField("error", "status", "state")},
		{name: "paused spec", reason: "SourcePaused", mutate: perconaField(true, "spec", "pause")},
		{name: "paused status", reason: "SourceNotReady", mutate: perconaField("paused", "status", "state")},
		{name: "unmanaged", reason: "SourceInvalid", mutate: perconaField(true, "spec", "unmanaged")},
		{name: "sharded", reason: "SourceInvalid", mutate: perconaField(true, "spec", "sharding", "enabled")},
		{name: "arbiter", reason: "SourceInvalid", mutate: perconaReplicaField(true, "arbiter", "enabled")},
		{name: "non-voting member", reason: "SourceInvalid", mutate: perconaReplicaField(true, "nonvoting", "enabled")},
		{name: "malformed non-voting member", reason: "SourceInvalid", mutate: perconaReplicaField("sensitive-sentinel", "nonvoting", "enabled")},
		{name: "disabled non-voting members", reason: "SourceReady", mutate: perconaReplicaField(false, "nonvoting", "enabled")},
		{name: "hidden member", reason: "SourceInvalid", mutate: perconaReplicaField(true, "hidden", "enabled")},
		{name: "malformed hidden member", reason: "SourceInvalid", mutate: perconaReplicaField("sensitive-sentinel", "hidden", "enabled")},
		{name: "disabled hidden members", reason: "SourceReady", mutate: perconaReplicaField(false, "hidden", "enabled")},
		{name: "external arbiter", reason: "SourceInvalid", mutate: perconaReplicaField([]any{map[string]any{"host": "external.example.com", "arbiterOnly": true}}, "externalNodes")},
		{name: "malformed external members", reason: "SourceInvalid", mutate: perconaReplicaField("sensitive-sentinel", "externalNodes")},
		{name: "zero requested", reason: "SourceInvalid", mutate: perconaReplicaField(int64(0), "size")},
		{name: "multiple replica sets", reason: "SourceInvalid", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			_ = unstructured.SetNestedSlice(c.Object, []any{map[string]any{"name": "rs0", "size": int64(3)}, map[string]any{"name": "rs1", "size": int64(3)}}, "spec", "replsets")
		}},
		{name: "unsupported operator profile", reason: "SourceVersionUnsupported", mutate: perconaField("1.22.0", "spec", "crVersion")},
		{name: "missing profile", reason: "SourceVersionUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			unstructured.RemoveNestedField(c.Object, "spec", "crVersion")
		}},
		{name: "missing ready replica", reason: "SourceNotReady", mutate: perconaField(int64(2), "status", "ready")},
		{name: "stale replica size", reason: "SourceNotReady", mutate: perconaField(int64(2), "status", "size")},
		{name: "wrong state case", reason: "SourceNotReady", mutate: perconaField("Ready", "status", "state")},
		{name: "malformed counts", reason: "SourceNotReady", mutate: perconaField("3", "status", "ready")},
		{name: "stale generation", reason: "SourceNotReady", mutate: perconaField(int64(2), "status", "observedGeneration")},
		{name: "current explicit generation", reason: "SourceReady", mutate: perconaField(int64(3), "status", "observedGeneration")},
		{name: "malformed generation", reason: "SourceNotReady", mutate: perconaField("3", "status", "observedGeneration")},
		{name: "missing app users", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			unstructured.RemoveNestedField(c.Object, "spec", "users")
		}},
		{name: "generated password", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField(map[string]any{}, "passwordSecretRef")},
		{name: "another publication", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField("other-password", "passwordSecretRef", "name")},
		{name: "privileged additional role", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField([]any{map[string]any{"name": "read", "db": "catalog"}, map[string]any{"name": "root", "db": "admin"}}, "roles")},
		{name: "system role database", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField([]any{map[string]any{"name": "read", "db": "admin"}}, "roles")},
		{name: "missing roles", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField([]any{}, "roles")},
		{name: "external authentication", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField("$external", "db")},
		{name: "system account", reason: "ConnectionPublicationUnsupported", mutate: perconaUserField("databaseAdmin", "name")},
		{name: "configured system password", reason: "ConnectionPublicationUnsupported", mutate: perconaField("documents-reader", "spec", "secrets", "users")},
		{name: "ambiguous user binding", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			users, _, _ := unstructured.NestedSlice(c.Object, "spec", "users")
			_ = unstructured.SetNestedSlice(c.Object, append(users, users[0]), "spec", "users")
		}},
		{name: "duplicate account with another password", reason: "ConnectionPublicationUnsupported", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			users, _, _ := unstructured.NestedSlice(c.Object, "spec", "users")
			other := map[string]any{"name": "catalog-reader", "db": "admin", "passwordSecretRef": map[string]any{"name": "other-password"}, "roles": []any{map[string]any{"name": "root", "db": "admin"}}}
			_ = unstructured.SetNestedSlice(c.Object, append(users, other), "spec", "users")
		}},
		{name: "malformed user binding", reason: "ConnectionPublicationUnsupported", mutate: perconaField("sensitive-sentinel", "spec", "users")},
		{name: "deleted source", reason: "SourceDeleting", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			c.SetDeletionTimestamp(&now)
		}},
		{name: "recreated source", reason: "ConnectionOwnerMismatch", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("new-source-uid") }},
		{name: "empty source identity", reason: "SourceInvalid", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("") }},
		{name: "wrong owner group", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].APIVersion = "other.example.org/v1"
		}},
		{name: "wrong owner version", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].APIVersion = "psmdb.percona.com/v2"
		}},
		{name: "wrong owner kind", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].Kind = "Cluster"
		}},
		{name: "wrong owner name", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			s.OwnerReferences[0].Name = "other"
		}},
		{name: "missing publication owner", reason: "ConnectionOwnerMismatch", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) { s.OwnerReferences = nil }},
		{name: "deleting publication", reason: "ConnectionNotPublished", mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			s.DeletionTimestamp = &now
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster, secret := perconaFixture(t)
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
						t.Errorf("provider mutation: %s", r.Method)
						w.WriteHeader(405)
						return
					}
					var value any
					var code int
					switch r.URL.Path {
					case "/apis/psmdb.percona.com/v1/namespaces/products/perconaservermongodbs/documents":
						value = cluster
						code = tc.sourceCode
					case "/api/v1/namespaces/products/secrets/documents-reader":
						if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
							t.Error("Secret values requested")
							w.WriteHeader(406)
							return
						}
						value = secret
						code = tc.secretCode
					default:
						t.Errorf("unexpected discovery or resource read: %s", r.URL.Path)
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
							"message":    "sensitive-sentinel",
						}
					}
					if err := json.NewEncoder(w).Encode(value); err != nil {
						t.Error(err)
					}
				}),
			)
			t.Cleanup(server.Close)
			reader, err := NewEngineReader(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				got := (&Registry{Reader: reader}).Observe(t.Context(), "products", perconaSource())
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if got.Reason != tc.reason || got.Ready != (tc.reason == "SourceReady") ||
					strings.Contains(string(encoded), "sensitive-sentinel") {
					t.Fatalf("observation=%s want %s", encoded, tc.reason)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.reason == "SourceReady" && len(paths) != 4 {
				t.Fatalf("fresh source/publication reads=%v", paths)
			}
			if tc.reason == "SourceNotReady" || tc.reason == "SourceFailed" ||
				tc.reason == "SourceInvalid" ||
				tc.reason == "SourcePaused" ||
				tc.reason == "SourceVersionUnsupported" ||
				tc.reason == "ConnectionPublicationUnsupported" {
				if len(paths) != 2 {
					t.Fatalf("rejected source read publication: %v", paths)
				}
			}
		})
	}
}

// TestPerconaRejectsSelectionBeforeReads catches bypasses of admission by direct callers.
func TestPerconaRejectsSelectionBeforeReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*datav1alpha1.ProvisionedSource)
	}{
		{"version", func(s *datav1alpha1.ProvisionedSource) { s.Engine.APIVersion = "engine-provider/v2" }},
		{"provider", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Provider = "cnpg-hybrid" }},
		{"type", func(s *datav1alpha1.ProvisionedSource) { s.Engine.Type = "sql" }},
		{"missing selection", func(s *datav1alpha1.ProvisionedSource) { s.Engine = nil }},
		{"API", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.APIVersion = "psmdb.percona.com/v2" }},
		{"kind", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.Kind = "Cluster" }},
		{"empty source", func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.Name = "" }},
		{"empty publication", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "" }},
		{"default system password", func(s *datav1alpha1.ProvisionedSource) { s.ConnectionSecretRef.Name = "percona-server-mongodb-users" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := perconaSource()
			tc.change(&source)
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					t.Error("invalid selection performed an external read")
					w.WriteHeader(http.StatusInternalServerError)
				}),
			)
			t.Cleanup(server.Close)
			reader, err := NewEngineReader(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			got := (&Registry{Reader: reader}).Observe(t.Context(), "products", source)
			if got.Ready ||
				(got.Reason != "EngineProviderUnsupported" && got.Reason != "SourceInvalid") {
				t.Fatalf("unsupported selection=%+v", got)
			}
		})
	}
}

// TestPerconaDeadlineSharesBothReads protects the whole observation, including a blocked Secret request.
func TestPerconaDeadlineSharesBothReads(t *testing.T) {
	t.Parallel()
	for _, blockSecret := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "source", true: "publication"}[blockSecret],
			func(t *testing.T) {
				t.Parallel()
				cluster, _ := perconaFixture(t)
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if blockSecret &&
							r.URL.Path == "/apis/psmdb.percona.com/v1/namespaces/products/perconaservermongodbs/documents" {
							w.Header().Set("Content-Type", "application/json")
							_ = json.NewEncoder(w).Encode(cluster)
							return
						}
						<-r.Context().Done()
					}),
				)
				t.Cleanup(server.Close)
				reader, err := NewEngineReader(&rest.Config{Host: server.URL})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
				defer cancel()
				started := time.Now()
				got := (&Registry{Reader: reader}).Observe(ctx, "products", perconaSource())
				if got.Ready || got.Reason != "SourceUnavailable" ||
					time.Since(started) > time.Second {
					t.Fatalf("deadline=%+v elapsed=%s", got, time.Since(started))
				}
			},
		)
	}
}

func perconaSource() datav1alpha1.ProvisionedSource {
	return datav1alpha1.ProvisionedSource{
		Adapter: "percona-mongodb/v1",
		Engine: &datav1alpha1.EngineSelection{
			APIVersion: "engine-provider/v1",
			Type:       "document",
			Provider:   "native",
		},
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "psmdb.percona.com/v1",
			Kind:       "PerconaServerMongoDB",
			Name:       "documents",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "documents-reader"},
	}
}

// perconaFixture is a hand-authored pinned API profile, not proof that MongoDB exists.
func perconaFixture(t *testing.T) (*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	t.Helper()
	cluster := &unstructured.Unstructured{}
	if err := cluster.UnmarshalJSON(
		[]byte(
			`{"apiVersion":"psmdb.percona.com/v1","kind":"PerconaServerMongoDB","metadata":{"name":"documents","namespace":"products","uid":"document-uid","generation":3},"spec":{"crVersion":"1.23.0","pause":false,"unmanaged":false,"sharding":{"enabled":false},"replsets":[{"name":"rs0","size":3}],"users":[{"name":"catalog-reader","db":"admin","passwordSecretRef":{"name":"documents-reader","key":"password"},"roles":[{"name":"read","db":"catalog"}]}]},"status":{"state":"ready","size":3,"ready":3}}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	secret := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "documents-reader",
			Namespace: "products",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "psmdb.percona.com/v1",
					Kind:       "PerconaServerMongoDB",
					Name:       "documents",
					UID:        "document-uid",
				},
			},
		},
	}
	return cluster, secret
}

func perconaField(
	value any,
	fields ...string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		_ = unstructured.SetNestedField(c.Object, value, fields...)
	}
}

func perconaReplicaField(
	value any,
	fields ...string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		sets, _, _ := unstructured.NestedSlice(c.Object, "spec", "replsets")
		replica, ok := sets[0].(map[string]any)
		if !ok {
			panic("invalid replica fixture")
		}
		_ = unstructured.SetNestedField(replica, value, fields...)
		_ = unstructured.SetNestedSlice(c.Object, sets, "spec", "replsets")
	}
}

func perconaUserField(
	value any,
	fields ...string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		users, _, _ := unstructured.NestedSlice(c.Object, "spec", "users")
		user, ok := users[0].(map[string]any)
		if !ok {
			panic("invalid application user fixture")
		}
		_ = unstructured.SetNestedField(user, value, fields...)
		_ = unstructured.SetNestedSlice(c.Object, users, "spec", "users")
	}
}
