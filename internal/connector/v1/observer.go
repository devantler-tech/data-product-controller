// Package v1 defines read-only connector workload observation contracts.
package v1

import (
	"context"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Observation contains stable, public-safe readiness details without workload templates or API errors.
type Observation struct {
	Ready   bool
	Reason  string
	Message string
}

// Observer reports workload readiness without taking lifecycle or data-plane ownership.
type Observer interface {
	Observe(context.Context, string, datav1alpha1.Connector) Observation
}

// Deployment implements deployment/v1 through exact-name, uncached Kubernetes reads.
type Deployment struct {
	// Reader must bypass the cache; operators grant only named Deployment get access.
	Reader client.Reader
}

var _ Observer = (*Deployment)(nil)

// Observe requires all desired replicas to be current, ready, and available within a bounded API read.
func (d *Deployment) Observe(
	ctx context.Context,
	namespace string,
	connector datav1alpha1.Connector,
) Observation {
	ref := connector.ResourceRef
	if connector.Adapter != "deployment/v1" || ref.APIVersion != "apps/v1" ||
		ref.Kind != "Deployment" ||
		ref.Namespace != "" ||
		len(validation.IsDNS1123Label(namespace)) != 0 ||
		len(validation.IsDNS1123Subdomain(ref.Name)) != 0 {
		return unavailable(
			"ConnectorInvalid",
			"Use deployment/v1 with an apps/v1 Deployment name; omit namespace to select this product's namespace.",
		)
	}
	if d.Reader == nil {
		return unavailable(
			"ConnectorUnavailable",
			"Configure the controller's uncached connector reader.",
		)
	}
	readContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	workload := &appsv1.Deployment{}
	if err := d.Reader.Get(
		readContext,
		client.ObjectKey{Namespace: namespace, Name: ref.Name},
		workload,
	); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return unavailable(
				"ConnectorNotFound",
				"Create the referenced connector Deployment in this product's namespace.",
			)
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			return unavailable(
				"ConnectorAccessDenied",
				"Grant the controller get access to this named Deployment in the product namespace.",
			)
		default:
			return unavailable(
				"ConnectorUnavailable",
				"The Deployment could not be observed; check API availability and controller access.",
			)
		}
	}
	if workload.DeletionTimestamp != nil {
		return unavailable(
			"ConnectorDeleting",
			"The connector Deployment is being deleted; restore or replace the reference.",
		)
	}
	desired := int32(1)
	if workload.Spec.Replicas != nil {
		desired = *workload.Spec.Replicas
	}
	if desired <= 0 {
		return unavailable(
			"ConnectorScaledToZero",
			"Scale the connector Deployment to at least one replica.",
		)
	}
	status := workload.Status
	if workload.Generation <= 0 || status.ObservedGeneration != workload.Generation {
		return unavailable(
			"ConnectorStatusStale",
			"Wait for the Deployment controller to observe the current workload generation.",
		)
	}
	if status.Replicas != desired || status.UpdatedReplicas != desired ||
		status.ReadyReplicas != desired ||
		status.AvailableReplicas != desired ||
		status.UnavailableReplicas != 0 {
		return unavailable(
			"ConnectorNotReady",
			"Wait for all desired connector replicas to be updated, ready, and available; inspect the Deployment and its probes.",
		)
	}
	return Observation{
		Ready:   true,
		Reason:  "ConnectorReady",
		Message: "All desired connector replicas are current, ready, and available.",
	}
}

// unavailable describes a failed observation without exposing workload or API error contents.
func unavailable(reason, message string) Observation {
	return Observation{Reason: reason, Message: message}
}
