package main

import (
	"strings"
	"testing"
)

func TestPublisherIdentityFollowsImmutableDeclaration(t *testing.T) {
	for _, sha := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		publisher := "devantler-tech/actions/.github/workflows/publish-app.yaml@" + sha
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

func TestPublisherIdentityRejectsAmbiguousOrMutableDeclarations(t *testing.T) {
	valid := "devantler-tech/actions/.github/workflows/publish-app.yaml@" + strings.Repeat("a", 40)
	for name, source := range map[string]string{
		"missing":           "jobs: {}",
		"mutable":           "jobs:\n  publish:\n    uses: devantler-tech/actions/.github/workflows/publish-app.yaml@main",
		"other-owner":       "jobs:\n  publish:\n    uses: other/actions/.github/workflows/publish-app.yaml@" + strings.Repeat("a", 40),
		"other-workflow":    "jobs:\n  publish:\n    uses: devantler-tech/actions/.github/workflows/other.yaml@" + strings.Repeat("a", 40),
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
