package v1

import (
	"context"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CloudNativePG observes a SQL Cluster and its operator-generated application Secret.
type CloudNativePG struct {
	// Reader must bypass caches so Secrets negotiate metadata-only responses.
	Reader client.Reader
}

var _ Provider = (*CloudNativePG)(nil)

// Observe retains external ownership and never authenticates to PostgreSQL or requests Secret values.
func (c *CloudNativePG) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	ref := source.ResourceRef
	if namespace == "" || ref.Name == "" ||
		ref.APIVersion != "postgresql.cnpg.io/v1" ||
		ref.Kind != "Cluster" ||
		source.Adapter != "cnpg/v1" ||
		source.Engine == nil ||
		source.Engine.APIVersion != "engine-provider/v1" ||
		source.Engine.Type != "sql" ||
		source.Engine.Provider != "native" ||
		source.ConnectionSecretRef.Name != ref.Name+"-app" {
		return unavailable(
			"SourceInvalid",
			"Use sql/native with cnpg/v1 and a same-namespace Cluster's generated application Secret.",
		)
	}
	if c.Reader == nil {
		return unavailable(
			"SourceUnavailable",
			"The provider reader is unavailable; check controller configuration.",
		)
	}
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(
		schema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"},
	)
	if err := c.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: ref.Name},
		cluster,
	); err != nil {
		return readFailure(
			err,
			"SourceNotFound",
			"Create the referenced CloudNativePG Cluster in this product's namespace.",
		)
	}
	if cluster.GetDeletionTimestamp() != nil {
		return unavailable(
			"SourceDeleting",
			"The source is being deleted; restore or replace its reference.",
		)
	}
	// A supplied bootstrap Secret is independently owned and is outside the generated-publication contract.
	for _, bootstrap := range []string{"initdb", "recovery", "pg_basebackup"} {
		if _, present, err := unstructured.NestedFieldNoCopy(
			cluster.Object,
			"spec",
			"bootstrap",
			bootstrap,
			"secret",
		); err != nil ||
			present {
			return unavailable(
				"ConnectionPublicationUnsupported",
				"Use the operator-generated application Secret; custom bootstrap credentials require a separate adapter.",
			)
		}
	}
	if !cnpgReady(cluster) {
		return unavailable(
			"SourceNotReady",
			"The Cluster must report Ready=True, all requested instances ready and a settled primary; inspect its status.",
		)
	}
	secret := &metav1.PartialObjectMetadata{}
	secret.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Secret"})
	if err := c.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: source.ConnectionSecretRef.Name},
		secret,
	); err != nil {
		return readFailure(
			err,
			"ConnectionNotPublished",
			"Wait for the operator to publish the generated application Secret.",
		)
	}
	if secret.GetDeletionTimestamp() != nil {
		return unavailable(
			"ConnectionNotPublished",
			"The connection Secret is being deleted; wait for its publisher.",
		)
	}
	for _, owner := range secret.GetOwnerReferences() {
		version, err := schema.ParseGroupVersion(owner.APIVersion)
		if err == nil && version.Group == "postgresql.cnpg.io" && version.Version != "" &&
			owner.Kind == "Cluster" &&
			owner.Name == cluster.GetName() &&
			cluster.GetUID() != "" &&
			owner.UID == cluster.GetUID() {
			return provisionerv1.Observation{
				Ready:   true,
				Reason:  "SourceReady",
				Message: "The SQL provider is ready and its generated application Secret is published.",
			}
		}
	}
	return unavailable(
		"ConnectionOwnerMismatch",
		"The application Secret must belong to the current Cluster; wait for its publisher to reconcile.",
	)
}

func readFailure(err error, missingReason, missingMessage string) provisionerv1.Observation {
	if apierrors.IsNotFound(err) {
		return unavailable(missingReason, missingMessage)
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return unavailable(
			"SourceAccessDenied",
			"Grant exact-name get access to the provider resource and its application Secret in this namespace.",
		)
	}
	return unavailable(
		"SourceUnavailable",
		"The source could not be observed; check API availability and scoped controller access.",
	)
}

// cnpgReady follows CNPG's optional condition generations and checks all published replica/primary facts.
func cnpgReady(cluster *unstructured.Unstructured) bool {
	if generation, present, err := unstructured.NestedInt64(
		cluster.Object,
		"status",
		"observedGeneration",
	); err != nil ||
		(present && generation != cluster.GetGeneration()) {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(cluster.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	ready := false
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			return false
		}
		kind, _, err := unstructured.NestedString(condition, "type")
		if err != nil {
			return false
		}
		if kind != "Ready" {
			continue
		}
		status, _, err := unstructured.NestedString(condition, "status")
		if err != nil || status != "True" || ready {
			return false
		}
		generation, present, err := unstructured.NestedInt64(condition, "observedGeneration")
		if err != nil || (present && generation != cluster.GetGeneration()) {
			return false
		}
		ready = true
	}
	desired, _, err := unstructured.NestedInt64(cluster.Object, "spec", "instances")
	if err != nil || desired <= 0 {
		return false
	}
	for _, field := range []string{"instances", "readyInstances"} {
		count, _, err := unstructured.NestedInt64(cluster.Object, "status", field)
		if err != nil || count != desired {
			return false
		}
	}
	current, _, err := unstructured.NestedString(cluster.Object, "status", "currentPrimary")
	if err != nil || current == "" {
		return false
	}
	target, _, err := unstructured.NestedString(cluster.Object, "status", "targetPrimary")
	return err == nil && ready && current == target
}
