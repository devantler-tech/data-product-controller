package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLifecycleOwnershipRejectsReplacedOrProductOwnedResources pins the real jq predicate's ownership fence.
func TestLifecycleOwnershipRejectsReplacedOrProductOwnedResources(t *testing.T) {
	const resource = `{"kind":"Deployment","metadata":{"name":"dpc-contract-probe","namespace":"products","uid":"probe-uid","ownerReferences":[]}}`
	for _, tc := range []struct {
		name, input string
		accepted    bool
	}{
		{"independent", resource, true},
		{"owned by product", strings.Replace(resource, `"ownerReferences":[]`, `"ownerReferences":[{"uid":"product-uid"}]`, 1), false},
		{"deleting", strings.Replace(resource, `"uid":"probe-uid"`, `"uid":"probe-uid","deletionTimestamp":"2026-10-03T00:00:00Z"`, 1), false},
		{"missing UID", strings.Replace(resource, `"probe-uid"`, `""`, 1), false},
		{"wrong namespace", strings.Replace(resource, `"products"`, `"foreign"`, 1), false},
		{"wrong name", strings.Replace(resource, `"dpc-contract-probe"`, `"foreign-probe"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := lifecycleFilter(
				t,
				tc.input,
				"--arg",
				"mode",
				"ownership",
				"--arg",
				"product_uid",
				"product-uid",
				"--argjson",
				"wanted",
				`[{"kind":"Deployment","name":"dpc-contract-probe"}]`,
			)
			if (err == nil) != tc.accepted {
				t.Fatalf("ownership accepted=%v, want=%v: %s", err == nil, tc.accepted, output)
			}
			if tc.accepted &&
				strings.TrimSpace(output) != `{"Deployment/dpc-contract-probe":"probe-uid"}` {
				t.Fatal("ownership result did not retain the actual resource identity")
			}
		})
	}
}

// TestLifecycleRolloutRejectsOldServingCapacity catches stale/partial rollouts concealed by a ready replica.
func TestLifecycleRolloutRejectsOldServingCapacity(t *testing.T) {
	const workload = `{"kind":"Deployment","metadata":{"name":"dpc-http-source","namespace":"products","generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":0}}`
	for _, tc := range []struct {
		name, input, mode string
		accepted          bool
	}{
		{"complete", workload, "full", true},
		{"stale observed generation", strings.Replace(workload, `"observedGeneration":2`, `"observedGeneration":1`, 1), "full", false},
		{"old ready extra replica", strings.Replace(workload, `"replicas":1,"updatedReplicas":1`, `"replicas":2,"updatedReplicas":1`, 1), "full", false},
		{"partial while old replica serves", strings.Replace(workload, `"replicas":1,"updatedReplicas":1`, `"replicas":2,"updatedReplicas":1`, 1), "partial", true},
		{"partial without actual serving capacity", strings.Replace(workload, `"readyReplicas":1`, `"readyReplicas":0`, 1), "partial", false},
		{"old generation cannot establish new rollout", strings.Replace(workload, `"generation":2`, `"generation":1`, 1), "partial", false},
		{"scaled zero", strings.ReplaceAll(workload, `:1`, `:0`), "zero", true},
		{"missing availability", strings.Replace(workload, `"availableReplicas":1`, `"availableReplicas":0`, 1), "full", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := lifecycleFilter(
				t,
				tc.input,
				"--arg",
				"mode",
				tc.mode,
				"--arg",
				"name",
				"dpc-http-source",
				"--argjson",
				"previous",
				"1",
			)
			if (err == nil) != tc.accepted {
				t.Fatalf("rollout accepted=%v, want=%v: %s", err == nil, tc.accepted, output)
			}
		})
	}
}

// lifecycleFilter evaluates the actual harness predicate against one locally authored resource snapshot.
func lifecycleFilter(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	filter, err := filepath.Abs("../lifecycle-state.jq")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- jq runs only the repository predicate with locally authored snapshots and arguments.
	command := exec.CommandContext(
		t.Context(),
		"jq",
		append([]string{"-ceS", "-f", filter}, args...)...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	return string(output), err
}
