package main

import (
	"strings"
	"testing"
)

// TestPublisherIdentityFollowsImmutableDeclaration binds the signer to the caller's full SHA.
func TestPublisherIdentityFollowsImmutableDeclaration(t *testing.T) {
	for _, sha := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		publisher := "devantler-tech/.github/.github/workflows/publish-app.yaml@" + sha
		got, err := publisherIdentity(
			strings.NewReader(
				"name: Publish\njobs:\n  publish:\n    uses: " + publisher + "\n  smoke:\n    runs-on: ubuntu-latest\n",
			),
		)
		if err != nil || got != "https://github.com/"+publisher {
			t.Fatalf("immutable publisher identity = %q, %v", got, err)
		}
	}
}

// TestPublisherIdentityRejectsAmbiguousOrMutableDeclarations fails closed on signer ambiguity.
func TestPublisherIdentityRejectsAmbiguousOrMutableDeclarations(t *testing.T) {
	valid := "devantler-tech/.github/.github/workflows/publish-app.yaml@" + strings.Repeat("a", 40)
	for name, source := range map[string]string{
		"missing":           "jobs: {}",
		"legacy-rollback":   "jobs:\n  publish:\n    uses: devantler-tech/actions/.github/workflows/publish-app.yaml@" + strings.Repeat("a", 40),
		"mutable":           "jobs:\n  publish:\n    uses: devantler-tech/.github/.github/workflows/publish-app.yaml@main",
		"tag":               "jobs:\n  publish:\n    uses: devantler-tech/.github/.github/workflows/publish-app.yaml@v7.23.0",
		"short-sha":         "jobs:\n  publish:\n    uses: devantler-tech/.github/.github/workflows/publish-app.yaml@0c7b6c3",
		"other-owner":       "jobs:\n  publish:\n    uses: other/.github/.github/workflows/publish-app.yaml@" + strings.Repeat("a", 40),
		"other-workflow":    "jobs:\n  publish:\n    uses: devantler-tech/.github/.github/workflows/other.yaml@" + strings.Repeat("a", 40),
		"duplicate-publish": "jobs:\n  publish:\n    uses: " + valid + "\n  publish:\n    uses: " + valid,
		"duplicate-uses":    "jobs:\n  publish:\n    uses: " + valid + "\n    uses: " + valid,
		"second-document":   "jobs:\n  publish:\n    uses: " + valid + "\n---\njobs: {}",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := publisherIdentity(strings.NewReader(source))
			if err == nil || got != "" {
				t.Fatalf("unsafe publisher admitted: %q, %v", got, err)
			}
		})
	}
}
