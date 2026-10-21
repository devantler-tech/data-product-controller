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
  printf '%s\n' '{"s":"E","c":"STORAGE","id":20576,"attr":{"error":{"code":30,"codeName":"InvalidPath","errmsg":"synthetic-private-detail: Permission denied"}}}'
  printf '%s\n' '{"s":"I","c":"STORAGE","id":20577,"msg":"synthetic-private-detail: Read-only file system"}'
  printf '%s\n' '{"s":"W","c":"STORAGE","id":20578,"msg":"synthetic-private-detail: Read-only file system"}'
  printf '%s\n' 'synthetic-private-detail' 'Fatal glibc error: CPU does not support x86-64-v3'
  printf '%s\n' 'Error parsing command line: synthetic-private-detail' \
    'BadValue: synthetic-password' 'Unrecognized option: synthetic-record' \
    '/data/db/ps-entry.sh: line 401: SYNTHETIC_SECRET: unbound variable' \
    'error: mongod does not appear to have stayed running -- perhaps it had an error?' \
    'error: mongod does not appear to have accepted connections quickly enough -- perhaps it had an error?' \
    'synthetic-private-detail: Permission denied' \
    'synthetic-password: Read-only file system'
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
for reason in CommandLineRejected InvalidStartupOption UnknownStartupOption EntrypointUnboundVariable BootstrapProcessExited BootstrapConnectionTimeout PermissionDenied ReadOnlyFilesystem; do
	grep -F "\"reason\":\"$reason\"" "$work/output" >/dev/null
done
if ! jq -se '
  [.[]|select(.container=="mongod" and .stream=="previous")]|length==1 and
  (.[0]|.structuredLines==6 and (.records|length)==13 and
    ([.records[]|select(.id==20576)]==[{severity:"E",component:"STORAGE",id:20576,errorCode:30,errorName:"InvalidPath"}]) and
    ([.records[]|select(.reason=="ReadOnlyFilesystem")]|length)==1)
' "$work/output" >/dev/null; then
	echo 'FAIL: structured log identifiers and severity must survive plaintext-like message content' >&2
	exit 1
fi
grep -F '"unclassifiedLines":1' "$work/output" >/dev/null
grep -F 'mongo-init' "$TEST_DIAGNOSTICS_CALLS" >/dev/null
grep -F -- '--previous --tail=80' "$TEST_DIAGNOSTICS_CALLS" >/dev/null
if grep -E 'synthetic-(private|password|record)|SYNTHETIC_SECRET|ps-entry.sh' "$work/output"; then
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
