#!/usr/bin/env bash
# Bounded, read-only startup evidence from this run's disposable kubeconfig.
# Never print raw messages, attributes, Secret data or database records.
set -euo pipefail
[[ $# == 1 && -r $1 ]] || exit 2
kubeconfig=$1
umask 077
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
kube() { kubectl --kubeconfig "$kubeconfig" --request-timeout=3s -n products "$@"; }
if ! kube get pods -l app=document-database -o json >"$work/pods.json" 2>/dev/null; then
	echo 'Document startup diagnostic inventory unavailable' >&2
	exit 1
fi
jq -e '(.items|type)=="array"' "$work/pods.json" >/dev/null
jq -c '
  def token: if type=="string" and test("^[A-Za-z0-9_-]{1,80}$") then . else null end;
  def terminated: {reason:(.reason|token),exitCode:.exitCode,signal:.signal};
  .items[] as $p |
  (($p.spec.initContainers//[])+($p.spec.containers//[]))[] as $c |
  ([($p.status.initContainerStatuses//[])+($p.status.containerStatuses//[])][0] |
    map(select(.name==$c.name)) | first // {}) as $s |
  {pod:$p.metadata.name,container:$c.name,restartCount:($s.restartCount//0),
   current:($s.state.terminated|terminated),previous:($s.lastState.terminated|terminated)}
' "$work/pods.json" >"$work/status.jsonl"

# read_log emits only identifiers, counts and fixed error classes, never log prose.
# The operator entrypoint can exit before mongod starts emitting JSON. Classify
# those plaintext errors without publishing their arguments, paths or values.
read_log() {
	local pod=$1 container=$2 stream=$3
	local -a args=(logs "$pod" -c "$container")
	if [[ $stream == previous ]]; then args+=(--previous --tail=80); else args+=(--tail=40); fi
	if ! kube "${args[@]}" >"$work/log" 2>/dev/null; then
		jq -nc --arg pod "$pod" --arg container "$container" --arg stream "$stream" \
			'{pod:$pod,container:$container,stream:$stream,available:false}'
		return
	fi
	jq -Rnc --arg pod "$pod" --arg container "$container" --arg stream "$stream" '
    def token: if type=="string" and test("^[A-Za-z0-9_-]{1,80}$") then . else null end;
    def number: if type=="number" then . else null end;
    def plaintext_reason:
      if .=="Fatal glibc error: CPU does not support x86-64-v3" then "UnsupportedCPU"
      elif startswith("Error parsing command line:") then "CommandLineRejected"
      elif startswith("BadValue:") then "InvalidStartupOption"
      elif startswith("Unrecognized option:") then "UnknownStartupOption"
      elif test("\\.sh: line [0-9]+: [A-Za-z_][A-Za-z0-9_]*: unbound variable$") then "EntrypointUnboundVariable"
      elif .=="error: mongod does not appear to have stayed running -- perhaps it had an error?" then "BootstrapProcessExited"
      elif .=="error: mongod does not appear to have accepted connections quickly enough -- perhaps it had an error?" then "BootstrapConnectionTimeout"
      elif contains(": Permission denied") then "PermissionDenied"
      elif contains(": Read-only file system") then "ReadOnlyFilesystem"
      else null end;
    [inputs | (try fromjson catch null) as $document |
      {document:$document,
       reason:(if ($document|type)=="object" then null else plaintext_reason end),
       nonempty:(length>0)}] as $lines |
    {pod:$pod,container:$container,stream:$stream,available:true,
     structuredLines:([$lines[] | select(.document|type=="object")]|length),
     unclassifiedLines:([$lines[] | select((.document|type)!="object" and .reason==null and .nonempty)]|length),
     records:[
      $lines[] |
      if .reason!=null then
        {severity:"E",component:"STARTUP",reason:.reason}
      else
        .document |
        select(type=="object" and (.s=="E" or .s=="F")) |
        (.attr|if type=="object" then . else {} end) as $attr |
        ($attr.error|if type=="object" then . else {} end) as $error |
        {severity:.s,component:(.c|token),id:(.id|number),
         errorCode:(($error.code//$attr.code)|number),
         errorName:(($error.codeName//$attr.codeName)|token)}
      end
    ]}' <"$work/log"
}
while IFS= read -r row; do
	pod=$(jq -er .pod <<<"$row")
	container=$(jq -er .container <<<"$row")
	[[ $pod =~ ^[a-z0-9][a-z0-9.-]*$ && $container =~ ^[a-z0-9][a-z0-9-]*$ ]] || exit 2
	printf '%s\n' "$row"
	if jq -e '.restartCount|type=="number" and .>0' <<<"$row" >/dev/null; then
		read_log "$pod" "$container" previous
	fi
	read_log "$pod" "$container" current
done <"$work/status.jsonl"
