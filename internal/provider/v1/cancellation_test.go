package v1

import (
	"context"
	"testing"
	"time"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type completionReader struct {
	client.Reader
	get func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
}

func (r completionReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	return r.get(ctx, key, obj, opts...)
}

// TestProviderCompletionHonorsContext covers every registered source path and recovery.
func TestProviderCompletionHonorsContext(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"sql", "document", "graph", "hybrid-document", "hybrid-graph", "crossplane"} {
		for _, boundary := range []string{"healthy", "cancelled", "deadline"} {
			t.Run(profile+"/"+boundary, func(t *testing.T) {
				t.Parallel()
				cluster, secret, source := completionFixture(t, profile)
				ctx, cancel := context.WithCancel(t.Context())
				if boundary == "deadline" {
					ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
				}
				defer cancel()
				finish := func(readContext context.Context) {
					if boundary == "cancelled" {
						cancel()
					}
					if boundary == "deadline" {
						<-readContext.Done()
					}
				}
				calls := 0
				reader := completionReader{
					get: func(readContext context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
						calls++
						if workload, ok := obj.(*unstructured.Unstructured); ok {
							if key != (client.ObjectKey{Namespace: "products", Name: cluster.GetName()}) {
								t.Fatal("source read escaped declared identity")
							}
							cluster.DeepCopyInto(workload)
						} else {
							metadata, ok := obj.(*metav1.PartialObjectMetadata)
							if !ok ||
								key != (client.ObjectKey{Namespace: "products", Name: secret.Name}) {
								t.Fatal("publication read was not exact-name metadata")
							}
							secret.DeepCopyInto(metadata)
							finish(readContext)
						}
						return nil
					},
				}
				version := schema.GroupVersion{Group: "database.example.org", Version: "v1alpha1"}
				mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{version})
				mapper.Add(version.WithKind("Database"), meta.RESTScopeNamespace)
				registry := Registry{Reader: reader, LegacyReader: reader, Mapper: mapper}
				got := registry.Observe(ctx, "products", source)
				want := "SourceUnavailable"
				if boundary == "healthy" {
					want = "SourceReady"
				}
				if got.Ready != (boundary == "healthy") || got.Reason != want || calls != 2 {
					t.Fatalf("completion result=%+v reads=%d want %s", got, calls, want)
				}
				finish = func(context.Context) {}
				got = registry.Observe(t.Context(), "products", source)
				if !got.Ready || calls != 4 {
					t.Fatalf("fresh recovery failed: %+v reads=%d", got, calls)
				}
			})
		}
	}
}

func completionFixture(
	t *testing.T,
	profile string,
) (*unstructured.Unstructured, *metav1.PartialObjectMetadata, data.ProvisionedSource) {
	t.Helper()
	switch profile {
	case "sql":
		c, s := cnpgFixture(t)
		return c, s, cnpgSource()
	case "document":
		c, s := perconaFixture(t)
		return c, s, perconaSource()
	case "graph":
		c, s := arangoFixture(t)
		return c, s, arangoSource()
	case "hybrid-document":
		c, s := hybridDocumentFixture(t)
		return c, s, hybridDocumentSource()
	case "hybrid-graph":
		c, s := hybridDocumentFixture(t)
		setHybridAGEProfile(t, c, s, "graph")
		source := hybridDocumentSource()
		source.Engine.Type = "graph"
		return c, s, source
	default:
		c := &unstructured.Unstructured{}
		if err := c.UnmarshalJSON(
			[]byte(
				`{"apiVersion":"database.example.org/v1alpha1","kind":"Database","metadata":{"name":"warehouse","namespace":"products","uid":"source-uid","generation":3},"spec":{"writeConnectionSecretToRef":{"name":"warehouse-connection"}},"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"Synced","status":"True"}]}}`,
			),
		); err != nil {
			t.Fatal(err)
		}
		s := &metav1.PartialObjectMetadata{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "warehouse-connection",
				Namespace: "products",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "database.example.org/v1alpha1",
						Kind:       "Database",
						Name:       "warehouse",
						UID:        "source-uid",
					},
				},
			},
		}
		source := data.ProvisionedSource{
			Adapter: "crossplane/v1",
			ResourceRef: data.ProvisionedResourceReference{
				APIVersion: "database.example.org/v1alpha1",
				Kind:       "Database",
				Name:       "warehouse",
			},
			ConnectionSecretRef: data.ConnectionSecretReference{Name: s.Name},
		}
		return c, s, source
	}
}
