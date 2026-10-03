package v1

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	arangov1 "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ArangoDB observes the pinned Single profile and independently published application metadata.
type ArangoDB struct{ Reader client.Reader }

var _ Provider = (*ArangoDB)(nil)

const (
	arangoImage       = "arangodb:3.12.12"
	arangoImageDigest = "sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf"
)

// arangoSupportedImage admits only the existing tag or the verified official release index.
func arangoSupportedImage(image string) bool {
	switch image {
	case arangoImage,
		arangoImage + "@" + arangoImageDigest,
		"arangodb@" + arangoImageDigest,
		"docker.io/library/" + arangoImage + "@" + arangoImageDigest,
		"docker.io/library/arangodb@" + arangoImageDigest:
		return true
	default:
		return false
	}
}

// Observe reads current operator status and Secret metadata, never database data or credentials.
func (p *ArangoDB) Observe(
	ctx context.Context,
	namespace string,
	source datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	ref := source.ResourceRef
	if namespace == "" || ref.Name == "" ||
		ref.APIVersion != "database.arangodb.com/v1" ||
		ref.Kind != "ArangoDeployment" ||
		source.Adapter != "arangodb/v1" || source.Engine == nil ||
		source.Engine.APIVersion != "engine-provider/v1" ||
		source.Engine.Type != "graph" ||
		source.Engine.Provider != "native" ||
		source.ConnectionSecretRef.Name == "" {
		return arangoInvalid()
	}
	if p.Reader == nil {
		return unavailable(
			"SourceUnavailable",
			"The provider reader is unavailable; check controller configuration.",
		)
	}
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(
		schema.GroupVersionKind{
			Group:   "database.arangodb.com",
			Version: "v1",
			Kind:    "ArangoDeployment",
		},
	)
	if err := p.Reader.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: ref.Name},
		resource,
	); err != nil {
		return readFailure(
			err,
			"SourceNotFound",
			"Install the supported ArangoDB operator and create the referenced ArangoDeployment in this product's namespace.",
		)
	}
	if resource.GetDeletionTimestamp() != nil {
		return unavailable(
			"SourceDeleting",
			"The source is being deleted; restore or replace its reference.",
		)
	}
	if resource.GetUID() == "" {
		return arangoInvalid()
	}
	data, err := resource.MarshalJSON()
	if err != nil {
		return arangoInvalid()
	}
	var deployment arangov1.ArangoDeployment
	if err := json.Unmarshal(data, &deployment); err != nil {
		return arangoInvalid()
	}
	if observation := arangoReadiness(&deployment); !observation.Ready {
		return observation
	}
	if arangoOperatorSecret(&deployment, source.ConnectionSecretRef.Name) {
		return unavailable(
			"ConnectionPublicationUnsupported",
			"Publish a dedicated application password Secret; root and operator credentials are unsupported.",
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
			"Publish the referenced application password Secret with the Graph publication contract and current source ownership.",
		)
	}
	if secret.GetDeletionTimestamp() != nil {
		return unavailable(
			"ConnectionNotPublished",
			"The application password Secret is being deleted; wait for its publisher.",
		)
	}
	if !arangoPublication(secret.GetAnnotations()) {
		return unavailable(
			"ConnectionPublicationUnsupported",
			"Publish v1 metadata for a dedicated read-only application user, database, graph and explicit collections.",
		)
	}
	for _, owner := range secret.GetOwnerReferences() {
		if owner.APIVersion == ref.APIVersion && owner.Kind == ref.Kind &&
			owner.Name == resource.GetName() &&
			owner.UID == resource.GetUID() {
			return provisionerv1.Observation{
				Ready:   true,
				Reason:  "SourceReady",
				Message: "The Graph provider reports current-spec readiness and its declared application password is published.",
			}
		}
	}
	return unavailable(
		"ConnectionOwnerMismatch",
		"The application password Secret must reference the current ArangoDeployment; wait for its publisher to reconcile.",
	)
}

