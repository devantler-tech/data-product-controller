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

kube delete pod "$probe" --ignore-not-found --wait=true --timeout=30s >/dev/null 2>&1 || true
probe_created=true
if ! kube debug "$pod" --copy-to="$probe" --same-node --container=mongod \
	-- bash -c "$(<"$here/percona-entry-probe-inner.sh")" khoist-probe "${original[@]}" \
	>/dev/null 2>&1; then
	result ProbeNotCreated
	exit 0
fi

state=Pending
for ((attempt = 0; attempt < attempts; attempt++)); do
	sleep "$interval"
	kube get pod "$probe" -o json >"$work/probe.json" 2>/dev/null || continue
	state=$(jq -r '[.status.containerStatuses[]? | select(.name=="mongod")] | first // {} |
      if (.restartCount // 0) > 0 or .state.terminated != null then "Terminated"
      elif .state.running != null then "Running"
      else "Pending" end' "$work/probe.json")
	[[ $state != Terminated ]] || break
done

# Read the first completed start; the copy restarts it after it exits.
logs=(logs "$probe" -c mongod --tail=200)
if jq -e '[.status.containerStatuses[]? | select(.name=="mongod")] | (first.restartCount // 0) > 0' \
	"$work/probe.json" >/dev/null 2>&1; then
	logs+=(--previous)
fi
available=true
kube "${logs[@]}" >"$work/log" 2>/dev/null || available=false
grep -E '^khoist-probe (entry-sha256=[0-9a-f]{64}|entry=unreadable|err status=[0-9]{1,3} line=[0-9]{1,6}|exit status=[0-9]{1,3})$' \
	"$work/log" >"$work/probe-lines" || true
other=$(($(wc -l <"$work/log") - $(wc -l <"$work/probe-lines")))

jq -Rnc --arg state "$state" --argjson available "$available" --argjson other "${other:-0}" \
	--slurpfile pod <(jq -c '[.status.containerStatuses[]? | select(.name=="mongod")] | first // {}' \
		"$work/probe.json" 2>/dev/null || printf '{}') '
  [inputs] as $lines |
  ($pod[0] | (.lastState.terminated // .state.terminated // {}).exitCode) as $containerExit |
  ([$lines[] | select(startswith("khoist-probe exit ")) | capture("status=(?<s>[0-9]+)").s | tonumber] | last) as $entryExit |
  {probe:"entrypoint",
   outcome:(
     if $state=="Running" then "StartedWithinDeadline"
     elif $state!="Terminated" then "ProbeNotStarted"
     elif $available|not then "ProbeLogUnavailable"
     elif any($lines[]; .=="khoist-probe entry=unreadable") then "EntrypointUnreadable"
     elif $entryExit!=null then "EntrypointExited"
     else "EntrypointReplaced" end),
   entrySha256:([$lines[] | select(startswith("khoist-probe entry-sha256=")) | .[26:]] | first),
   errors:([$lines[] | select(startswith("khoist-probe err ")) |
     capture("status=(?<status>[0-9]+) line=(?<line>[0-9]+)") | map_values(tonumber)] | .[-8:]),
   entrypointExit:$entryExit,
   containerExit:(if ($containerExit|type)=="number" then $containerExit else null end),
   otherLines:$other}' <"$work/probe-lines"
