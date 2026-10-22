#!/usr/bin/env bash
# Hermetic checks for the startup exit probe; no cluster, image or credentials are used.
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
inner="$root/tests/provider/percona-entry-probe-inner.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# 1. The in-container probe names the failing line and status without echoing values.
cat >"$work/entry-fails.sh" <<'ENTRY'
set -Eeuo pipefail
secret_value="synthetic-private-detail"
: "$secret_value"
grep -q never-present /dev/null
exec "$@"
ENTRY
out=$(bash -c "$(<"$inner")" khoist-probe "$work/entry-fails.sh" --synthetic-argument 2>&1) && status=0 || status=$?
[[ $status == 1 ]] || fail "failing entrypoint status was $status, want 1"
grep -qx 'khoist-probe err status=1 line=4' <<<"$out" || fail "missing failing line: $out"
grep -qx 'khoist-probe exit status=1' <<<"$out" || fail "missing exit line: $out"
grep -qE '^khoist-probe entry-sha256=[0-9a-f]{64}$' <<<"$out" || fail "missing entrypoint digest: $out"
! grep -qE 'synthetic|grep' <<<"$out" || fail "probe echoed entrypoint values or arguments: $out"

# 2. A handled failure is not reported, and process replacement leaves no exit line.
cat >"$work/entry-execs.sh" <<'ENTRY'
set -Eeuo pipefail
if grep -q never-present /dev/null; then :; fi
exec bash -c 'exit 7' "$@"
ENTRY
out=$(bash -c "$(<"$inner")" khoist-probe "$work/entry-execs.sh" 2>&1) && status=0 || status=$?
[[ $status == 7 ]] || fail "replaced process status was $status, want 7"
! grep -q '^khoist-probe err ' <<<"$out" || fail "handled conditional reported as a failure: $out"
! grep -q '^khoist-probe exit ' <<<"$out" || fail "process replacement reported an entrypoint exit: $out"

# 2b. A failure inside a command substitution is reported without changing the captured value.
cat >"$work/entry-substitutes.sh" <<'ENTRY'
value=$(false; printf kept)
[[ $value == kept ]] || exit 9
ENTRY
out=$(bash -c "$(<"$inner")" khoist-probe "$work/entry-substitutes.sh" 2>&1) && status=0 || status=$?
[[ $status == 0 ]] || fail "probe output changed a substituted value (status $status): $out"
grep -qx 'khoist-probe err status=1 line=1' <<<"$out" || fail "missing substitution failure line: $out"

# 2c. Capture the real failing pipeline stages before the ERR handler changes PIPESTATUS.
cat >"$work/entry-pipeline.sh" <<'ENTRY'
set -Eeuo pipefail
value=$((printf 'synthetic-private-detail\n'; exit 29) | cat)
ENTRY
out=$(bash -c "$(<"$inner")" khoist-probe "$work/entry-pipeline.sh" 2>&1) && status=0 || status=$?
[[ $status == 29 ]] || fail "pipeline failure status changed: $status"
grep -qx 'khoist-probe pipeline line=2 statuses=29,0' <<<"$out" || fail 'missing original pipeline segment statuses'
! grep -q synthetic <<<"$out" || fail 'pipeline diagnostic leaked command output'

# 3. An unreadable entrypoint is distinguished from a clean start.
out=$(bash -c "$(<"$inner")" khoist-probe "$work/missing.sh" 2>&1) && status=0 || status=$?
[[ $status == 97 && $out == 'khoist-probe entry=unreadable' ]] || fail "unreadable entrypoint: $status $out"

# 4. The cluster wrapper reports only fixed probe fields from a fake kubectl.
mkdir "$work/bin"
export TEST_PROBE_CALLS="$work/calls" TEST_PROBE_POD="$work/probe-pod.json"
cat >"$work/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_PROBE_CALLS"
[[ "$*" == "--kubeconfig "*' --request-timeout=10s -n products '* ]] || exit 90
case "$*" in
*' get pod documents-rs0-0 -o json')
  [[ ${TEST_NOT_FAILED:-false} != true ]] || { printf '%s\n' '{"spec":{"containers":[{"name":"mongod","command":["/opt/percona/ps-entry.sh"]}]},"status":{"containerStatuses":[{"name":"mongod","lastState":{}}]}}'; exit 0; }
  printf '%s\n' '{"metadata":{"name":"documents-rs0-0","labels":{"app":"documents"},"ownerReferences":[{"kind":"StatefulSet","name":"documents-rs0"}]},"spec":{"nodeName":"node-a","hostname":"documents-rs0-0","volumes":[{"name":"mongod-data","persistentVolumeClaim":{"claimName":"mongod-data-documents-rs0-0"}},{"name":"bin","emptyDir":{}}],"containers":[{"name":"mongod","image":"db:pinned","command":["/opt/percona/ps-entry.sh"],"args":["--bind_ip_all","--synthetic-password=x y"],"livenessProbe":{"exec":{"command":["true"]}},"readinessProbe":{"tcpSocket":{"port":27017}},"volumeMounts":[{"name":"mongod-data","mountPath":"/data/db"}]}]},"status":{"containerStatuses":[{"name":"mongod","restartCount":6,"lastState":{"terminated":{"exitCode":1,"message":"synthetic-private-detail"}}}]}}'
  ;;
