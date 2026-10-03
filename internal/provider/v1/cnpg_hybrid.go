package v1

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CloudNativePGHybrid observes independently operated PostgreSQL capabilities and reader publications.
// It neither initializes extensions nor obtains database credentials or connections.
type CloudNativePGHybrid struct {
	Reader client.Reader
}

var _ Provider = (*CloudNativePGHybrid)(nil)

// Observe validates the source profile before a metadata-only application publication read.
func (c *CloudNativePGHybrid) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	ref := source.ResourceRef
	if namespace == "" || ref.Name == "" || ref.APIVersion != "postgresql.cnpg.io/v1" ||
		ref.Kind != "Cluster" ||
		source.Adapter != "cnpg-hybrid/v1" ||
		source.Engine == nil ||
		source.Engine.APIVersion != "engine-provider/v1" ||
		source.Engine.Provider != "cnpg-hybrid" ||
		(source.Engine.Type != "document" && source.Engine.Type != "graph") ||
		!hybridReaderSecret(ref.Name, source.ConnectionSecretRef.Name) {
		return unavailable(
			"SourceInvalid",
			"Use document or graph with cnpg-hybrid/v1, a same-namespace Cluster and a dedicated application reader publication.",
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
	if !hybridSourceProfile(cluster, source.Engine.Type) {
		return unavailable(
			"SourceProfileUnsupported",
			"Use the supported immutable PostgreSQL image and declared hybrid capability configuration.",
		)
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
			"Wait for the independent publisher's application reader Secret.",
		)
	}
	if secret.GetDeletionTimestamp() != nil {
		return unavailable(
			"ConnectionNotPublished",
			"The application Secret is being deleted; wait for its publisher.",
		)
	}
	if !hybridCurrentOwner(cluster, secret) {
		return unavailable(
			"ConnectionOwnerMismatch",
			"The application reader Secret must identify the current Cluster UID; reconcile its publication.",
		)
	}
	if !hybridPublication(secret.Annotations, cluster.GetGeneration(), source.Engine.Type) {
		return unavailable(
			"ConnectionPublicationUnsupported",
			"Publish the supported read-only capability metadata for the current Cluster generation after verifying the application query.",
		)
	}
	return provisionerv1.Observation{
		Ready:   true,
		Reason:  "SourceReady",
		Message: "The hybrid source is ready and its current application reader capability is published.",
	}
}

// hybridReaderSecret excludes bootstrap, replication, server and operator credential publications.
func hybridReaderSecret(cluster, secret string) bool {
	if secret == "" {
		return false
	}
	for _, suffix := range []string{"-app", "-superuser", "-replication", "-server", "-ca", "-client"} {
		if secret == cluster+suffix {
			return false
		}
	}
	return true
}

// hybridSourceProfile requires an exact supported image; mutable catalog indirection cannot be observed by this adapter.
func hybridSourceProfile(cluster *unstructured.Unstructured, engine string) bool {
	if _, found, err := unstructured.NestedFieldNoCopy(
		cluster.Object,
		"spec",
		"imageCatalogRef",
	); err != nil ||
		found {
		return false
	}
	image, _, err := unstructured.NestedString(cluster.Object, "spec", "imageName")
	if err != nil || (engine != "document" && engine != "graph") {
		return false
	}
	currentImage, reported, err := unstructured.NestedString(cluster.Object, "status", "image")
	if err != nil || !reported || currentImage != image {
		return false
	}
	const repository = "ghcr.io/cloudnative-pg/postgresql"
	const digest = "sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712"
	if image == repository+"@"+digest || image == repository+":17.11-minimal-trixie@"+digest {
		return engine == "document"
	}
	// This signed publication proof is replaced by the verified stable release
	// artifact before the hybrid delivery is opened for review.
	const ageRepository = "ghcr.io/devantler-tech/data-product-controller-postgresql-age"
	const ageDigest = "sha256:c24fab14cdede789cbe4c13a9a218571b75b55826161e4b3283f82c6ea4d3c4f"
	const ageTag = "17.11-age1.7.0-dpc0.0.0-age-proof.01a0fdb3.1"
	if image != ageRepository+"@"+ageDigest && image != ageRepository+":"+ageTag+"@"+ageDigest {
		return false
	}
	return engine == "document" || hybridAGEPreload(cluster)
}

// hybridCurrentOwner binds only to the observed source's live identity without adopting either object.
func hybridCurrentOwner(
	cluster *unstructured.Unstructured,
	secret *metav1.PartialObjectMetadata,
) bool {
	if cluster.GetUID() == "" {
		return false
	}
	for _, owner := range secret.OwnerReferences {
		if owner.APIVersion == "postgresql.cnpg.io/v1" && owner.Kind == "Cluster" &&
			owner.Name == cluster.GetName() && owner.UID == cluster.GetUID() {
			return true
		}
	}
	return false
}

var hybridIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// hybridAGEPreload checks declared operator configuration; effective preloading is verified independently.
func hybridAGEPreload(cluster *unstructured.Unstructured) bool {
	libraries, declared, err := unstructured.NestedStringSlice(cluster.Object,
		"spec", "postgresql", "shared_preload_libraries")
	if err != nil || !declared {
		return false
	}
	for _, library := range libraries {
		if library == "age" {
			return true
		}
	}
	return false
}

// hybridPublication validates bounded publisher intent, not the live database's effective grants.
func hybridPublication(annotations map[string]string, generation int64, engine string) bool {
	const prefix = "data.devantler.tech/cnpg-hybrid-"
	if generation <= 0 || annotations[prefix+"publication"] != "v1" ||
		annotations[prefix+"access"] != "read-only" ||
		annotations[prefix+"source-generation"] != strconv.FormatInt(generation, 10) {
		return false
	}
	for _, field := range []string{"database", "user"} {
		if !hybridIdentifier.MatchString(annotations[prefix+field]) {
			return false
		}
	}
	user := annotations[prefix+"user"]
	if user == "postgres" || user == "streaming_replica" || strings.HasPrefix(user, "pg_") ||
		strings.HasPrefix(user, "cnpg_") {
		return false
	}
	if engine == "graph" {
		return annotations[prefix+"capability"] == "age/1.7.0" &&
			hybridIdentifier.MatchString(annotations[prefix+"graph"])
	}
	if engine != "document" || annotations[prefix+"capability"] != "jsonb/v1" {
		return false
	}
	for _, field := range []string{"schema", "table", "column"} {
		if !hybridIdentifier.MatchString(annotations[prefix+field]) {
			return false
		}
	}
	return true
}
