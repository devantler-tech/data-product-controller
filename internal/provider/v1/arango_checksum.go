package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	arangov1 "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// arangoSpecChecksum preserves the pinned 1.4.5 operator's Kubernetes 0.33
// encoding. Kubernetes 0.37 omits zero ObjectMeta creation timestamps; 0.33
// encodes null. Replace only complete typed PVC fragments, preserving every
// surrounding field's order and value instead of re-encoding the spec as a map.
func arangoSpecChecksum(spec arangov1.DeploymentSpec) (string, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	for _, name := range arangov1.AllServerGroups {
		group := spec.GetServerGroupSpec(name)
		if group.VolumeClaimTemplate == nil {
			continue
		}
		pvc := group.VolumeClaimTemplate
		current, marshalErr := json.Marshal(pvc)
		if marshalErr != nil {
			return "", marshalErr
		}
		metadata, marshalErr := json.Marshal(pvc.ObjectMeta)
		if marshalErr != nil {
			return "", marshalErr
		}
		var legacyMetadata arangoOperatorMetadata
		if marshalErr := json.Unmarshal(metadata, &legacyMetadata); marshalErr != nil {
			return "", marshalErr
		}
		legacy, marshalErr := json.Marshal(legacyMetadata)
		if marshalErr != nil {
			return "", marshalErr
		}
		operator := bytes.Replace(current,
			append([]byte(`"metadata":`), metadata...),
			append([]byte(`"metadata":`), legacy...), 1)
		encoded = bytes.ReplaceAll(encoded, current, operator)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// Keep ObjectMeta field order and tags used by the pinned operator. In
// particular, CreationTimestamp intentionally has no omitzero tag.
type arangoOperatorMetadata struct {
	Name                       string                      `json:"name,omitempty"`
	GenerateName               string                      `json:"generateName,omitempty"`
	Namespace                  string                      `json:"namespace,omitempty"`
	SelfLink                   string                      `json:"selfLink,omitempty"`
	UID                        types.UID                   `json:"uid,omitempty"`
	ResourceVersion            string                      `json:"resourceVersion,omitempty"`
	Generation                 int64                       `json:"generation,omitempty"`
	CreationTimestamp          metav1.Time                 `json:"creationTimestamp,omitempty"`
	DeletionTimestamp          *metav1.Time                `json:"deletionTimestamp,omitempty"`
	DeletionGracePeriodSeconds *int64                      `json:"deletionGracePeriodSeconds,omitempty"`
	Labels                     map[string]string           `json:"labels,omitempty"`
	Annotations                map[string]string           `json:"annotations,omitempty"`
	OwnerReferences            []metav1.OwnerReference     `json:"ownerReferences,omitempty"`
	Finalizers                 []string                    `json:"finalizers,omitempty"`
	ManagedFields              []metav1.ManagedFieldsEntry `json:"managedFields,omitempty"`
}
