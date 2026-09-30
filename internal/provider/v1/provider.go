// Package v1 dispatches read-only engine providers without granting source lifecycle ownership.
package v1

import (
	"context"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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
	Reader client.Reader
	Mapper meta.RESTMapper
}

var _ Provider = (*Registry)(nil)

// Observe validates dispatch before reads and bounds the entire observation to five seconds.
func (r *Registry) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var selected Provider
	switch {
	case source.Engine == nil && source.Adapter == "crossplane/v1":
		selected = &provisionerv1.Crossplane{Reader: r.Reader, Mapper: r.Mapper}
	case source.Engine != nil && source.Engine.APIVersion == "engine-provider/v1" &&
		source.Engine.Type == "sql" && source.Engine.Provider == "native" && source.Adapter == "cnpg/v1":
		selected = &CloudNativePG{Reader: r.Reader}
	default:
		return unavailable(
			"EngineProviderUnsupported",
			"Select a supported engine type, provider and versioned adapter.",
		)
	}
	return selected.Observe(ctx, namespace, source)
}

func unavailable(reason, message string) provisionerv1.Observation {
	return provisionerv1.Observation{Reason: reason, Message: message}
}
