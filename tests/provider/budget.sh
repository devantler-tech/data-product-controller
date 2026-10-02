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
# Resolve the exact current rollout; a Service can still select a retiring replica.
registry_target_ready() {
	kube get deployment dpc -o json >"$test_dir/registry-deployment.json" || return 1
	kube get replicasets -l app.kubernetes.io/instance=dpc -o json >"$test_dir/registry-replicasets.json" || return 1
	kube get pods -l app.kubernetes.io/instance=dpc -o json >"$test_dir/registry-pods.json" || return 1
	jq -n --slurpfile deployment "$test_dir/registry-deployment.json" \
		--slurpfile replicasets "$test_dir/registry-replicasets.json" \
		--slurpfile pods "$test_dir/registry-pods.json" \
		-f "$repo_root/tests/provider/registry-target.jq" >"$test_dir/registry-target.json" || return 1
	registry_pod=$(jq -er '.pod' "$test_dir/registry-target.json") || return 1
	registry_target_port=$(jq -er '.port' "$test_dir/registry-target.json") || return 1
}
# kubectl reports the named pod port, not the Service's frontend port 80.
# Accept only an allocated loopback listener owned by the live forwarding process.
registry_forward_ready() {
	local line
	kill -0 "${registry_forward_pid:?owned registry forwarding process required}" 2>/dev/null || return 1
	while IFS= read -r line; do
		if [[ $line =~ ^Forwarding\ from\ 127\.0\.0\.1:([0-9]+)\ -\>\ ([0-9]+)$ ]]; then
			registry_port=${BASH_REMATCH[1]}
			[[ ${BASH_REMATCH[2]} == "${registry_target_port:?observed registry target port required}" ]] || return 1
			[[ $registry_port -ge 1 && $registry_port -le 65535 ]] || return 1
			return 0
		fi
	done <"$test_dir/forward.log"
	return 1
}
