#!/usr/bin/env bash
# Absolute and phase deadlines are shared by every real-provider assertion.
: "${work_deadline:?outer work deadline required}"
: "${test_dir:?private assertion output directory required}"
: "${started_at:?run start time required}"
# Start a phase within the absolute assertion deadline, preserving the cleanup reserve.
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

# Return the positive phase time remaining, or fail before another assertion starts.
remaining() {
	local left=$((phase_deadline - SECONDS))
	((left > 0)) || {
		echo 'provider phase deadline exceeded' >&2
		return 1
	}
	printf '%s\n' "$left"
}

# Run one external process with a phase deadline and bounded termination grace.
bounded() {
	local left
	left=$(remaining) || return 1
	if [[ $# == 0 || $(type -t "$1") != file ]]; then
		echo 'bounded requires an external executable' >&2
		return 1
	fi
	timeout --signal=TERM --kill-after=5s "${left}s" "$@"
}

# Verify the live API server, node mount and exact policy before trusting audit counts.
require_audit_server() {
	: "${repo_root:?owned repository required}" "${control_node:?owned control-plane node required}"
	kubectl --request-timeout=15s -n kube-system get pods -l component=kube-apiserver -o json |
		jq -e -f "$repo_root/tests/provider/audit-server.jq" >/dev/null || return 1
	bounded docker exec "$control_node" cat /audit/policy.yaml >"$test_dir/audit/node-policy.yaml" || return 1
	cmp -s "$test_dir/audit/policy.yaml" "$test_dir/audit/node-policy.yaml" || return 1
	bounded docker exec "$control_node" test -f /audit/log.json || return 1
	echo 'PASS: running API server writes audit events using the exact acceptance policy'
}

# Inspect the complete run, including denied attempts, after positively observing reads.
require_read_only_source_audit() {
	bounded docker exec "$control_node" cat /audit/log.json |
		jq -se -f "$repo_root/tests/provider/audit-read-only.jq" >/dev/null || return 1
	echo 'PASS: controller audit contains observed GET requests and zero source mutation attempts'
}

# Retry one observation until success or the phase deadline, retaining failure diagnostics.
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

# Start the independently selected current registry pod's owned forwarding process.
start_registry_forward() {
	registry_port=''
	# Clear stale evidence in the parent before the asynchronous process can run.
	: >"$test_dir/forward.log"
	kubectl --request-timeout=0 -n products port-forward --address=127.0.0.1 "pod/$registry_pod" ":$registry_target_port" >>"$test_dir/forward.log" 2>&1 &
	registry_forward_pid=$!
	wait_for 'owned registry port-forward starts on an allocated loopback port' registry_forward_ready || {
		cat "$test_dir/forward.log" >&2
		return 1
	}
}
