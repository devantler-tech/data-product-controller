# shellcheck shell=bash
# Runs inside a label-free copy of the database pod as `bash -c "<this file>" probe ENTRY ARGS...`.
# It sources the operator entrypoint under numeric traps so a start that fails before the
# database logs anything still names the failing line and status. It prints only fixed,
# numeric probe lines itself; the cluster wrapper keeps only those lines from the log.
entry=${1-}
shift || true
if [[ -z $entry || ! -r $entry ]]; then
	printf 'khoist-probe entry=unreadable\n'
	exit 97
fi
if command -v sha256sum >/dev/null 2>&1; then
	digest=$(sha256sum "$entry" 2>/dev/null) && printf 'khoist-probe entry-sha256=%s\n' "${digest%% *}"
fi
# Compare version discovery in this copy's image, node and environment before
# sourcing the unchanged entrypoint. All commands are read-only, their output is
# discarded, and each producer is bounded. Neither failure nor success is a
# database readiness result. The subshell cannot alter the entrypoint's traps.
if command -v mongod >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1 &&
	command -v head >/dev/null 2>&1 && command -v awk >/dev/null 2>&1; then
	(
		trap - ERR EXIT
		set +e
		set -o pipefail
		# Keep only a bounded private stream, then publish identifiers and fixed
		# classes. Never echo messages, attributes, paths or environment values.
		probe_output=''
		if command -v jq >/dev/null 2>&1; then
			umask 077
			probe_output=$(mktemp) || probe_output=''
		fi
		if [[ -n $probe_output ]]; then
			(
				ulimit -f 64
				exec timeout --signal=TERM --kill-after=1s 5s mongod --version
			) >"$probe_output" 2>&1
		else
			timeout --signal=TERM --kill-after=1s 5s mongod --version >/dev/null 2>&1
		fi
		probe_version_status=$?
		printf 'khoist-probe version mode=direct statuses=%d\n' "$probe_version_status"
		if [[ -n $probe_output ]]; then
			# shellcheck disable=SC2016 # jq variables are not shell interpolation.
			probe_summary=$(timeout --signal=TERM --kill-after=1s 5s jq -Rnc '
        def number: if type=="number" and .>=0 and .<=2147483647 and floor==. then . else null end;
        def reason:
          if .=="Fatal glibc error: CPU does not support x86-64-v3" then "UnsupportedCPU"
          elif startswith("Error parsing command line:") then "CommandLineRejected"
          elif startswith("BadValue:") then "InvalidStartupOption"
          elif startswith("Unrecognized option:") then "UnknownStartupOption"
          elif contains(": Permission denied") then "PermissionDenied"
          elif contains(": Read-only file system") then "ReadOnlyFilesystem"
          elif contains(": No space left on device") then "NoSpaceLeft"
          else null end;
        [inputs | (try fromjson catch null) as $document |
          {document:$document,nonempty:(length>0),
           reason:(if ($document|type)=="object" then null else reason end)}] as $lines |
        {lines:($lines|length),structured:([$lines[]|select(.document|type=="object")]|length),
         unclassified:([$lines[]|select(.nonempty and .reason==null and (.document|type)!="object")]|length),
         classes:([$lines[].reason|select(.!=null)]|unique),
         errors:([$lines[].document|select(type=="object" and (.s=="E" or .s=="F"))|
           {severity:.s,id:(.id|number),code:((try (.attr.error.code // .attr.code) catch null)|number)}]|.[0:8])}
      ' <"$probe_output" 2>/dev/null) && printf 'khoist-probe version-output %s\n' "$probe_summary" ||
				printf 'khoist-probe version-output=unavailable\n'
			rm -f "$probe_output"
		else
			printf 'khoist-probe version-output=unavailable\n'
		fi
		timeout --signal=TERM --kill-after=1s 5s mongod --version 2>/dev/null |
			head -1 | awk '{print $3}' | awk -F'.' '{print $1"."$2}' >/dev/null
		probe_version_stages=("${PIPESTATUS[@]}")
		(
			IFS=,
			printf 'khoist-probe version mode=early statuses=%s\n' "${probe_version_stages[*]}"
		)
		timeout --signal=TERM --kill-after=1s 5s mongod --version 2>/dev/null |
			awk 'NR==1 {split($3, version, "."); print version[1]"."version[2]}' >/dev/null
		probe_version_stages=("${PIPESTATUS[@]}")
		(
			IFS=,
			printf 'khoist-probe version mode=drain statuses=%s\n' "${probe_version_stages[*]}"
		)
	)
else
	printf 'khoist-probe version=unavailable\n'
fi
# Write probe lines to a saved copy of the container log. With errtrace the ERR trap also fires
# inside command substitutions and pipelines, whose stdout is the entrypoint's own data.
exec 47>&1
# The trap passes all values as arguments before any handler command overwrites PIPESTATUS.
# Keep the original error marker and emit only numeric stages for multi-command pipelines.
_khoist_probe_error() {
	printf 'khoist-probe err status=%d line=%d\n' "$1" "$2" >&47
	if (($# > 3)); then
		local IFS=,
		printf 'khoist-probe pipeline line=%d statuses=%s\n' "$2" "${*:3}" >&47
	fi
}
trap '_khoist_probe_error "$?" "$LINENO" "${PIPESTATUS[@]}"' ERR
trap 'printf "khoist-probe exit status=%d\n" "$?" >&47' EXIT
set -o errtrace
# shellcheck source=/dev/null
. "$entry" "$@"
