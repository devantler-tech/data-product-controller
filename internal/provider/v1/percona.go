package v1

import (
	"context"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PerconaMongoDB observes a managed Document source and an independently published application password.
type PerconaMongoDB struct {
	// Reader must bypass caches so Secrets negotiate metadata-only responses.
	Reader client.Reader
}

var _ Provider = (*PerconaMongoDB)(nil)

const perconaSystemPublication = "percona-server-mongodb-users"

// Observe never authenticates to MongoDB, reads credentials or owns external resource lifecycle.
func (p *PerconaMongoDB) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	ref := source.ResourceRef
	if namespace == "" || ref.Name == "" || ref.APIVersion != "psmdb.percona.com/v1" ||
		ref.Kind != "PerconaServerMongoDB" ||
		source.Adapter != "percona-mongodb/v1" ||
		source.Engine == nil ||
		source.Engine.APIVersion != "engine-provider/v1" ||
		source.Engine.Type != "document" ||
		source.Engine.Provider != "native" ||
		(source.ConnectionSecretRef.Name == "" || source.ConnectionSecretRef.Name == perconaSystemPublication) {
		return unavailable(
			"SourceInvalid",
			"Use document/native with percona-mongodb/v1, a same-namespace PerconaServerMongoDB and a dedicated application password Secret.",
		)
	}
	if p.Reader == nil {
		return unavailable(
			"SourceUnavailable",
			"The provider reader is unavailable; check controller configuration.",
		)
	}
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(
		schema.GroupVersionKind{
			Group:   "psmdb.percona.com",
			Version: "v1",
			Kind:    "PerconaServerMongoDB",
		},
	)
	if err := p.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: ref.Name},
		cluster,
	); err != nil {
		return readFailure(
			err,
			"SourceNotFound",
			"Install the supported Percona operator and create the referenced PerconaServerMongoDB in this product's namespace.",
		)
	}
	if cluster.GetDeletionTimestamp() != nil {
		return unavailable(
			"SourceDeleting",
			"The source is being deleted; restore or replace its reference.",
		)
	}
	if observation := perconaReadiness(cluster); !observation.Ready {
		return observation
	}
	if !perconaApplicationPassword(cluster, source.ConnectionSecretRef.Name) {
		return unavailable(
			"ConnectionPublicationUnsupported",
			"Bind a dedicated password Secret to exactly one custom application user with only read roles on application databases.",
		)
	}
	secret := &metav1.PartialObjectMetadata{}
	secret.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Secret"})
	if err := p.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: source.ConnectionSecretRef.Name},
		secret,
	); err != nil {
		return readFailure(
			err,
			"ConnectionNotPublished",
			"Publish the referenced application password Secret with an owner reference to the current source.",
		)
	}
	if secret.GetDeletionTimestamp() != nil {
		return unavailable(
			"ConnectionNotPublished",
			"The connection Secret is being deleted; wait for its publisher.",
		)
	}
	for _, owner := range secret.GetOwnerReferences() {
		if owner.APIVersion == "psmdb.percona.com/v1" && owner.Kind == "PerconaServerMongoDB" &&
			owner.Name == cluster.GetName() && owner.UID == cluster.GetUID() {
			return provisionerv1.Observation{
				Ready:   true,
				Reason:  "SourceReady",
				Message: "The Document provider is ready and its declared application password Secret is published.",
			}
		}
	}
	return unavailable(
		"ConnectionOwnerMismatch",
		"The application password Secret must reference the current PerconaServerMongoDB; wait for its publisher to reconcile.",
	)
}

