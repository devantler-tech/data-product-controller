#!/usr/bin/env bash
# Absolute and phase deadlines are shared by every real-provider assertion.
: "${work_deadline:?outer work deadline required}"
: "${test_dir:?private assertion output directory required}"
: "${started_at:?run start time required}"
phase() {
	local name=$1 seconds=$2
	if [[ ! $seconds =~ ^[1-9][0-9]*$ ]] || ((SECONDS >= work_deadline)); then
		echo 'invalid or expired provider phase budget' >&2
		return 1
	fi
	phase_deadline=$((SECONDS + seconds))
	((phase_deadline <= work_deadline)) || phase_deadline=$work_deadline
	printf 'PHASE: %s (remaining %ss)\n' "$name" "$((phase_deadline - SECONDS))"
}

remaining() {
	local left=$((phase_deadline - SECONDS))
	((left > 0)) || {
		echo 'provider phase deadline exceeded' >&2
		return 1
	}
	printf '%s\n' "$left"
}

bounded() {
	local left
	left=$(remaining) || return 1
	if [[ $# == 0 || $(type -t "$1") != file ]]; then
		echo 'bounded requires an external executable' >&2
		return 1
	fi
	timeout --signal=TERM --kill-after=5s "${left}s" "$@"
}

wait_for() {
	local description=$1
	shift
	until "$@" >"$test_dir/wait.log" 2>&1; do
		remaining >/dev/null || {
			echo "FAIL: $description" >&2
			cat "$test_dir/wait.log" >&2
			return 1
		}
		sleep 2
	done
	remaining >/dev/null || return 1
	printf 'PASS: %s (elapsed %ss)\n' "$description" "$((SECONDS - started_at))"
}
