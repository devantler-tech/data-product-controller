#!/usr/bin/env bash
: "${test_dir:?integration scratch directory is required}"
# Real Deployment transitions; the controller only observes the installed workload.

# connector_matrix_run proves that capacity, rollouts, access and flags control readiness while the independent contract stays healthy.
connector_matrix_run() {
	lifecycle_begin 'installed connector readiness matrix' 480 || return 1
	lifecycle_wait 'independent contract is healthy before connector failures' 120 lifecycle_contract_healthy
	kube scale deployment/dpc-http-source --replicas=0
	lifecycle_wait 'the Deployment reaches actual zero capacity' 120 lifecycle_rollout dpc-http-source zero
	lifecycle_wait 'zero capacity withdraws the selected product within the observation allowance' 45 readiness False false ConnectorScaledToZero
	lifecycle_contract_healthy
	kube scale deployment/dpc-http-source --replicas=1
	lifecycle_wait 'positive capacity completes the current rollout' 180 lifecycle_rollout dpc-http-source full
	lifecycle_wait 'restored capacity recovers selected product readiness within the observation allowance' 45 readiness True true

	local generation old_uid old_address
	generation=$(kube get deployment dpc-http-source -o json | jq -er '.metadata.generation')
	old_uid=$(source_pod)
	old_address=$(kube get pods -l app.kubernetes.io/component=http-source -o json | jq -er --arg uid "$old_uid" \
		'.items[] | select(.metadata.uid == $uid) | .status.podIP | select(type == "string" and length > 0)')
	kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=false
	lifecycle_wait 'new observed generation retains old serving capacity during an incomplete rollout' 180 lifecycle_rollout dpc-http-source partial "$generation"
	lifecycle_wait 'old serving capacity cannot conceal incomplete rollout readiness within the observation allowance' 45 readiness False false ConnectorNotReady
	probe --url "http://$old_address:8080/api/data" --contains '"fixture":"source"'
	lifecycle_contract_healthy
	kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=true
	lifecycle_wait 'reenabled current generation reaches full capacity' 180 lifecycle_rollout dpc-http-source full
	lifecycle_wait 'full current generation restores the selected product within the observation allowance' 45 readiness True true

	kube delete role export-connector-observer
	lifecycle_wait 'revoked exact-name observer access fails closed within the observation allowance' 45 readiness False false ConnectorAccessDenied
	lifecycle_contract_healthy
	kube apply -f "$test_dir/observer-rbac.yaml"
	lifecycle_wait 'restored exact-name observer access recovers within the observation allowance' 45 readiness True true

	kube set env deployment/dpc CONNECTOR_READINESS_ENABLED=false
	lifecycle_wait 'disabled connector observer completes its current rollout' 180 lifecycle_rollout dpc full
	lifecycle_wait 'disabled connector observation withdraws the selected product' 180 readiness False false ConnectorFeatureDisabled
	lifecycle_contract_healthy
	kube set env deployment/dpc CONNECTOR_READINESS_ENABLED=true
	lifecycle_wait 'enabled connector observer completes its current rollout' 180 lifecycle_rollout dpc full
	lifecycle_wait 'reenabled connector observation recovers the selected product' 180 readiness True true
	lifecycle_contract_healthy
	echo 'PASS: connector failures preserve independent contract readiness'
}

# connector_matrix_rollback_check requires complete current connector capacity and independent contract health after rollback.
connector_matrix_rollback_check() {
	lifecycle_begin 'connector readiness after installed rollback' 90 || return 1
	lifecycle_wait 'rollback restores full current-generation connector capacity' 90 lifecycle_rollout dpc-http-source full
	lifecycle_wait 'rollback restores current-generation connector and aggregate readiness' 90 readiness True true
	lifecycle_contract_healthy
}