*' delete pod documents-entry-probe '*) ;;
*' create -f '*) cp "${@: -1}" "$TEST_PROBE_POD" ;;
*' get pod documents-entry-probe -o json')
  [[ ${TEST_STILL_RUNNING:-false} != true ]] || { printf '%s\n' '{"status":{"containerStatuses":[{"name":"mongod","restartCount":0,"state":{"running":{}}}]}}'; exit 0; }
  [[ ${TEST_NO_EXIT:-false} != true ]] || { printf '%s\n' '{"status":{"containerStatuses":[{"name":"mongod","restartCount":0,"state":{"terminated":{"exitCode":137,"reason":"OOMKilled","message":"synthetic-private-detail"}}}]}}'; exit 0; }
  printf '%s\n' '{"status":{"containerStatuses":[{"name":"mongod","restartCount":0,"state":{"terminated":{"exitCode":1,"reason":"Error"}}}]}}'
  ;;
*' logs documents-entry-probe -c mongod --tail=200')
  [[ ${TEST_EMPTY_LOG:-false} != true ]] || exit 0
  printf '%s\n' 'khoist-probe entry-sha256=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' \
    'synthetic-private-detail' 'khoist-probe err status=1 line=42' 'khoist-probe err status=1 line=synthetic'
  [[ ${TEST_NO_EXIT:-false} == true ]] || printf '%s\n' 'khoist-probe exit status=1'
  if [[ ${TEST_PIPELINE:-false} == true ]]; then
    printf '%s\n' 'khoist-probe pipeline line=34 statuses=29,0,0,0' \
      'khoist-probe pipeline line=34 statuses=synthetic-private-detail'
  fi
  ;;
*) exit 91 ;;
esac
KUBECTL
chmod +x "$work/bin/kubectl"
export PATH="$work/bin:$PATH" PERCONA_PROBE_INTERVAL=0
printf 'disposable fixture\n' >"$work/kubeconfig"
bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/output"
jq -e '.outcome=="EntrypointExited" and .entrypointExit==1 and .containerExit==1 and
  .errors==[{status:1,line:42}] and .otherLines==2 and (.entrySha256|length)==64' "$work/output" >/dev/null ||
	fail "unexpected probe result: $(cat "$work/output")"
! grep -qE 'synthetic|ps-entry|bind_ip' "$work/output" || fail "probe result leaked arguments or log prose"
grep -q ' delete pod documents-entry-probe ' "$TEST_PROBE_CALLS" || fail 'probe copy was not removed'
# The copy is label- and owner-free on the same node, shares no claimed volume, and runs the
# original command and arguments as separate words after the probe name.
jq -e --arg inner "$(<"$inner")" '.metadata == {name:"documents-entry-probe"} and
  .spec.nodeName == "node-a" and .spec.hostname == null and .spec.restartPolicy == "Never" and
  ([.spec.volumes[] | select(has("persistentVolumeClaim"))] | length) == 0 and
  (.spec.volumes | map(.name) | sort) == ["bin", "mongod-data"] and
  (.spec.containers[0] | .livenessProbe == null and .readinessProbe == null and .args == null and
    .volumeMounts == [{name:"mongod-data", mountPath:"/data/db"}] and
    .command == ["bash", "-c", $inner, "khoist-probe", "/opt/percona/ps-entry.sh",
      "--bind_ip_all", "--synthetic-password=x y"])' "$TEST_PROBE_POD" >/dev/null ||
	fail "unexpected probe pod: $(jq -c 'del(.spec.containers[0].command[2])' "$TEST_PROBE_POD")"

TEST_NOT_FAILED=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/skipped"
jq -e '.outcome=="NoFailedStart"' "$work/skipped" >/dev/null || fail "probed a container that never failed"

# 5. An empty log proves nothing about the entrypoint: the probe's own first line never ran.
TEST_EMPTY_LOG=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/empty"
jq -e '.outcome=="ProbeOutputMissing" and .entrySha256==null and .otherLines==0' "$work/empty" >/dev/null ||
	fail "empty probe log was classified as $(cat "$work/empty")"

# 6. A copy still running at the deadline proves only that its shell is alive, not that the database started.
TEST_STILL_RUNNING=true PERCONA_PROBE_ATTEMPTS=2 bash "$root/tests/provider/percona-entry-probe.sh" \
	"$work/kubeconfig" >"$work/running"
jq -e '.outcome=="StillRunningAtDeadline"' "$work/running" >/dev/null ||
	fail "running copy was classified as $(cat "$work/running")"

# 7. No EXIT marker is not proof of replacement: a killed shell leaves the same log.
TEST_NO_EXIT=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/no-exit"
jq -e '.outcome=="Indeterminate" and .entrypointExit==null and .containerExit==137 and
  .containerReason=="OOMKilled" and .errors==[{status:1,line:42}]' "$work/no-exit" >/dev/null ||
	fail "missing exit marker was classified as $(cat "$work/no-exit")"
! grep -q synthetic "$work/no-exit" || fail "probe result leaked the termination message"

TEST_PIPELINE=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/pipeline"
jq -e '.pipelines==[{line:34,statuses:[29,0,0,0]}] and .otherLines==3' "$work/pipeline" >/dev/null ||
  fail 'pipeline segment evidence was lost or malformed log data was accepted'
! grep -q synthetic "$work/pipeline" || fail 'pipeline result leaked unclassified output'
printf 'PASS: startup probe line/status evidence, substitution safety, process replacement, isolated one-shot copy, empty log, running deadline, missing exit marker, skip and privacy controls\n'