// arangoReadiness hashes the live raw spec: upstream accepted-spec contains defaults and cannot replace it.
func arangoReadiness(d *arangov1.ArangoDeployment) provisionerv1.Observation {
	if d.Spec.Image == nil || !arangoSupportedImage(*d.Spec.Image) {
		return unavailable(
			"SourceVersionUnsupported",
			"Use the supported official ArangoDB 3.12.12 image and operator 1.4.5 API profile.",
		)
	}
	if d.Spec.Mode == nil || *d.Spec.Mode != arangov1.DeploymentModeSingle ||
		d.Spec.Single.Count == nil ||
		*d.Spec.Single.Count != 1 ||
		!d.Spec.Authentication.IsAuthenticated() {
		return arangoInvalid()
	}
	for _, spec := range []*arangov1.DeploymentSpec{&d.Spec, d.Status.AcceptedSpec} {
		if spec == nil {
			continue
		}
		for user := range spec.Bootstrap.PasswordSecretNames {
			if user != "root" {
				return arangoInvalid()
			}
		}
	}
	if d.Status.Phase == arangov1.DeploymentPhaseFailed ||
		d.Status.Conditions.IsTrue(arangov1.ConditionTypeUpdateFailed) ||
		d.Status.Conditions.IsTrue(arangov1.ConditionTypeUpgradeFailed) ||
		d.Status.Conditions.IsTrue(arangov1.ConditionTypeSecretsChanged) {
		return unavailable(
			"SourceFailed",
			"The operator reports a source failure; inspect the independently operated source.",
		)
	}
	accepted, err := d.IsAccepted()
	if err != nil || !accepted {
		return arangoNotReady()
	}
	current, err := d.IsUpToDate()
	acceptedSpec := d.Status.AcceptedSpec
	if err != nil || !current || d.Status.Phase != arangov1.DeploymentPhaseRunning ||
		acceptedSpec == nil || acceptedSpec.Mode == nil ||
		*acceptedSpec.Mode != arangov1.DeploymentModeSingle ||
		acceptedSpec.Single.Count == nil || *acceptedSpec.Single.Count != 1 ||
		acceptedSpec.Image == nil || *acceptedSpec.Image != *d.Spec.Image ||
		acceptedSpec.Authentication.GetJWTSecretName() == "" ||
		!acceptedSpec.Authentication.IsAuthenticated() ||
		d.Status.Conditions.IsTrue(arangov1.ConditionTypeUpdateInProgress) ||
		d.Status.Conditions.IsTrue(arangov1.ConditionTypeUpgradeInProgress) ||
		!arangoConditions(
			d.Status.Conditions,
			arangov1.ConditionTypeReady,
			arangov1.ConditionTypeSpecAccepted,
			arangov1.ConditionTypeUpToDate,
			arangov1.ConditionTypeBootstrapCompleted,
			arangov1.ConditionTypeBootstrapSucceded,
		) {
		return arangoNotReady()
	}
	image := d.Status.CurrentImage
	if !arangoCurrentImage(image, *d.Spec.Image) || len(d.Status.Members.Single) != 1 {
		return arangoNotReady()
	}
	member := d.Status.Members.Single[0]
	if member.ID == "" || member.Phase != arangov1.MemberPhaseCreated || member.Pod == nil ||
		member.Pod.Name == "" ||
		member.Pod.UID == "" ||
		!arangoCurrentImage(member.Image, *d.Spec.Image) ||
		member.Image.ImageID != image.ImageID ||
		member.ImageID != image.ImageID ||
		string(member.ArangoVersion) != "3.12.12" ||
		!arangoConditions(member.Conditions, arangov1.ConditionTypeReady) ||
		member.Conditions.IsTrue(arangov1.ConditionTypeUpdateFailed) ||
		member.Conditions.IsTrue(arangov1.ConditionTypeUpgradeFailed) ||
		member.Conditions.IsTrue(arangov1.ConditionTypePendingUpdate) ||
		member.Conditions.IsTrue(arangov1.ConditionTypeUpdating) ||
		member.Conditions.IsTrue(arangov1.ConditionTypeRestart) ||
		member.Conditions.IsTrue(arangov1.ConditionTypePendingRestart) ||
		member.Conditions.IsTrue(arangov1.ConditionTypePVCResizePending) {
		return arangoNotReady()
	}
	return provisionerv1.Observation{Ready: true}
}

