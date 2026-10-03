package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLifecycleBudgetsKeepProjectionTimeAndAbsoluteBounds reproduces a late second Secret projection.
func TestLifecycleBudgetsKeepProjectionTimeAndAbsoluteBounds(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"source restoration", "absolute clamp", "shared clamp", "exhausted"} {
		t.Run(name, func(t *testing.T) {
			scratch := t.TempDir()
			if err := os.WriteFile(filepath.Join(scratch, "kubeconfig"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "bash", "-c", `
source tests/source/source-lifecycle.sh
cluster_started=true
source_started=true
cluster_name=dpc-e2e-budget
source_container=$cluster_name-source
KUBECONFIG=$test_dir/kubeconfig
integration_deadline=2700
SECONDS=0
case "$BUDGET_CASE" in
	'source restoration')
		kube() { :; }
		docker() { :; }
		probe() { :; }
		source_pod() { printf '%s\n' same-pod; }
		source_lifecycle_retention_capture() { :; }
		source_secret() { [[ $1 != fixture-token-a ]] || SECONDS=436; }
		wait_for() {
			if [[ $1 == 'initial credential pair is restored' && $2 != 300 ]]; then
				printf 'Secret restoration was truncated to %s seconds\n' "$2" >&2
				exit 73
			fi
		}
		source_lifecycle_run ;;
	'absolute clamp')
		SECONDS=1000
		integration_deadline=1100
		lifecycle_begin budget 900 || exit 74
		[[ $lifecycle_suite_deadline == 1100 && $lifecycle_deadline == 1100 ]] || exit 75 ;;
	'shared clamp')
		SECONDS=600
		lifecycle_suite_deadline=650
		lifecycle_begin budget 900 || exit 76
		wait_for() { [[ $2 == 50 ]] || exit 77; }
		lifecycle_wait bounded 300 true ;;
	'exhausted')
		SECONDS=600
		lifecycle_suite_deadline=500
		lifecycle_begin budget 900 || exit 78
		wait_for() { exit 79; }
		if lifecycle_wait exhausted 300 true; then exit 80; fi ;;
	*) exit 81 ;;
esac`)
			command.Dir = root
			command.Env = append(os.Environ(), "repo_root="+root, "test_dir="+scratch,
				"BUDGET_CASE="+name)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("budget case failed: %v\n%s", err, output)
			}
		})
	}
}
