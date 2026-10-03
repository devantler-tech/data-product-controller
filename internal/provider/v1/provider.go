// Package v1 dispatches read-only engine providers without granting source lifecycle ownership.
package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Provider observes creation, readiness and connection publication. The external operator owns
// creation and deletion; providers must never mutate resources, adopt them or inspect credentials.
type Provider interface {
	Observe(context.Context, string, datav1alpha1.ProvisionedSource) provisionerv1.Observation
}

// Registry binds supported engine selections to exactly one versioned observation implementation.
// New providers and their admission rules are registered here without editing reconciliation.
type Registry struct {
	// Reader uses fixed mappings for the supported typed engine APIs.
	Reader client.Reader
	// LegacyReader retains the separately configured Crossplane observation contract.
	LegacyReader client.Reader
	Mapper       meta.RESTMapper
}

var _ Provider = (*Registry)(nil)

// NewEngineReader avoids lazy API discovery and requests Secrets as metadata through an uncached client.
func NewEngineReader(config *rest.Config) (client.Reader, error) {
	if config == nil {
		return nil, fmt.Errorf("engine provider Kubernetes configuration is required")
	}
	mapper := meta.NewDefaultRESTMapper(
		[]schema.GroupVersion{
			{Group: "postgresql.cnpg.io", Version: "v1"},
			{Group: "psmdb.percona.com", Version: "v1"},
			{Group: "database.arangodb.com", Version: "v1"},
			{Version: "v1"},
		},
	)
	mapper.Add(
		schema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"},
		meta.RESTScopeNamespace,
	)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Secret"}, meta.RESTScopeNamespace)
	mapper.Add(
		schema.GroupVersionKind{
			Group:   "psmdb.percona.com",
			Version: "v1",
			Kind:    "PerconaServerMongoDB",
		},
		meta.RESTScopeNamespace,
	)
	mapper.Add(
		schema.GroupVersionKind{
			Group:   "database.arangodb.com",
			Version: "v1",
			Kind:    "ArangoDeployment",
		},
		meta.RESTScopeNamespace,
	)
	bounded := rest.CopyConfig(config)
	bounded.Timeout = 5 * time.Second
	bounded.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return metadataOnlyTransport{next: next}
	})
	reader, err := client.New(bounded, client.Options{Scheme: runtime.NewScheme(), Mapper: mapper})
	if err != nil {
		return nil, fmt.Errorf("create engine provider reader: %w", err)
	}
	return reader, nil
}

// Observe validates dispatch before reads and bounds typed observations to five seconds.
// Legacy Crossplane discovery retains its own API discovery behavior.
func (r *Registry) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	var selected Provider
	switch {
	case source.Engine == nil && source.Adapter == "crossplane/v1":
		reader := r.LegacyReader
		if reader == nil {
			reader = r.Reader
		}
		selected = &provisionerv1.Crossplane{Reader: reader, Mapper: r.Mapper}
	case source.Engine != nil && source.Engine.APIVersion == "engine-provider/v1" &&
		source.Engine.Type == "sql" && source.Engine.Provider == "native" && source.Adapter == "cnpg/v1":
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		selected = &CloudNativePG{Reader: r.Reader}
	case source.Engine != nil && source.Engine.APIVersion == "engine-provider/v1" &&
		source.Engine.Type == "document" && source.Engine.Provider == "native" && source.Adapter == "percona-mongodb/v1":
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		selected = &PerconaMongoDB{Reader: r.Reader}
	case source.Engine != nil && source.Engine.APIVersion == "engine-provider/v1" &&
		source.Engine.Type == "graph" && source.Engine.Provider == "native" && source.Adapter == "arangodb/v1":
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		selected = &ArangoDB{Reader: r.Reader}
	default:
		return unavailable(
			"EngineProviderUnsupported",
			"Select a supported engine type, provider and versioned adapter.",
		)
	}
	return selected.Observe(ctx, namespace, source)
}

// unavailable constructs an unready observation with an explicit, caller-supplied public explanation.
func unavailable(reason, message string) provisionerv1.Observation {
	return provisionerv1.Observation{Reason: reason, Message: message}
}
