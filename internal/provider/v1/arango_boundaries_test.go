package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// TestArangoBoundaries prevents stale operator status and privileged publications from becoming ready.
func TestArangoBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason           string
		mutate                 func(*unstructured.Unstructured, *metav1.PartialObjectMetadata)
		sourceCode, secretCode int
		secretRead             bool
		publicationName        string
	}{
		{name: "absent API", sourceCode: 404, reason: "SourceNotFound"},
		{name: "denied source", sourceCode: 403, reason: "SourceAccessDenied"},
		{name: "unavailable source", sourceCode: 503, reason: "SourceUnavailable"},
		{name: "absent publication", secretCode: 404, reason: "ConnectionNotPublished", secretRead: true},
		{name: "denied publication", secretCode: 403, reason: "SourceAccessDenied", secretRead: true},
		{name: "deleted source", reason: "SourceDeleting", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			c.SetDeletionTimestamp(&now)
		}},
		{name: "unsupported image", reason: "SourceVersionUnsupported", mutate: arangoField("arangodb:latest", "spec", "image")},
		{name: "unsupported topology", reason: "SourceInvalid", mutate: arangoField("Cluster", "spec", "mode")},
		{name: "unsupported count", reason: "SourceInvalid", mutate: arangoField(int64(2), "spec", "single", "count")},
		{name: "authentication disabled", reason: "SourceInvalid", mutate: arangoField("None", "spec", "auth", "jwtSecretName")},
		{name: "empty source UID", reason: "SourceInvalid", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("") }},
		{name: "missing status", reason: "SourceNotReady", mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { delete(c.Object, "status") }},
		{name: "failed deployment", reason: "SourceFailed", mutate: arangoField("Failed", "status", "phase")},
		{name: "stale accepted checksum", reason: "SourceNotReady", mutate: arangoField("old", "status", "acceptedSpecVersion")},
		{name: "accepted authentication disabled", reason: "SourceNotReady", mutate: arangoField("None", "status", "accepted-spec", "auth", "jwtSecretName")},
		{name: "accepted authentication unresolved", reason: "SourceNotReady", mutate: arangoField("", "status", "accepted-spec", "auth", "jwtSecretName")},
		{name: "accepted topology unsupported", reason: "SourceNotReady", mutate: arangoField("Cluster", "status", "accepted-spec", "mode")},
		{name: "accepted count unsupported", reason: "SourceNotReady", mutate: arangoField(int64(2), "status", "accepted-spec", "single", "count")},
		{name: "accepted image unsupported", reason: "SourceNotReady", mutate: arangoField("arangodb:latest", "status", "accepted-spec", "image")},
		{name: "stale applied checksum", reason: "SourceNotReady", mutate: arangoField("old", "status", "appliedVersion")},
		{name: "new spec with old ready status", reason: "SourceNotReady", mutate: arangoField(true, "spec", "downtimeAllowed")},
		{name: "missing conditions", reason: "SourceNotReady", mutate: arangoField([]any{}, "status", "conditions")},
		{name: "historical propagation", reason: "SourceNotReady", mutate: arangoCondition("UpToDate", "False")},
		{name: "unsuccessful bootstrap", reason: "SourceNotReady", mutate: arangoCondition("BootstrapSucceded", "False")},
		{name: "failed update", reason: "SourceFailed", mutate: arangoCondition("UpdateFailed", "True")},
		{name: "secret changes", reason: "SourceFailed", mutate: arangoCondition("SecretsChanged", "True")},
		{name: "update in progress", reason: "SourceNotReady", mutate: arangoCondition("UpdateInProgress", "True")},
		{name: "upgrade in progress", reason: "SourceNotReady", mutate: arangoCondition("UpgradeInProgress", "True")},
		{name: "pending member update", reason: "SourceNotReady", mutate: arangoMemberCondition("PendingUpdate", "True")},
		{name: "member updating", reason: "SourceNotReady", mutate: arangoMemberCondition("Updating", "True")},
		{name: "stale pod image", reason: "SourceNotReady", mutate: arangoMemberField("sha256:old-image", "image-id")},
		{name: "stale pod version", reason: "SourceNotReady", mutate: arangoMemberField("3.11.0", "arango-version")},
		{name: "nonroot bootstrap mapping", reason: "SourceInvalid", mutate: arangoField("other-secret", "status", "accepted-spec", "bootstrap", "passwordSecretNames", "catalog-reader")},
		{name: "no ready member", reason: "SourceNotReady", mutate: arangoField([]any{}, "status", "members", "single")},
		{name: "configured exporter JWT alias", reason: "ConnectionPublicationUnsupported", mutate: arangoField("lineage-reader", "status", "accepted-spec", "metrics", "authentication", "jwtTokenSecretName")},
		{name: "configured license alias", reason: "ConnectionPublicationUnsupported", mutate: arangoField("lineage-reader", "status", "accepted-spec", "license", "secretName")},
		{name: "configured encryption alias", reason: "ConnectionPublicationUnsupported", mutate: arangoField("lineage-reader", "status", "accepted-spec", "rocksdb", "encryption", "keySecretName")},
		{name: "configured restore alias", reason: "ConnectionPublicationUnsupported", mutate: arangoField("lineage-reader", "status", "accepted-spec", "restoreEncryptionSecret")},
		{name: "generated JWT folder", reason: "ConnectionPublicationUnsupported", publicationName: "lineage-jwt-folder"},
		{name: "generated registry credentials", reason: "ConnectionPublicationUnsupported", publicationName: "lineage-rlm"},
		{name: "generated trust store", reason: "ConnectionPublicationUnsupported", publicationName: "lineage-truststore"},
		{name: "generated encryption folder", reason: "ConnectionPublicationUnsupported", publicationName: "lineage-encryption-folder"},
		{name: "generated member keyfile", reason: "ConnectionPublicationUnsupported", publicationName: "lineage-single-single1-tls-keyfile"},
		{name: "configured registry credentials", reason: "ConnectionPublicationUnsupported", mutate: arangoField([]any{"lineage-reader"}, "status", "accepted-spec", "imagePullSecrets")},
		{name: "configured SNI alias", reason: "ConnectionPublicationUnsupported", mutate: arangoField([]any{"lineage.example.com"}, "status", "accepted-spec", "tls", "sni", "mapping", "lineage-reader")},
		{name: "malformed extra condition", reason: "SourceNotReady", mutate: arangoCondition("UpdateFailed", "sensitive-sentinel")},
		{name: "stale runtime version", reason: "SourceNotReady", mutate: arangoField("3.11.0", "status", "current-image", "arangodb-version")},
		{name: "enterprise outside profile", reason: "SourceNotReady", mutate: arangoField(true, "status", "current-image", "enterprise")},
		{name: "missing publication contract", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) { s.Annotations = nil }},
		{name: "root publication", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-user", "root")},
		{name: "mixed-case root publication", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-user", "Root")},
		{name: "upper-case operator publication", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-user", "OPERATOR")},
		{name: "mixed-case internal publication", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-user", "Internal")},
		{name: "mixed-case backup publication", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-user", "Backup")},
		{name: "system database", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-database", "_system")},
		{name: "wildcard collection", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-collections", "products,*")},
		{name: "ambiguous collections", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-collections", "products,products")},
		{name: "write access", reason: "ConnectionPublicationUnsupported", secretRead: true, mutate: arangoAnnotation("arango-access", "read-write")},
		{name: "recreated source", reason: "ConnectionOwnerMismatch", secretRead: true, mutate: func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) { c.SetUID("replacement-uid") }},
		{name: "deleted publication", reason: "ConnectionNotPublished", secretRead: true, mutate: func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
			now := metav1.Now()
			s.SetDeletionTimestamp(&now)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster, secret := arangoFixture(t)
			source := arangoSource()
			if tc.publicationName != "" {
				source.ConnectionSecretRef.Name = tc.publicationName
				secret.Name = tc.publicationName
			}
			if tc.mutate != nil {
				tc.mutate(cluster, secret)
			}
			var secretReads atomic.Int32
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					var value any = cluster
					code := tc.sourceCode
					if r.URL.Path == "/api/v1/namespaces/products/secrets/"+source.ConnectionSecretRef.Name {
						secretReads.Add(1)
						if !strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
							t.Error("Secret values requested")
						}
						value, code = secret, tc.secretCode
					} else if r.URL.Path != "/apis/database.arangodb.com/v1/namespaces/products/arangodeployments/lineage" {
						t.Errorf("unexpected read: %s", r.URL.Path)
						code = 404
					}
					if r.Method != http.MethodGet {
						t.Errorf("unexpected mutation: %s", r.Method)
					}
					if code != 0 {
						w.WriteHeader(code)
						value = map[string]any{
							"apiVersion": "v1",
							"kind":       "Status",
							"status":     "Failure",
							"code":       code,
							"message":    "sensitive-sentinel",
						}
					}
					_ = json.NewEncoder(w).Encode(value)
				}),
			)
			t.Cleanup(server.Close)
			reader, err := NewEngineReader(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			got := (&Registry{Reader: reader}).Observe(t.Context(), "products", source)
			if got.Ready || got.Reason != tc.reason ||
				strings.Contains(got.Message, "sensitive-sentinel") {
				t.Fatalf("observation=%+v want %s", got, tc.reason)
			}
			if (secretReads.Load() != 0) != tc.secretRead {
				t.Fatalf("publication reads=%d want read=%t", secretReads.Load(), tc.secretRead)
			}
		})
	}
}

// TestArangoCancellation bounds both reads with the caller's shorter deadline.
func TestArangoCancellation(t *testing.T) {
	t.Parallel()
	for _, blockSecret := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "source", true: "publication"}[blockSecret],
			func(t *testing.T) {
				t.Parallel()
				cluster, _ := arangoFixture(t)
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if blockSecret && strings.HasPrefix(r.URL.Path, "/apis/") {
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
				start := time.Now()
				got := (&Registry{Reader: reader}).Observe(ctx, "products", arangoSource())
				if got.Ready || got.Reason != "SourceUnavailable" ||
					time.Since(start) > time.Second {
					t.Fatalf("deadline=%+v", got)
				}
			},
		)
	}
}

