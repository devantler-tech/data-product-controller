package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestObserverUsesUncachedMetadataOnlyConnectionRequests verifies each observation makes fresh GETs
// and negotiates Secret metadata without requesting credential values or mutating resources.
func TestObserverUsesUncachedMetadataOnlyConnectionRequests(t *testing.T) {
	t.Parallel()
	var paths []string
	var pathsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		pathsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/apis/database.example.org/v1alpha1/namespaces/products/databases/warehouse":
			_, _ = w.Write(
				[]byte(
					`{"apiVersion":"database.example.org/v1alpha1","kind":"Database","metadata":{"name":"warehouse","namespace":"products","uid":"source-uid","generation":3},"spec":{"writeConnectionSecretToRef":{"name":"warehouse-connection"}},"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}}`,
				),
			)
		case "/api/v1/namespaces/products/secrets/warehouse-connection":
			if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
				t.Error("connection request did not negotiate metadata-only response")
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			_, _ = w.Write(
				[]byte(
					`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"warehouse-connection","namespace":"products","ownerReferences":[{"apiVersion":"database.example.org/v1alpha1","kind":"Database","name":"warehouse","uid":"source-uid"}]}}`,
				),
			)
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	mapper := sourceMapper(meta.RESTScopeNamespace)
	reader, err := client.New(
		&rest.Config{Host: server.URL},
		client.Options{Scheme: runtime.NewScheme(), Mapper: mapper},
	)
	if err != nil {
		t.Fatal(err)
	}
	observer := &Crossplane{Reader: reader, Mapper: mapper}
	for range 2 {
		got := observer.Observe(t.Context(), "products", sourceReference())
		if !got.Ready {
			t.Fatalf("observation = %+v", got)
		}
	}
	pathsMu.Lock()
	defer pathsMu.Unlock()
	if len(paths) != 4 {
		t.Fatalf("each observation must freshly read source and connection metadata: %v", paths)
	}
}

// TestObserverRejectsInvalidOrClusterScopedReferencesBeforeReading ensures unsupported references
// fail before any resource request, keeping discovery failures and namespace boundaries explicit.
func TestObserverRejectsInvalidOrClusterScopedReferencesBeforeReading(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*datav1alpha1.ProvisionedSource)
		scope  meta.RESTScope
		want   string
	}{
		{name: "core resource", change: func(s *datav1alpha1.ProvisionedSource) {
			s.ResourceRef.APIVersion = "v1"
			s.ResourceRef.Kind = "Secret"
		}, want: "SourceInvalid"},
		{name: "unknown adapter", change: func(s *datav1alpha1.ProvisionedSource) { s.Adapter = "unknown/v1" }, want: "SourceInvalid"},
		{name: "unknown API", change: func(s *datav1alpha1.ProvisionedSource) { s.ResourceRef.APIVersion = "unknown.example.org/v1" }, want: "SourceAPIUnavailable"},
		{name: "cluster resource", scope: meta.RESTScopeRoot, want: "SourceScopeUnsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("invalid reference caused API read: %s", r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
				}),
			)
			t.Cleanup(server.Close)
			scope := test.scope
			if scope == nil {
				scope = meta.RESTScopeNamespace
			}
			mapper := sourceMapper(scope)
			reader, err := client.New(
				&rest.Config{Host: server.URL},
				client.Options{Scheme: runtime.NewScheme(), Mapper: mapper},
			)
			if err != nil {
				t.Fatal(err)
			}
			source := sourceReference()
			if test.change != nil {
				test.change(&source)
			}
			got := (&Crossplane{Reader: reader, Mapper: mapper}).Observe(
				t.Context(),
				"products",
				source,
			)
			if got.Ready || got.Reason != test.want {
				t.Fatalf("observation = %+v, want %s", got, test.want)
			}
		})
	}
}

// TestCrossplaneConditionContract checks optional generations and rejects failed, missing,
// duplicate, malformed, or explicitly stale readiness conditions.
func TestCrossplaneConditionContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, status string
		want         bool
	}{
		{name: "optional generations", status: `{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}`, want: true},
		{name: "current generations", status: `{"observedGeneration":3,"conditions":[{"type":"Ready","status":"True","observedGeneration":3},{"type":"Synced","status":"True","observedGeneration":3}]}`, want: true},
		{name: "failed sync", status: `{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"False"}]}`},
		{name: "missing sync", status: `{"conditions":[{"type":"Ready","status":"True"}]}`},
		{name: "stale Ready", status: `{"conditions":[{"type":"Ready","status":"True","observedGeneration":2},{"type":"Synced","status":"True"}]}`},
		{name: "stale Synced", status: `{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True","observedGeneration":2}]}`},
		{name: "duplicate Ready", status: `{"conditions":[{"type":"Ready","status":"True"},{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}`},
		{name: "malformed condition", status: `{"conditions":["Ready"]}`},
		{name: "malformed generation", status: `{"observedGeneration":"3","conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resource := &unstructured.Unstructured{}
			if err := resource.UnmarshalJSON(
				[]byte(
					`{"apiVersion":"database.example.org/v1alpha1","kind":"Database","metadata":{"generation":3},"status":` + test.status + `}`,
				),
			); err != nil {
				t.Fatal(err)
			}
			if got := crossplaneReady(resource); got != test.want {
				t.Fatalf("ready = %t, want %t", got, test.want)
			}
		})
	}
}

// TestObserverRejectsAccessAndMalformedReferencesWithoutLeakingErrors verifies API failure details
// stay out of serialized observations while access denials retain an actionable reason.
func TestObserverRejectsAccessAndMalformedReferencesWithoutLeakingErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "forbidden", status: http.StatusForbidden, want: "SourceAccessDenied"},
		{name: "unauthorized", status: http.StatusUnauthorized, want: "SourceAccessDenied"},
		{name: "unavailable", status: http.StatusBadRequest, want: "SourceUnavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(test.status)
					_ = json.NewEncoder(w).
						Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "code": test.status, "message": "credential-sentinel"})
				}),
			)
			t.Cleanup(server.Close)
			mapper := sourceMapper(meta.RESTScopeNamespace)
			reader, err := client.New(
				&rest.Config{Host: server.URL},
				client.Options{Scheme: runtime.NewScheme(), Mapper: mapper},
			)
			if err != nil {
				t.Fatal(err)
			}
			observer := &Crossplane{Reader: reader, Mapper: mapper}
			got := observer.Observe(t.Context(), "products", sourceReference())
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if got.Ready || got.Reason != test.want ||
				strings.Contains(string(encoded), "credential-sentinel") {
				t.Fatalf("unsafe observation: %s", encoded)
			}
		})
	}
}

// sourceMapper supplies deterministic discovery for the test Database API and core Secrets,
// with a selectable Database scope to exercise rejection of cluster-scoped sources.
func sourceMapper(scope meta.RESTScope) meta.RESTMapper {
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
		scope,
	)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Secret"}, meta.RESTScopeNamespace)
	return mapper
}

// sourceReference binds the test Database and its connection Secret through the crossplane/v1 adapter.
func sourceReference() datav1alpha1.ProvisionedSource {
	return datav1alpha1.ProvisionedSource{
		Adapter: "crossplane/v1",
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "database.example.org/v1alpha1",
			Kind:       "Database",
			Name:       "warehouse",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "warehouse-connection"},
	}
}
