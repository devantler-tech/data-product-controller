package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Operator-created ArangoMembers have no deployment labels. Recovery must record the
// source-owned observed member and exclude same-ID members belonging to another source.
func TestGraphRecoveryRecordsUnlabelledOwnedMembers(t *testing.T) {
	body := graphLifecycleBody(t, "record_old_members")
	directory := t.TempDir()
	if err := os.WriteFile(directory+"/recovery-members.json",
		[]byte(`{"status":{"members":{"single":[{"id":"observed-member"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		directory+"/retained-source.json",
		[]byte(
			`{"metadata":{"uid":"observed-source"},"status":{"members":{"single":[{"id":"observed-member","pod":{"name":"lineage-sngl-observed-member-random","uid":"observed-pod"}}]}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
test_dir=$DPC_TEST_DIRECTORY
source_uid=observed-source
kube() {
	case "$2" in
	pods) printf '%s\n' '{"items":[{"kind":"Pod","metadata":{"name":"lineage-sngl-observed-member-random","namespace":"products","uid":"observed-pod","labels":{"arango_deployment":"lineage"}}},{"kind":"Pod","metadata":{"name":"lineage-single-unobserved","namespace":"products","uid":"foreign-pod","labels":{"arango_deployment":"lineage"}}}]}' ;;
	arangomembers) printf '%s\n' '{"items":[
	{"kind":"ArangoMember","metadata":{"name":"lineage-single-observed-member","namespace":"products","uid":"owned-member","ownerReferences":[{"apiVersion":"database.arangodb.com/v1","kind":"ArangoDeployment","name":"lineage","uid":"observed-source"}]},"spec":{"group":"single","id":"observed-member","deploymentUID":"observed-source"}},
	{"kind":"ArangoMember","metadata":{"name":"foreign-single-observed-member","namespace":"products","uid":"foreign-member","labels":{"arango_deployment":"lineage"},"ownerReferences":[{"apiVersion":"database.arangodb.com/v1","kind":"ArangoDeployment","name":"foreign","uid":"foreign-source"}]},"spec":{"group":"single","id":"observed-member","deploymentUID":"foreign-source"}}
	]}' ;;
	*) return 1 ;;
	esac
}
record_old_members() {
`+body+"\n}\nrecord_old_members")
	command.Env = append(os.Environ(), "DPC_TEST_DIRECTORY="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("runtime inventory: %v %s", err, output)
	}
	data, err := os.ReadFile(directory + "/old-members.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Items []struct {
			Kind     string
			Metadata struct{ UID string }
		}
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Items) != 2 || inventory.Items[0].Kind != "Pod" ||
		inventory.Items[0].Metadata.UID != "observed-pod" ||
		inventory.Items[1].Kind != "ArangoMember" ||
		inventory.Items[1].Metadata.UID != "owned-member" {
		t.Fatal(
			"unlabelled source member was lost or a foreign member entered the deletion inventory",
		)
	}
}
