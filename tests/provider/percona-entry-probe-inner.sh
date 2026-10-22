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
_khoist_probe_error() {
	local status=$1 line=$2 stages=$(($# - 2)) stage retained=0 pipeline=
	shift 2
	for stage in "$@"; do
		((retained < 16)) || break
		pipeline+="${pipeline:+,}$stage"
		retained=$((retained + 1))
	done
	printf 'khoist-probe err status=%d line=%d stages=%d pipeline=%s\n' \
		"$status" "$line" "$stages" "$pipeline" >&47
}
# Expand the status and the complete PIPESTATUS vector before entering the function:
# even its first local assignment would otherwise replace the evidence with [0].
trap '_khoist_probe_error "$?" "$LINENO" "${PIPESTATUS[@]}"' ERR
trap 'printf "khoist-probe exit status=%d\n" "$?" >&47' EXIT
set -o errtrace
# shellcheck source=/dev/null
. "$entry" "$@"