// perconaReadiness admits only the declared 1.23.0 single-replica-set profile.
func perconaReadiness(cluster *unstructured.Unstructured) provisionerv1.Observation {
	version, _, err := unstructured.NestedString(cluster.Object, "spec", "crVersion")
	if err != nil || version != "1.23.0" {
		return unavailable(
			"SourceVersionUnsupported",
			"Use the supported Percona 1.23.0 resource profile and operator installation.",
		)
	}
	paused, _, err := unstructured.NestedBool(cluster.Object, "spec", "pause")
	if err != nil {
		return perconaInvalid()
	}
	if paused {
		return unavailable(
			"SourcePaused",
			"Resume the externally operated source before using this product.",
		)
	}
	if cluster.GetUID() == "" || !perconaFalse(cluster.Object, "spec", "unmanaged") ||
		!perconaFalse(cluster.Object, "spec", "sharding", "enabled") {
		return perconaInvalid()
	}
	replicas, found, err := unstructured.NestedSlice(cluster.Object, "spec", "replsets")
	if err != nil || !found || len(replicas) != 1 {
		return perconaInvalid()
	}
	replica, ok := replicas[0].(map[string]any)
	if !ok || !perconaFalse(replica, "arbiter", "enabled") ||
		!perconaFalse(replica, "nonvoting", "enabled") ||
		!perconaFalse(replica, "hidden", "enabled") {
		return perconaInvalid()
	}
	external, _, err := unstructured.NestedSlice(replica, "externalNodes")
	if err != nil || len(external) != 0 {
		return perconaInvalid()
	}
	name, _, err := unstructured.NestedString(replica, "name")
	if err != nil || name == "" {
		return perconaInvalid()
	}
	desired, _, err := unstructured.NestedInt64(replica, "size")
	if err != nil || desired <= 0 {
		return perconaInvalid()
	}
	state, _, err := unstructured.NestedString(cluster.Object, "status", "state")
	if err == nil && state == "error" {
		return unavailable(
			"SourceFailed",
			"The operator reports a source error; inspect the independently operated source.",
		)
	}
	if err != nil || state != "ready" {
		return perconaNotReady()
	}
	generation, present, err := unstructured.NestedInt64(
		cluster.Object,
		"status",
		"observedGeneration",
	)
	if err != nil || (present && generation != cluster.GetGeneration()) {
		return perconaNotReady()
	}
	for _, field := range []string{"size", "ready"} {
		count, _, err := unstructured.NestedInt64(cluster.Object, "status", field)
		if err != nil || count != desired {
			return perconaNotReady()
		}
	}
	return provisionerv1.Observation{Ready: true}
}

func perconaFalse(object map[string]any, fields ...string) bool {
	value, _, err := unstructured.NestedBool(object, fields...)
	return err == nil && !value
}

func perconaInvalid() provisionerv1.Observation {
	return unavailable(
		"SourceInvalid",
		"Use one managed, unsharded replica set with positive size and no arbiter, non-voting, hidden or external members.",
	)
}

func perconaNotReady() provisionerv1.Observation {
	return unavailable(
		"SourceNotReady",
		"The source must report ready with every requested replica ready; inspect its status.",
	)
}

// perconaApplicationPassword verifies declarations, not actual credentials or effective database privileges.
func perconaApplicationPassword(cluster *unstructured.Unstructured, secretName string) bool {
	systemSecret, _, err := unstructured.NestedString(cluster.Object, "spec", "secrets", "users")
	if err != nil || secretName == systemSecret || secretName == perconaSystemPublication {
		return false
	}
	clusterName := cluster.GetName()
	if secretName == "internal-"+clusterName+"-users" ||
		secretName == clusterName+"-databaseadmin-conn-str" ||
		secretName == clusterName+"-custom-user-secret" ||
		secretName == clusterName+"-custom-user-secret-conn-str" {
		return false
	}
	users, found, err := unstructured.NestedSlice(cluster.Object, "spec", "users")
	if err != nil || !found {
		return false
	}
	matches := 0
	names := make(map[string]struct{}, len(users))
	for _, item := range users {
		user, ok := item.(map[string]any)
		if !ok {
			return false
		}
		name, _, err := unstructured.NestedString(user, "name")
		if err != nil || name == "" {
			return false
		}
		if _, duplicate := names[name]; duplicate {
			return false
		}
		names[name] = struct{}{}
		password, _, err := unstructured.NestedString(user, "passwordSecretRef", "name")
		if err != nil {
			return false
		}
		// A password cannot reuse any user's operator-managed connection-string publication.
		if password != "" && secretName == password+"-conn-str" {
			return false
		}
		if password != secretName {
			continue
		}
		matches++
		if !perconaReadOnlyUser(user) {
			return false
		}
	}
	return matches == 1
}

func perconaReadOnlyUser(user map[string]any) bool {
	name, _, err := unstructured.NestedString(user, "name")
	if err != nil || name == "" {
		return false
	}
	switch name {
	case "clusterAdmin",
		"clusterMonitor",
		"databaseAdmin",
		"userAdmin",
		"backup",
		"operator",
		"internal":
		return false
	}
	authDB, _, err := unstructured.NestedString(user, "db")
	if err != nil || authDB == "" || authDB == "$external" {
		return false
	}
	roles, found, err := unstructured.NestedSlice(user, "roles")
	if err != nil || !found || len(roles) == 0 {
		return false
	}
	for _, item := range roles {
		role, ok := item.(map[string]any)
		if !ok {
			return false
		}
		roleName, _, err := unstructured.NestedString(role, "name")
		if err != nil || roleName != "read" {
			return false
		}
		db, _, err := unstructured.NestedString(role, "db")
		if err != nil || db == "" || db == "admin" || db == "local" || db == "config" ||
			db == "$external" {
			return false
		}
	}
	return true
}