// arangoCurrentImage binds the binary marker to the observed official release profile, not a license entitlement.
func arangoCurrentImage(image *arangov1.ImageInfo, expected string) bool {
	// The verified official index contains a binary that reports license=enterprise.
	// The existing tag-only profile retains its earlier Community marker contract.
	enterpriseBuild := strings.HasSuffix(expected, "@"+arangoImageDigest)
	return image != nil && image.Image == expected && image.ImageID != "" &&
		string(image.ArangoDBVersion) == "3.12.12" &&
		image.Enterprise == enterpriseBuild
}

// arangoConditions rejects ambiguous lists; condition hashes and timestamps are not freshness markers.
func arangoConditions(conditions arangov1.ConditionList, required ...arangov1.ConditionType) bool {
	seen := make(map[arangov1.ConditionType]bool, len(conditions))
	for _, condition := range conditions {
		if condition.Type == "" || (condition.Status != "True" && condition.Status != "False") {
			return false
		}
		if _, duplicate := seen[condition.Type]; duplicate {
			return false
		}
		seen[condition.Type] = condition.Status == "True"
	}
	for _, kind := range required {
		if !seen[kind] {
			return false
		}
	}
	return true
}

// arangoOperatorSecret rejects raw, default and accepted operator-owned credential names.
func arangoOperatorSecret(d *arangov1.ArangoDeployment, name string) bool {
	for _, suffix := range []string{"-jwt", "-jwt-folder", "-root-password", "-exporter-jwt-token", "-ca", "-rlm", "-truststore", "-encryption-folder"} {
		if name == d.Name+suffix {
			return true
		}
	}
	for _, member := range d.Status.Members.Single {
		if name == member.ArangoMemberName(d.Name, arangov1.ServerGroupSingle)+"-tls-keyfile" {
			return true
		}
	}
	for _, spec := range []*arangov1.DeploymentSpec{&d.Spec, d.Status.AcceptedSpec} {
		if spec == nil {
			continue
		}
		if name == spec.Authentication.GetJWTSecretName() || name == spec.TLS.GetCASecretName() ||
			name == spec.Metrics.GetJWTTokenSecretName() ||
			name == spec.License.GetSecretName() ||
			name == spec.RocksDB.Encryption.GetKeySecretName() ||
			(spec.RestoreEncryptionSecret != nil && name == *spec.RestoreEncryptionSecret) {
			return true
		}
		if _, keyfile := spec.TLS.GetSNI().Mapping[name]; keyfile {
			return true
		}
		for _, registrySecret := range spec.ImagePullSecrets {
			if name == registrySecret {
				return true
			}
		}
		for _, password := range spec.Bootstrap.PasswordSecretNames {
			if name == password.Get() {
				return true
			}
		}
	}
	return false
}

var arangoApplicationName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// arangoPublication validates publisher intent; it cannot establish actual grants or password validity.
func arangoPublication(annotations map[string]string) bool {
	const prefix = "data.devantler.tech/"
	if annotations[prefix+"arango-publication"] != "v1" ||
		annotations[prefix+"arango-access"] != "read-only" {
		return false
	}
	for _, field := range []string{"arango-user", "arango-database", "arango-graph"} {
		name := annotations[prefix+field]
		if !arangoApplicationName.MatchString(name) {
			return false
		}
	}
	switch strings.ToLower(annotations[prefix+"arango-user"]) {
	case "root", "operator", "internal", "backup":
		return false
	}
	collectionNames := annotations[prefix+"arango-collections"]
	if len(collectionNames) > 64*64+63 {
		return false
	}
	collections := strings.Split(collectionNames, ",")
	if len(collections) > 64 {
		return false
	}
	seen := make(map[string]bool, len(collections))
	for _, name := range collections {
		if !arangoApplicationName.MatchString(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// arangoInvalid reports unsupported source configuration without exposing operator details.
func arangoInvalid() provisionerv1.Observation {
	return unavailable(
		"SourceInvalid",
		"Use graph/native with arangodb/v1, an authenticated single-server ArangoDeployment and a dedicated application password Secret.",
	)
}

// arangoNotReady withdraws readiness until the operator reports a consistent current specification.
func arangoNotReady() provisionerv1.Observation {
	return unavailable(
		"SourceNotReady",
		"The current specification must be accepted and applied with successful bootstrap and one ready Graph member; inspect source status.",
	)
}