// arangoField changes one fixture field without refreshing the reported specification checksums.
func arangoField(
	value any,
	fields ...string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		_ = unstructured.SetNestedField(c.Object, value, fields...)
	}
}

// arangoCondition replaces or appends an operator condition while retaining the other fixture conditions.
func arangoCondition(
	kind, status string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		conditions, _, _ := unstructured.NestedSlice(c.Object, "status", "conditions")
		for _, value := range conditions {
			condition, ok := value.(map[string]any)
			if !ok {
				panic("invalid condition fixture")
			}
			if condition["type"] == kind {
				condition["status"] = status
				_ = unstructured.SetNestedSlice(c.Object, conditions, "status", "conditions")
				return
			}
		}
		_ = unstructured.SetNestedSlice(
			c.Object,
			append(conditions, map[string]any{"type": kind, "status": status}),
			"status",
			"conditions",
		)
	}
}

// arangoAnnotation changes public application metadata without changing source ownership.
func arangoAnnotation(
	key, value string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(_ *unstructured.Unstructured, s *metav1.PartialObjectMetadata) {
		s.Annotations["data.devantler.tech/"+key] = value
	}
}

// arangoMemberField changes one running member while retaining deployment-level readiness.
func arangoMemberField(
	value any,
	field string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return func(c *unstructured.Unstructured, _ *metav1.PartialObjectMetadata) {
		members, _, _ := unstructured.NestedSlice(c.Object, "status", "members", "single")
		member, ok := members[0].(map[string]any)
		if !ok {
			panic("invalid member fixture")
		}
		member[field] = value
		_ = unstructured.SetNestedSlice(c.Object, members, "status", "members", "single")
	}
}

// arangoMemberCondition combines Ready with a competing member state to test readiness withdrawal.
func arangoMemberCondition(
	kind, status string,
) func(*unstructured.Unstructured, *metav1.PartialObjectMetadata) {
	return arangoMemberField(
		[]any{
			map[string]any{"type": "Ready", "status": "True"},
			map[string]any{"type": kind, "status": status},
		},
		"conditions",
	)
}
