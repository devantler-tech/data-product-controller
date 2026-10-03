package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	arangov1 "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Operator 1.4.5 uses Kubernetes 0.33.13: an empty PVC creation timestamp
// is encoded as null, including when other metadata fields are present.
func TestArangoPinnedOperatorPVCChecksum(t *testing.T) {
	for _, tc := range []struct {
		name             string
		metadata         metav1.ObjectMeta
		operatorMetadata string
	}{
		{"empty metadata", metav1.ObjectMeta{}, `{"creationTimestamp":null}`},
		{
			"named metadata",
			metav1.ObjectMeta{Name: "retained", Generation: 2, Labels: map[string]string{"fixture": "storage"}},
			`{"name":"retained","generation":2,"creationTimestamp":null,"labels":{"fixture":"storage"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := arangoImageFixture(t, "arangodb:3.12.12")
			d.Spec.Single.VolumeClaimTemplate = &corev1.PersistentVolumeClaim{
				ObjectMeta: tc.metadata,
				Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				}},
			}
			d.Status.AcceptedSpec.Single.VolumeClaimTemplate = d.Spec.Single.VolumeClaimTemplate.DeepCopy()
			encoded, err := json.Marshal(d.Spec)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := json.Marshal(tc.metadata)
			if err != nil {
				t.Fatal(err)
			}
			operatorEncoded := bytes.Replace(encoded,
				append([]byte(`"metadata":`), metadata...),
				[]byte(`"metadata":`+tc.operatorMetadata), 1)
			if bytes.Equal(encoded, operatorEncoded) {
				t.Fatal("regression did not exercise timestamp serialization drift")
			}
			digest := sha256.Sum256(operatorEncoded)
			checksum := hex.EncodeToString(digest[:])
			d.Status.AcceptedSpecVersion, d.Status.AppliedVersion = &checksum, checksum
			if got := arangoReadiness(d); !got.Ready {
				t.Fatalf("pinned operator accepted and applied hash rejected: %+v", got)
			}
			d.Spec.Single.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse(
				"2Gi",
			)
			if got := arangoReadiness(d); got.Ready || got.Reason != "SourceNotReady" {
				t.Fatalf("changed storage spec reused an accepted hash: %+v", got)
			}
		})
	}
}

// This value was independently reproduced with the released operator's SDK
// and Kubernetes 0.33.13, and matches its actual hosted accepted/applied hashes.
// Defaults populate PVCs in Agents and DBServers as well as Single.
func TestArangoRealOperatorSpecChecksum(t *testing.T) {
	data, err := os.ReadFile("../../../tests/provider/arango.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment arangov1.ArangoDeployment
	if err := yaml.Unmarshal(data, &deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Spec.SetDefaults("lineage")
	const expected = "52611cf3ee91d96b6755fa8272c058bccaf10209b434548108dab30377470ef2"
	modern, err := deployment.Spec.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	if modern == expected {
		t.Fatal("regression no longer exercises Kubernetes serializer drift")
	}
	actual, err := arangoSpecChecksum(deployment.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("pinned operator checksum = %s; want %s", actual, expected)
	}
}

func TestArangoPinnedOperatorStorageVectors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pvc      *corev1.PersistentVolumeClaim
		checksum string
	}{
		{"empty PVC", &corev1.PersistentVolumeClaim{}, "7cb7bad36f677606e8e81cfd92b8df3d473778c3aae68cfac12f9c509d9a0eee"},
		{"persistent PVC", &corev1.PersistentVolumeClaim{
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				}},
			},
		}, "2ffbb6c339e909c8a479781c0689f8ec71f8481a10ba77c368c6223b40575837"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := arangoImageFixture(
				t,
				"arangodb:3.12.12@sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf",
			)
			d.Spec.Single.VolumeClaimTemplate = tc.pvc
			actual, err := arangoSpecChecksum(d.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if actual != tc.checksum {
				t.Fatalf("operator vector = %s; want %s", actual, tc.checksum)
			}
		})
	}
}
