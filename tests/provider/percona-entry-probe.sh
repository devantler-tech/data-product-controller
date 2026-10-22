#!/usr/bin/env bash
# Startup-only exit probe for a database container that already failed to start.
# Starts a label-free copy of the failed pod on the same node, whose database
# container sources the declared entrypoint under numeric traps, and reports one
# JSON line: the entrypoint digest, failing line numbers and exit statuses.
# Never prints log prose, arguments, environment values or Secret data.
set -euo pipefail
[[ $# == 1 && -r $1 ]] || exit 2
kubeconfig=$1
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
pod=${PERCONA_PROBE_POD:-documents-rs0-0}
probe=documents-entry-probe
attempts=${PERCONA_PROBE_ATTEMPTS:-20}
interval=${PERCONA_PROBE_INTERVAL:-3}
umask 077
work=$(mktemp -d)
kube() { kubectl --kubeconfig "$kubeconfig" --request-timeout=10s -n products "$@"; }
probe_created=false
cleanup() {
	[[ $probe_created != true ]] || kube delete pod "$probe" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
result() { jq -nc --arg outcome "$1" '{probe:"entrypoint",outcome:$outcome}'; }

if ! kube get pod "$pod" -o json >"$work/pod.json" 2>/dev/null; then
	result PodUnreadable
	exit 0
fi
# Probe only a database container whose last start already ended non-zero.
if ! jq -e '[.status.containerStatuses[]? | select(.name=="mongod") |
    .lastState.terminated.exitCode // 0] | (first // 0) != 0' "$work/pod.json" >/dev/null; then
	result NoFailedStart
	exit 0
fi
original=()
while IFS= read -r -d '' word; do original+=("$word"); done < <(
	jq -j '.spec.containers[] | select(.name=="mongod") |
    ((.command // []) + (.args // []))[] | . + "\u0000"' "$work/pod.json"
)
if [[ ${#original[@]} == 0 ]]; then
	result EntrypointUndeclared
	exit 0
fi

# The copy keeps the pod's node, image and mounts, but carries no labels or owner, swaps every
# claimed volume for an empty one so it never shares the live database files, and drops the
# health checks and restarts so the copy starts the database container exactly once.
jq --arg probe "$probe" --arg inner "$(<"$here/percona-entry-probe-inner.sh")" '
  {apiVersion:"v1", kind:"Pod", metadata:{name:$probe},
   spec:(.spec | del(.ephemeralContainers, .hostname, .subdomain) | .restartPolicy = "Never" |
     .volumes |= map(if has("persistentVolumeClaim") then {name, emptyDir:{}} else . end) |
     .containers |= map(if .name == "mongod" then
       del(.livenessProbe, .readinessProbe, .startupProbe) |
       .command = (["bash", "-c", $inner, "khoist-probe"] + (.command // []) + (.args // [])) |
       del(.args)
     else . end))}' "$work/pod.json" >"$work/probe-pod.json"
kube delete pod "$probe" --ignore-not-found --wait=true --timeout=30s >/dev/null 2>&1 || true
probe_created=true
if ! kube create -f "$work/probe-pod.json" >/dev/null 2>&1; then
	result ProbeNotCreated
	exit 0
fi

state=Pending
for ((attempt = 0; attempt < attempts; attempt++)); do
	sleep "$interval"
	kube get pod "$probe" -o json >"$work/probe.json" 2>/dev/null || continue
	state=$(jq -r '[.status.containerStatuses[]? | select(.name=="mongod")] | first // {} |
      if .state.terminated != null then "Terminated"
      elif .state.running != null then "Running"
      else "Pending" end' "$work/probe.json")
	[[ $state != Terminated ]] || break
done

# The copy never restarts, so its log is exactly the one diagnostic start.
available=true
kube logs "$probe" -c mongod --tail=200 >"$work/log" 2>/dev/null || available=false
status_pattern='([0-9]{1,2}|1[0-9]{2}|2[0-4][0-9]|25[0-5])'
grep -E "^khoist-probe (entry-sha256=[0-9a-f]{64}|entry=unreadable|err status=$status_pattern line=[0-9]{1,6}( stages=[0-9]{1,3} pipeline=$status_pattern(,$status_pattern){0,15})?|exit status=$status_pattern)$" \
	"$work/log" >"$work/probe-lines" || true
other=$(($(wc -l <"$work/log") - $(wc -l <"$work/probe-lines")))

# Each named outcome needs positive evidence. Anything else, including a shell killed before its
# EXIT trap or an entrypoint that replaced the shell, is Indeterminate with the container's own
# termination reason and exit code beside it.
jq -Rnc --arg state "$state" --argjson available "$available" --argjson other "${other:-0}" \
	--slurpfile pod <(jq -c '[.status.containerStatuses[]? | select(.name=="mongod")] | first // {}' \
		"$work/probe.json" 2>/dev/null || printf '{}') '
  [inputs] as $lines |
  ($pod[0] | .state.terminated // {}) as $terminated |
  ([$lines[] | select(startswith("khoist-probe exit ")) | capture("status=(?<s>[0-9]+)").s | tonumber] | last) as $entryExit |
  {probe:"entrypoint",
   outcome:(
     if $state=="Running" then "StillRunningAtDeadline"
     elif $state!="Terminated" then "ProbeNotStarted"
     elif $available|not then "ProbeLogUnavailable"
     elif ($lines|length)==0 then "ProbeOutputMissing"
     elif any($lines[]; .=="khoist-probe entry=unreadable") then "EntrypointUnreadable"
     elif $entryExit!=null then "EntrypointExited"
     else "Indeterminate" end),
   entrySha256:([$lines[] | select(startswith("khoist-probe entry-sha256=")) | .[26:]] | first),
   errors:([$lines[] | select(startswith("khoist-probe err ")) |
     capture("status=(?<status>[0-9]+) line=(?<line>[0-9]+)(?: stages=(?<stages>[0-9]+) pipeline=(?<pipeline>[0-9,]+))?") |
     (.pipeline // "" | split(",") | map(tonumber)) as $pipeline |
     {status:(.status|tonumber),line:(.line|tonumber)} +
       (if .stages==null then {} else
         {pipelineStages:(.stages|tonumber),pipelineStatuses:$pipeline,
          pipelineComplete:((.stages|tonumber)==($pipeline|length))} end)] | .[-8:]),
   entrypointExit:$entryExit,
   containerExit:(if ($terminated.exitCode|type)=="number" then $terminated.exitCode else null end),
   containerReason:(if ($terminated.reason|type)=="string" and ($terminated.reason|test("^[A-Za-z]{1,32}$"))
     then $terminated.reason else null end),
   otherLines:$other}' <"$work/probe-lines"
