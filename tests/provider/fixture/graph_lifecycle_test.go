package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// graphLifecycleBody loads the Bash predicate used by real operator acceptance.
func graphLifecycleBody(t *testing.T, name string) string {
	t.Helper()
	script, err := os.ReadFile("../arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(script), "\n"+name+"() {\n")
	if !found {
		t.Fatal("lifecycle function missing")
	}
	body, _, found = strings.Cut(body, "\n}\n")
	if !found {
		t.Fatal("lifecycle function incomplete")
	}
	return body
}

// TestGraphRotationWaitsForAuthenticationRejection models delayed projection despite a successful old-password update.
func TestGraphRotationWaitsForAuthenticationRejection(t *testing.T) {
	body := graphLifecycleBody(t, "reader_rotation_ready")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
updates=0
database=old
kube() {
	if [[ $2 == graph-bootstrap && $5 == rotate ]]; then
	((updates+=1))
	database=old
	[[ $updates == 1 ]] || database=replacement
	return 0
	fi
	[[ $2 == deployment/graph-query && $5 == stale-password && $database == replacement ]]
}
reader_rotation_ready() {
`+body+`
}
if reader_rotation_ready; then echo 'accepted the old projected password'; exit 1; fi
reader_rotation_ready
[[ $updates == 2 ]]
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rotation predicate: %v %s", err, output)
	}
}

// TestGraphRecoveryHonorsServedStatusEndpoint covers both supported CRD status representations and rejects unknown versions.
func TestGraphRecoveryHonorsServedStatusEndpoint(t *testing.T) {
	body := graphLifecycleBody(t, "restore_members")
	for _, tc := range []struct {
		name, crd, endpoint string
		valid               bool
	}{
		{"status subresource", `{"spec":{"versions":[{"name":"v1","served":true,"subresources":{"status":{}}}]}}`, "status", true},
		{"plain status", `{"spec":{"versions":[{"name":"v1","served":true}]}}`, "main", true},
		{"unknown serving contract", `{"spec":{"versions":[{"name":"v1alpha","served":true}]}}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
test_dir=unused
kubectl() { printf '%s\n' "$DPC_TEST_CRD"; }
patched=false
kube() {
	patched=true
	endpoint=main
	for argument in "$@"; do [[ $argument != --subresource=status ]] || endpoint=status; done
	[[ $endpoint == "$DPC_TEST_ENDPOINT" ]]
}


restore_members() {
`+body+`
}
if restore_members; then
	[[ $DPC_TEST_VALID == true && $patched == true ]]
else
	[[ $DPC_TEST_VALID == false && $patched == false ]]
fi
`)
			valid := "false"
			if tc.valid {
				valid = "true"
			}
			command.Env = append(
				os.Environ(),
				"DPC_TEST_CRD="+tc.crd,
				"DPC_TEST_ENDPOINT="+tc.endpoint,
				"DPC_TEST_VALID="+valid,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("recovery endpoint: %v %s", err, output)
			}
		})
	}
}

// The operator may already remove old Pods while orphan-deleting the source.
// Missing recorded runtime objects must not abort cleanup of the remaining recorded objects.
func TestGraphRecoveryAcceptsAlreadyRemovedRuntimeObjects(t *testing.T) {
	script, err := os.ReadFile("../arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	var deletion string
	for _, line := range strings.Split(string(script), "\n") {
		if strings.HasPrefix(line, "bounded kubectl ") &&
			strings.Contains(line, "old-members.json") {
			deletion = line
			break
		}
	}
	if deletion == "" {
		t.Fatal("recorded runtime deletion missing")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
test_dir=unused
remaining() { echo 10; }
bounded() { "$@"; }
kubectl() {
	for argument in "$@"; do [[ $argument != --ignore-not-found ]] || return 0; done
	echo 'recorded Pod is already absent' >&2
	return 1
}
`+deletion)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("already removed runtime objects: %v %s", err, output)
	}
}
