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

# 3. An unreadable entrypoint is distinguished from a clean start.
out=$(bash -c "$(<"$inner")" khoist-probe "$work/missing.sh" 2>&1) && status=0 || status=$?
[[ $status == 97 && $out == 'khoist-probe entry=unreadable' ]] || fail "unreadable entrypoint: $status $out"

# 4. The cluster wrapper reports only fixed probe fields from a fake kubectl.
mkdir "$work/bin"
export TEST_PROBE_CALLS="$work/calls"
cat >"$work/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_PROBE_CALLS"
[[ "$*" == "--kubeconfig "*' --request-timeout=10s -n products '* ]] || exit 90
case "$*" in
*' get pod documents-rs0-0 -o json')
  [[ ${TEST_NOT_FAILED:-false} != true ]] || { printf '%s\n' '{"spec":{"containers":[{"name":"mongod","command":["/opt/percona/ps-entry.sh"]}]},"status":{"containerStatuses":[{"name":"mongod","lastState":{}}]}}'; exit 0; }
  printf '%s\n' '{"spec":{"containers":[{"name":"mongod","command":["/opt/percona/ps-entry.sh"],"args":["--bind_ip_all","--synthetic-password=x y"]}]},"status":{"containerStatuses":[{"name":"mongod","restartCount":6,"lastState":{"terminated":{"exitCode":1,"message":"synthetic-private-detail"}}}]}}'
  ;;
*' delete pod documents-entry-probe '*) ;;
*' debug documents-rs0-0 --copy-to=documents-entry-probe --same-node --container=mongod -- bash -c '*)
  # The original command and arguments follow the probe name as separate words.
  [[ "${*: -4:1}" == khoist-probe && "${*: -3:1}" == /opt/percona/ps-entry.sh ]] || exit 92
  [[ "${*: -1}" == '--synthetic-password=x y' ]] || exit 93
  ;;
*' get pod documents-entry-probe -o json')
  printf '%s\n' '{"status":{"containerStatuses":[{"name":"mongod","restartCount":1,"state":{"waiting":{}},"lastState":{"terminated":{"exitCode":1}}}]}}'
  ;;
*' logs documents-entry-probe -c mongod --tail=200 --previous')
  [[ ${TEST_EMPTY_LOG:-false} != true ]] || exit 0
  printf '%s\n' 'khoist-probe entry-sha256=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' \
    'synthetic-private-detail' 'khoist-probe err status=1 line=42' 'khoist-probe err status=1 line=synthetic' \
    'khoist-probe exit status=1'
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

TEST_NOT_FAILED=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/skipped"
jq -e '.outcome=="NoFailedStart"' "$work/skipped" >/dev/null || fail "probed a container that never failed"

# 5. An empty log proves nothing about the entrypoint: the probe's own first line never ran.
TEST_EMPTY_LOG=true bash "$root/tests/provider/percona-entry-probe.sh" "$work/kubeconfig" >"$work/empty"
jq -e '.outcome=="ProbeOutputMissing" and .entrySha256==null and .otherLines==0' "$work/empty" >/dev/null ||
	fail "empty probe log was classified as $(cat "$work/empty")"
printf 'PASS: startup probe line/status evidence, process replacement, empty log, skip and privacy controls\n'
