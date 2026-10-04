#!/usr/bin/env bash
# Shared function definitions only; each selected module starts its own budget.
# Each engine acceptance module gets its own bounded budget.
engine_start_budget() {
	engine_started_at=$SECONDS
	engine_deadline=$((SECONDS + 480))
}
# Report the shared acceptance budget, failing once the module has exhausted it.
engine_remaining() {
	local remaining=$((engine_deadline - SECONDS))
	if ((remaining <= 0)); then
		echo 'engine-provider acceptance exceeded its eight-minute bound' >&2
		return 1
	fi
	printf '%s\n' "$remaining"
}

# Bound each asynchronous observation to both its polling allowance and the remaining module budget.
engine_wait() {
	local description=$1 remaining
	shift
	remaining=$(engine_remaining)
	((remaining <= 45)) || remaining=45
	wait_for "$description" "$remaining" "$@"
}

# Wait for gate changes to reach the actual controller Deployment within the shared budget.
engine_rollout() {
	local remaining
	remaining=$(engine_remaining)
	((remaining <= 60)) || remaining=60
	kube --request-timeout=0 rollout status deployment/dpc --timeout="${remaining}s"
}

# Prevent a stuck finalizer from turning a lifecycle assertion into an unbounded deletion wait.
engine_delete() {
	local remaining
	remaining=$(engine_remaining)
	((remaining <= 20)) || remaining=20
	kube delete "$@" --wait=true --timeout="${remaining}s"
}
# Count server-side schema rejection only; transport and authorization errors must fail acceptance.
engine_reject() {
	local description=$1 filter=$2
	jq "$filter" "$engine_product_file" >"$test_dir/engine-invalid.json"
	if kube apply --dry-run=server -f "$test_dir/engine-invalid.json" >"$test_dir/engine-admission.log" 2>&1; then
		echo "engine admission unexpectedly accepted: $description" >&2
		return 1
	fi
	# A transport or authorization failure must not count as a validation rejection.
	case "$(cat "$test_dir/engine-admission.log")" in
	*'(Invalid)'* | *'is invalid:'*) ;;
	*)
		cat "$test_dir/engine-admission.log" >&2
		echo "engine admission did not report validation failure: $description" >&2
		return 1
		;;
	esac
	echo "PASS: engine admission rejects $description"
}

