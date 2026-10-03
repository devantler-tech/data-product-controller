#!/usr/bin/env bash
# Functions for the owned disposable installed-release coordinator.
: "${repo_root:?run through the installed-release coordinator}" "${test_dir:?integration scratch directory is required}"

lifecycle_begin() {
	[[ ${cluster_started:-false} == true && ${source_started:-false} == true &&
		${cluster_name:-} == dpc-e2e-* && ${source_container:-} == "$cluster_name-source" &&
		${KUBECONFIG:-} == "$test_dir/kubeconfig" && -f "$KUBECONFIG" ]] || {
		echo 'lifecycle acceptance requires the owned disposable fixture' >&2
		return 1
	}
	# Leave time for the existing composition/catalog/provider cases and cleanup.
	lifecycle_suite_deadline=${lifecycle_suite_deadline:-$((SECONDS + 18 * 60))}
	lifecycle_deadline=$((SECONDS + $2))
	((lifecycle_deadline <= lifecycle_suite_deadline)) || lifecycle_deadline=$lifecycle_suite_deadline
	echo "PHASE: $1"
}

lifecycle_wait() {
	local description=$1 timeout=$2 remaining=$((lifecycle_deadline - SECONDS))
	shift 2
	((remaining > 0)) || {
		echo 'lifecycle acceptance deadline exhausted' >&2
		return 1
	}
	((timeout <= remaining)) || timeout=$remaining
	wait_for "$description" "$timeout" "$@"
}

lifecycle_phase() {
	local timestamp seconds
	timestamp=$(date +%s.%N) || return 1
	if [[ "$timestamp" =~ ^[1-9][0-9]{0,10}\.[0-9]{9}$ ]]; then
		printf '%s\n' "$timestamp"
	else
		# BSD date does not expand %N. A future whole-second boundary is conservative.
		seconds=$(date +%s) || return 1
		[[ "$seconds" =~ ^[1-9][0-9]{0,10}$ ]] || return 1
		printf '%s\n' "$((seconds + 1))"
	fi
}

lifecycle_metrics() {
	local kind=$1 ready=$2 since=$3 address
	case "$kind" in
	http-source) address=http://dpc-http-source-metrics:8081/metrics ;;
	contract-probe) address=http://dpc-contract-probe:8081/metrics ;;
	*) return 1 ;;
	esac
	kube exec consumer -- /fixture metrics --url "$address" --kind "$kind" --ready "$ready" --since "$since" --timeout 10s
}

lifecycle_rollout() {
	kube get "deployment/$1" -o json | jq -e -f "$repo_root/tests/source/lifecycle-state.jq" \
		--arg mode "$2" --arg name "$1" --argjson previous "${3:-0}" >/dev/null
}

lifecycle_contract_healthy() {
	kube get dataproduct existing-export -o json | jq -e '
    . as $p | any(.status.conditions[]?; .type == "ContractsReady" and .status == "True" and
      .observedGeneration == $p.metadata.generation)' >/dev/null
}

