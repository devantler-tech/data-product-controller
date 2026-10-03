package v1

import (
	"encoding/json"
	"testing"

	arangov1 "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
)

// TestArangoImmutableImages keeps digest support within the verified official release profile.
func TestArangoImmutableImages(t *testing.T) {
	t.Parallel()
	const digest = "sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf"
	for _, image := range []string{
		"arangodb:3.12.12",
		"arangodb:3.12.12@" + digest,
		"arangodb@" + digest,
		"docker.io/library/arangodb:3.12.12@" + digest,
		"docker.io/library/arangodb@" + digest,
	} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()
			d := arangoImageFixture(t, image)
			if got := arangoReadiness(d); !got.Ready {
				t.Fatalf("supported immutable image was rejected: %+v", got)
			}
		})
	}
	for _, image := range []string{
		"arangodb:latest@" + digest,
		"arangodb:3.12.11@" + digest,
		"other.example/arangodb:3.12.12@" + digest,
		"arangodb/enterprise:3.12.12@" + digest,
		"arangodb:3.12.12@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"arangodb:3.12.12@sha256:invalid",
	} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()
			d := arangoImageFixture(t, image)
			if got := arangoReadiness(d); got.Ready || got.Reason != "SourceVersionUnsupported" {
				t.Fatalf("unsupported image observation = %+v", got)
			}
		})
	}
}

// TestArangoOfficialBinaryProfile uses the actual published image's enterprise build marker without accepting other repositories.
func TestArangoOfficialBinaryProfile(t *testing.T) {
	d := arangoImageFixture(
		t,
		"arangodb:3.12.12@sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf",
	)
	d.Status.CurrentImage.Enterprise = true
	d.Status.Members.Single[0].Image.Enterprise = true
	if got := arangoReadiness(d); !got.Ready {
		t.Fatalf("actual official binary profile rejected: %+v", got)
	}
}

// TestArangoPinnedImageContradictions prevents supported references from masking inconsistent status.
func TestArangoPinnedImageContradictions(t *testing.T) {
	t.Parallel()
	const image = "arangodb:3.12.12@sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf"
	for _, tc := range []struct {
		name   string
		mutate func(*arangov1.ArangoDeployment)
	}{
		{"accepted mutable reference", func(d *arangov1.ArangoDeployment) { d.Status.AcceptedSpec.Image = new("arangodb:3.12.12") }},
		{"current mutable reference", func(d *arangov1.ArangoDeployment) { d.Status.CurrentImage.Image = "arangodb:3.12.12" }},
		{"member mutable reference", func(d *arangov1.ArangoDeployment) { d.Status.Members.Single[0].Image.Image = "arangodb:3.12.12" }},
		{"wrong current version", func(d *arangov1.ArangoDeployment) { d.Status.CurrentImage.ArangoDBVersion = "3.12.11" }},
		{"wrong member version", func(d *arangov1.ArangoDeployment) { d.Status.Members.Single[0].ArangoVersion = "3.12.11" }},
		{"contradictory current binary marker", func(d *arangov1.ArangoDeployment) { d.Status.CurrentImage.Enterprise = false }},
		{"contradictory member binary marker", func(d *arangov1.ArangoDeployment) { d.Status.Members.Single[0].Image.Enterprise = false }},
		{"different member image ID", func(d *arangov1.ArangoDeployment) { d.Status.Members.Single[0].ImageID = "sha256:other" }},
		{"different member resolved image", func(d *arangov1.ArangoDeployment) { d.Status.Members.Single[0].Image.ImageID = "sha256:other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := arangoImageFixture(t, image)
			tc.mutate(d)
			if got := arangoReadiness(d); got.Ready || got.Reason != "SourceNotReady" {
				t.Fatalf("contradictory pinned image observation = %+v", got)
			}
		})
	}
}

// arangoImageFixture changes only the image in synthetic, current-checksum unit evidence.
func arangoImageFixture(t *testing.T, image string) *arangov1.ArangoDeployment {
	t.Helper()
	resource, _ := arangoFixture(t)
	data, err := resource.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var d arangov1.ArangoDeployment
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	d.Spec.Image = &image
	d.Status.AcceptedSpec.Image = &image
	d.Status.CurrentImage.Image = image
	d.Status.Members.Single[0].Image.Image = image
	d.Status.CurrentImage.Enterprise = image != "arangodb:3.12.12"
	d.Status.Members.Single[0].Image.Enterprise = image != "arangodb:3.12.12"
	checksum, err := d.Spec.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	d.Status.AcceptedSpecVersion, d.Status.AppliedVersion = &checksum, checksum
	return &d
}
