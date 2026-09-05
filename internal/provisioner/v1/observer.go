// Package v1 defines the read-only provisioner observation contract.
package v1

import (
	"context"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Observation contains only public-safe readiness information, never provider messages or credentials.
type Observation struct {
	Ready   bool
	Reason  string
	Message string
}

// Observer projects readiness without creating, modifying, or deleting a source or its credentials.
type Observer interface {
	Observe(context.Context, string, datav1alpha1.ProvisionedSource) Observation
}

// Crossplane observes namespaced resources using the crossplane/v1 condition contract.
type Crossplane struct {
	// Reader must be uncached so Secret requests return only metadata.
	Reader client.Reader
	Mapper meta.RESTMapper
}

var _ Observer = (*Crossplane)(nil)

// Observe requires Ready and Synced conditions and a non-deleting connection Secret.
func (c *Crossplane) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) Observation {
	version, err := schema.ParseGroupVersion(source.ResourceRef.APIVersion)
	if err != nil || version.Group == "" || source.Adapter != "crossplane/v1" || namespace == "" ||
		source.ResourceRef.Kind == "" || source.ResourceRef.Name == "" || source.ConnectionSecretRef.Name == "" {
		return unavailable(
			"SourceInvalid",
			"Use the crossplane/v1 adapter with a namespaced custom-resource reference and connection Secret.",
		)
	}
	if c.Reader == nil || c.Mapper == nil {
		return unavailable(
			"SourceUnavailable",
			"The provisioner reader is unavailable; check controller configuration.",
		)
	}
	mapping, err := c.Mapper.RESTMapping(
		schema.GroupKind{Group: version.Group, Kind: source.ResourceRef.Kind},
		version.Version,
	)
	if err != nil {
		return unavailable(
			"SourceAPIUnavailable",
			"Install the referenced provisioner's API and allow discovery before retrying.",
		)
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return unavailable(
			"SourceScopeUnsupported",
			"The referenced provisioner resource must be namespaced.",
		)
	}
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(version.WithKind(source.ResourceRef.Kind))
	if err := c.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: source.ResourceRef.Name},
		resource,
	); err != nil {
		if apierrors.IsNotFound(err) {
			return unavailable(
				"SourceNotFound",
				"Create the referenced provisioner resource in this product's namespace.",
			)
		}
		return readFailure(err)
	}
	if resource.GetDeletionTimestamp() != nil {
		return unavailable(
			"SourceDeleting",
			"The provisioner resource is being deleted; restore or replace the source reference.",
		)
	}
	if !crossplaneReady(resource) {
		return unavailable(
			"SourceNotReady",
			"The provisioner must report Ready=True and Synced=True; inspect its conditions for details.",
		)
	}
	connectionName, _, nameErr := unstructured.NestedString(
		resource.Object,
		"spec",
		"writeConnectionSecretToRef",
		"name",
	)
	connectionNamespace, _, namespaceErr := unstructured.NestedString(
		resource.Object,
		"spec",
		"writeConnectionSecretToRef",
		"namespace",
	)
	if nameErr != nil || namespaceErr != nil || connectionName != source.ConnectionSecretRef.Name ||
		(connectionNamespace != "" && connectionNamespace != namespace) {
		return unavailable(
			"ConnectionReferenceMismatch",
			"The provisioner's connection Secret reference must match this product's same-namespace connection reference.",
		)
	}
	secret := &metav1.PartialObjectMetadata{}
	secret.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Secret"})
	if err := c.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: source.ConnectionSecretRef.Name},
		secret,
	); err != nil {
		if apierrors.IsNotFound(err) {
			return unavailable(
				"ConnectionNotPublished",
				"The provisioner must publish the referenced connection Secret in this product's namespace.",
			)
		}
		return readFailure(err)
	}
	if secret.GetDeletionTimestamp() != nil {
		return unavailable(
			"ConnectionNotPublished",
			"The connection Secret is being deleted; wait for the provisioner to republish it.",
		)
	}
	owned := false
	for _, owner := range secret.GetOwnerReferences() {
		ownerVersion, ownerErr := schema.ParseGroupVersion(owner.APIVersion)
		// UID identifies the object across served-version changes; group and kind
		// keep the reference bound to the selected provisioner API.
		if ownerErr == nil && ownerVersion.Version != "" &&
			ownerVersion.Group == version.Group &&
			resource.GetUID() != "" && owner.UID == resource.GetUID() &&
			owner.Name == resource.GetName() &&
			owner.Kind == resource.GetKind() {
			owned = true
			break
		}
	}
	if !owned {
		return unavailable(
			"ConnectionOwnerMismatch",
			"The connection Secret must belong to the current provisioner resource; wait for its publisher to reconcile.",
		)
	}
	return Observation{
		Ready:   true,
		Reason:  "SourceReady",
		Message: "The provisioner is ready and its connection Secret is published.",
	}
}

func unavailable(reason, message string) Observation {
	return Observation{Reason: reason, Message: message}
}

func readFailure(err error) Observation {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return unavailable(
			"SourceAccessDenied",
			"Grant the controller get access to the referenced resource and connection Secret in this namespace.",
		)
	}
	return unavailable(
		"SourceUnavailable",
		"The source could not be observed; check API availability and controller access.",
	)
}

func crossplaneReady(resource *unstructured.Unstructured) bool {
	if generation, found, err := unstructured.NestedInt64(
		resource.Object,
		"status",
		"observedGeneration",
	); err != nil ||
		(found && generation != resource.GetGeneration()) {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(resource.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	seen := make(map[string]bool, 2)
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			return false
		}
		name, _, err := unstructured.NestedString(condition, "type")
		if err != nil {
			return false
		}
		if name != "Ready" && name != "Synced" {
			continue
		}
		if seen[name] {
			return false
		}
		status, _, err := unstructured.NestedString(condition, "status")
		if err != nil || status != "True" {
			return false
		}
		// Crossplane condition generations are optional. An explicit generation must be current.
		generation, found, err := unstructured.NestedInt64(condition, "observedGeneration")
		if err != nil || (found && generation != resource.GetGeneration()) {
			return false
		}
		seen[name] = true
	}
	return seen["Ready"] && seen["Synced"]
}
