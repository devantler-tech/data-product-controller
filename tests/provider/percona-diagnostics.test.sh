#!/usr/bin/env bash
# Hermetic failure-evidence checks; no cluster, credentials or database is used.
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
export TEST_DIAGNOSTICS_CALLS="$work/calls"
cat >"$work/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_DIAGNOSTICS_CALLS"
[[ "$*" == "--kubeconfig "*' --request-timeout=3s -n products '* ]] || exit 90
case "$*" in
*' get pods -l app=document-database -o json')
  [[ ${TEST_LIST_FAIL:-false} != true ]] || exit 1
  printf '%s\n' '{"items":[{"metadata":{"name":"documents-rs0-0"},"spec":{"containers":[{"name":"mongod"}],"initContainers":[{"name":"mongo-init"}]},"status":{"containerStatuses":[{"name":"mongod","restartCount":6,"lastState":{"terminated":{"reason":"Error","exitCode":14,"signal":0,"message":"synthetic-private-detail"}}}],"initContainerStatuses":[{"name":"mongo-init","restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}}]}'
  ;;
*' logs documents-rs0-0 -c mongod --previous --tail=80')
  [[ ${TEST_PREVIOUS_FAIL:-false} != true ]] || exit 1
  printf '%s\n' '{"s":"F","c":"CONTROL","id":20574,"msg":"synthetic-private-detail","attr":{"error":{"code":30,"codeName":"InvalidPath","errmsg":"synthetic-private-detail"},"password":"synthetic-password","document":"synthetic-record"}}'
	printf '%s\n' '{"s":"E","c":"STORAGE","id":20575,"msg":"synthetic-private-detail","attr":{"error":"synthetic-private-detail"}}'
	printf '%s\n' '{"s":"E","c":{"private":"synthetic-private-detail"},"id":"synthetic-private-detail","attr":["synthetic-private-detail"]}'
  printf '%s\n' 'synthetic-private-detail' 'Fatal glibc error: CPU does not support x86-64-v3'
  ;;
*' logs documents-rs0-0 -c mongod --tail=40'|*' logs documents-rs0-0 -c mongo-init --tail=40')
  printf '%s\n' '{"s":"I","c":"COMMAND","msg":"synthetic-private-detail","attr":{"document":"synthetic-record"}}'
  ;;
*) exit 91 ;;
esac
KUBECTL
chmod +x "$work/bin/kubectl"
export PATH="$work/bin:$PATH"
printf 'disposable fixture\n' >"$work/kubeconfig"
bash "$root/tests/provider/percona-diagnostics.sh" "$work/kubeconfig" >"$work/output"
grep -F '"exitCode":14' "$work/output" >/dev/null
grep -F '"errorName":"InvalidPath"' "$work/output" >/dev/null
grep -F '"reason":"UnsupportedCPU"' "$work/output" >/dev/null
grep -F 'mongo-init' "$TEST_DIAGNOSTICS_CALLS" >/dev/null
grep -F -- '--previous --tail=80' "$TEST_DIAGNOSTICS_CALLS" >/dev/null
if grep -E 'synthetic-(private|password|record)' "$work/output"; then
	echo 'FAIL: diagnostics leaked raw messages, credentials or records' >&2
	exit 1
fi
TEST_PREVIOUS_FAIL=true bash "$root/tests/provider/percona-diagnostics.sh" "$work/kubeconfig" >"$work/missing"
grep -F '"available":false' "$work/missing" >/dev/null
grep -F '"container":"mongo-init"' "$work/missing" >/dev/null
if TEST_LIST_FAIL=true bash "$root/tests/provider/percona-diagnostics.sh" "$work/kubeconfig" >"$work/unavailable"; then
	echo 'FAIL: unreadable pod inventory reported complete diagnostics' >&2
	exit 1
fi
printf 'PASS: bounded startup evidence, every container, previous-log failure and privacy controls\n'
