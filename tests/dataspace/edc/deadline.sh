#!/usr/bin/env bash
# Shared absolute allowance for subprocesses and final receipt publication.
: "${deadline:?}"

# Give every child only the remaining shared allowance, including final receipt publication.
bounded() {
	local remaining=$((deadline - SECONDS))
	((remaining > 0)) || return 124
	timeout --signal=TERM --kill-after=5 "${remaining}s" "$@"
}