source_lifecycle_run() {
	lifecycle_begin 'installed HTTP source lifecycle' 480 || return 1
	lifecycle_wait 'authorized consumer reads the export' 120 probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	probe --url http://dpc-http-source/openapi.json --contains '"openapi"'
	probe --url http://dpc-http-source/metrics --want-status 404
	probe --url http://dpc-http-source/readyz --want-status 404
	probe --url http://dpc-http-source/healthz --want-status 404
	echo 'PASS: public query listener does not expose management endpoints'
	source_lifecycle_retention_capture
	local connector_uid phase
	connector_uid=$(source_pod)
	lifecycle_wait 'unauthorized consumer transport is denied' 90 kube exec outsider -- /fixture probe \
		--url http://dpc-http-source/api/data --want-error --timeout 5s
	lifecycle_wait 'unauthorized monitor cannot reach source management metrics' 90 kube exec outsider -- /fixture probe \
		--url http://dpc-http-source-metrics:8081/metrics --want-error --timeout 5s
	kube label pod outsider app=source-consumer --overwrite
	lifecycle_wait 'the same consumer succeeds after its identity grant' 90 kube exec outsider -- /fixture probe \
		--url http://dpc-http-source/api/data --contains '"fixture":"source"' --timeout 5s
	kube label pod outsider app=outsider --overwrite
	lifecycle_wait 'withdrawing the same identity restores transport denial' 90 kube exec outsider -- /fixture probe \
		--url http://dpc-http-source/api/data --want-error --timeout 5s
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'

	phase=$(lifecycle_phase)
	docker exec "$source_container" /fixture control down
	lifecycle_wait 'source outage reaches conditions and the selected registry product' 240 readiness False false
	lifecycle_wait 'management reports a fresh source outage' 90 lifecycle_metrics http-source 0 "$phase"
	phase=$(lifecycle_phase)
	docker exec "$source_container" /fixture control healthy
	lifecycle_wait 'source recovery reaches the selected product' 240 readiness True true
	lifecycle_wait 'management reports fresh source recovery' 90 lifecycle_metrics http-source 1 "$phase"

	phase=$(lifecycle_phase)
	docker exec "$source_container" /fixture control rotated
	lifecycle_wait 'rotated upstream credential rejects the projected old token' 240 readiness False false
	lifecycle_wait 'management records the rejected old token' 90 lifecycle_metrics http-source 0 "$phase"
	phase=$(lifecycle_phase)
	source_secret fixture-token-b
	lifecycle_wait 'projected credential rotation recovers the source' 300 readiness True true
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	lifecycle_wait 'management records the new projected credential' 90 lifecycle_metrics http-source 1 "$phase"
	[[ $(source_pod) == "$connector_uid" ]] || {
		echo 'credential rotation replaced the connector Pod' >&2
		return 1
	}
	echo 'PASS: projected credential rotation retained the connector Pod'

	kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=false
	lifecycle_wait 'the disabled new replica refuses data and readiness' 180 disabled_export
	lifecycle_wait 'disabled incomplete rollout withdraws the selected product' 180 readiness False false
	kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=true
	lifecycle_wait 'reenabled export completes its current rollout' 240 lifecycle_rollout dpc-http-source full
	lifecycle_wait 'reenabled export recovers selected product readiness' 240 readiness True true

	# Restore the initial credential pair for subsequent independent matrices and rollback.
	docker exec "$source_container" /fixture control healthy
	source_secret fixture-token-a
	lifecycle_wait 'initial credential pair is restored' 300 probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	lifecycle_wait 'restored source remains fully ready' 240 readiness True true
}

source_lifecycle_rollback_check() {
	lifecycle_begin 'HTTP source after installed rollback' 120 || return 1
	local phase
	phase=$(lifecycle_phase)
	lifecycle_wait 'rollback preserves a complete connector rollout' 90 lifecycle_rollout dpc-http-source full
	lifecycle_wait 'rollback restores the selected product' 90 readiness True true
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	probe --url http://dpc-http-source/openapi.json --contains '"openapi"'
	lifecycle_wait 'rollback has a fresh completed source observation' 90 lifecycle_metrics http-source 1 "$phase"
	source_lifecycle_retention_check
}

source_lifecycle_retention_capture() {
	lifecycle_product_uid=$(kube get dataproduct existing-export -o json | jq -er '.metadata.uid | select(type == "string" and length > 0)')
	source_lifecycle_uids=$(kube get deployment/dpc-http-source secret/existing-export -o json | jq -ceS \
		-f "$repo_root/tests/source/lifecycle-state.jq" --arg mode ownership --arg product_uid "$lifecycle_product_uid" \
		--argjson wanted '[{"kind":"Deployment","name":"dpc-http-source"},{"kind":"Secret","name":"existing-export"}]')
	source_lifecycle_container_id=$(docker inspect "$source_container" --format '{{.Id}}')
	[[ -n "$source_lifecycle_container_id" ]]
}

source_lifecycle_retention_check() {
	local current
	current=$(kube get deployment/dpc-http-source secret/existing-export -o json | jq -ceS \
		-f "$repo_root/tests/source/lifecycle-state.jq" --arg mode ownership --arg product_uid "${lifecycle_product_uid:?capture ownership before rollback/deletion}" \
		--argjson wanted '[{"kind":"Deployment","name":"dpc-http-source"},{"kind":"Secret","name":"existing-export"}]')
	[[ "$current" == "${source_lifecycle_uids:?capture source resource identities}" &&
		$(docker inspect "$source_container" --format '{{.Id}}') == "$source_lifecycle_container_id" ]] || {
		echo 'source ownership or identity changed during lifecycle acceptance' >&2
		return 1
	}
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	docker exec "$source_container" /fixture probe --url http://127.0.0.1:9000/healthz --timeout 3s
	echo 'PASS: independently owned source, connector and credentials retain their identities'
}
