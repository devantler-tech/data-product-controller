package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Constructor status writes precede maintenance inspection. Recovery must restore
// observed identities while the operator is stopped, before its next snapshot.
func TestGraphRecoveryFencesOperatorStartupStatus(t *testing.T) {
	script, err := os.ReadFile("../arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, phase, found := strings.Cut(
		string(script),
		"\nbounded kubectl --request-timeout=0 -n products delete arangodeployment lineage --cascade=orphan",
	)
	if !found {
		t.Fatal("source recovery phase missing")
	}
	phase = "bounded kubectl --request-timeout=0 -n products delete arangodeployment lineage --cascade=orphan" + phase
	phase, _, found = strings.Cut(phase, "\nwait_for 'stale application ownership is rejected'")
	if !found {
		t.Fatal("source recovery boundary missing")
	}
	functions := ""
	for _, name := range []string{"pause_recovery_operator", "recovery_operator_stopped", "retained_members_match", "resume_recovery_operator"} {
		if strings.Contains(string(script), "\n"+name+"() {\n") {
			functions += name + "() {\n" + graphLifecycleBody(t, name) + "\n}\n"
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	program := `set -euo pipefail
test_dir=$DPC_TEST_DIRECTORY
repo_root=unused
source_uid=old-source
printf '%s\n' '{"status":{"members":{"single":[{"id":"retained","persistentVolumeClaim":{"name":"retained-pvc"},"persistentVolumeClaimName":"retained-pvc"}]}}}' >"$test_dir/recovery-members.json"
printf running >"$test_dir/operator"
remaining() { echo 10; }
bounded() { "$@"; }
yq() { echo source; }
kubectl() {
	case " $* " in
	*" delete arangodeployment "*) [[ $(cat "$test_dir/operator") == running ]]; touch "$test_dir/deleted" ;;
	*" old-members.json "*) touch "$test_dir/runtime-removed" ;;
	*" delete "*"old-members.json"*) touch "$test_dir/runtime-removed" ;;
	*" scale "*"--replicas=0"*) [[ -f $test_dir/deleted && -f $test_dir/runtime-removed ]]; printf stopped >"$test_dir/operator" ;;
	*" scale "*"--replicas=1"*) [[ -f $test_dir/quiesced && -f $test_dir/verified ]]; printf running >"$test_dir/operator" ;;
	*" rollout status "*) return 0 ;;
	*) echo "unexpected kubectl: $*" >&2; return 1 ;;
	esac
}
kube() {
	case " $* " in
	*" get deployment "*) printf '%s\n' '{"metadata":{"uid":"owned-operator","namespace":"products","labels":{"app.kubernetes.io/name":"kube-arangodb","app.kubernetes.io/instance":"arango"},"annotations":{"meta.helm.sh/release-name":"arango","meta.helm.sh/release-namespace":"products"}},"spec":{"replicas":1,"selector":{"matchLabels":{"app.kubernetes.io/name":"kube-arangodb","app.kubernetes.io/instance":"arango"}}}}' ;;
	*" get pods "*) [[ $(cat "$test_dir/operator") == stopped ]]; touch "$test_dir/quiesced"; printf '{"items":[]}' ;;
	*" apply "*) cat >/dev/null; printf '{"status":{"members":{"single":[]}}}' >"$test_dir/live" ;;
	*" get arangodeployment "*"jsonpath"*) printf new-source ;;
	*" get arangodeployment "*)
		cat "$test_dir/live"
		if [[ $(cat "$test_dir/operator") == stopped ]]; then touch "$test_dir/verified"; fi ;;
	*" annotate "*) printf '{"status":{"members":{"single":[]}}}' >"$test_dir/live" ;;
	*) echo "unexpected kube: $*" >&2; return 1 ;;
	esac
}
restore_members() {
	cp "$test_dir/recovery-members.json" "$test_dir/live"
	# A live operator's constructor overwrites even a successful status patch.
	if [[ $(cat "$test_dir/operator") == running || $DPC_TEST_CORRUPT == true ]]; then printf '{"status":{"members":{"single":[]}}}' >"$test_dir/live"; fi
}
database_ready() { return 0; }
wait_for() { shift; "$@"; }
` + functions + phase + `
jq -e '.status.members.single[0].id == "retained"' "$test_dir/live" >/dev/null || {
	echo 'operator startup discarded the retained member identity' >&2; exit 1;
}
[[ $(cat "$test_dir/operator") == running ]]
`
	for _, corrupt := range []string{"false", "true"} {
		t.Run("corrupt restored status="+corrupt, func(t *testing.T) {
			directory := t.TempDir()
			command := exec.CommandContext(ctx, "bash", "-c", program)
			command.Env = append(
				os.Environ(),
				"DPC_TEST_DIRECTORY="+directory,
				"DPC_TEST_CORRUPT="+corrupt,
			)
			output, runErr := command.CombinedOutput()
			if corrupt == "false" && runErr != nil {
				t.Fatalf("operator startup fence: %v %s", runErr, output)
			}
			if corrupt == "true" {
				state, readErr := os.ReadFile(directory + "/operator")
				if runErr == nil || readErr != nil || string(state) != "stopped" {
					t.Fatalf(
						"invalid restoration restarted operator: %v %v %s",
						runErr,
						readErr,
						output,
					)
				}
			}
		})
	}
}
