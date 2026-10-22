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
