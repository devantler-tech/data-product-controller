#!/usr/bin/env bash
: "${repo_root:?run through the installed-release coordinator}" "${test_dir:?integration scratch directory is required}" "${source_container:?owned source fixture is required}"
# The independent TLS publication fails without changing the authenticated source.

contract_matrix_run() {
	lifecycle_begin 'installed independent contract matrix' 480 || return 1
	jq --arg cidr "$DPC_SOURCE_IP/32" '.contractProbe={enabled:true,
    url:"https://source.products.svc.cluster.local/contract",targetCIDR:$cidr,
    monitorPodLabels:{app:"source-consumer"}}' "$test_dir/values.yaml" >"$test_dir/contract-values.yaml"
	mv "$test_dir/contract-values.yaml" "$test_dir/values.yaml"
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true --set contractReadiness.enabled=false
	lifecycle_wait 'installed probe completes its current rollout' 240 lifecycle_rollout dpc-contract-probe full
	kube patch dataproduct existing-export --type=merge -p '{"spec":{"outputs":[{"name":"query","protocol":"OpenAPI","url":"https://export.example.com/api/data","contractUrl":"https://source.products.svc.cluster.local/contract","mediaType":"application/json"}],"contractChecks":[{"output":"query","resourceRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"dpc-contract-probe"}}]}}'
	lifecycle_wait 'contract observation defaults off without changing connector health' 180 contract_readiness False ContractFeatureDisabled
	kube set env deployment/dpc CONTRACT_READINESS_ENABLED=true
	lifecycle_wait 'enabled contract observer completes its current rollout' 180 lifecycle_rollout dpc full
	lifecycle_wait 'contract observation requires exact-resource RBAC' 180 contract_readiness False ContractProbeAccessDenied
	yq 'with(select(.kind == "Role"); .rules[0].resourceNames = ["dpc-contract-probe"]) |
    with(select(.kind == "RoleBinding"); .subjects[0].name = "dpc" | .subjects[0].namespace = "products")' \
		"$repo_root/docs/examples/contract-observer-rbac.yaml" >"$test_dir/contract-rbac.yaml"
	kube apply -f "$test_dir/contract-rbac.yaml"
	lifecycle_wait 'selected contract becomes ready through its independent probe' 180 contract_readiness True ContractsReady
	contract_matrix_retention_capture

	local phase
	phase=$(lifecycle_phase)
	docker exec "$source_container" /fixture control contract-down
	lifecycle_wait 'contract outage withdraws readiness with the connector otherwise healthy' 240 contract_readiness False ContractProbeNotReady
	lifecycle_wait 'management records a fresh contract outage' 90 lifecycle_metrics contract-probe 0 "$phase"
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	phase=$(lifecycle_phase)
	docker exec "$source_container" /fixture control contract-up
	lifecycle_wait 'independent contract recovery restores selected product readiness' 240 contract_readiness True ContractsReady
	lifecycle_wait 'management records fresh contract recovery' 90 lifecycle_metrics contract-probe 1 "$phase"
	kube patch dataproduct existing-export --type=json -p '[{"op":"replace","path":"/spec/outputs/0/contractUrl","value":"https://source.products.svc.cluster.local/other"}]'
	lifecycle_wait 'changed output URL rejects the old healthy probe binding' 180 contract_readiness False ContractProbeConfigurationMismatch
	kube patch dataproduct existing-export --type=json -p '[{"op":"replace","path":"/spec/outputs/0/contractUrl","value":"https://source.products.svc.cluster.local/contract"}]'
	lifecycle_wait 'restored literal URL binding recovers' 180 contract_readiness True ContractsReady
	kube delete role export-contract-observer
	lifecycle_wait 'contract RBAC revocation becomes unready' 180 contract_readiness False ContractProbeAccessDenied
	kube apply -f "$test_dir/contract-rbac.yaml"
	lifecycle_wait 'contract RBAC restoration recovers' 180 contract_readiness True ContractsReady

	kube set env deployment/dpc-contract-probe CONTRACT_READINESS_ENABLED=false
	lifecycle_wait 'disabled probe configuration fails closed' 180 contract_readiness False ContractProbeConfigurationMismatch
	lifecycle_wait 'the actual disabled probe replica refuses execution' 180 contract_disabled_probe
	kube set env deployment/dpc-contract-probe CONTRACT_READINESS_ENABLED=true
	lifecycle_wait 'reenabled probe completes its current rollout' 180 lifecycle_rollout dpc-contract-probe full
	lifecycle_wait 'reenabled probe restores selected product readiness' 180 contract_readiness True ContractsReady
	lifecycle_wait 'unauthorized monitor transport is denied' 90 kube exec outsider -- /fixture probe \
		--url http://dpc-contract-probe:8081/metrics --want-error --timeout 5s
	kube patch dataproduct existing-export --type=merge -p '{"spec":{"contractChecks":null}}'
	lifecycle_wait 'removing checks preserves the connector and aggregate readiness' 180 readiness True true
	kube get dataproduct existing-export -o json | jq -e 'all(.status.conditions[]; .type != "ContractsReady")' >/dev/null
	contract_matrix_retention_check
	kube patch dataproduct existing-export --type=merge -p '{"spec":{"contractChecks":[{"output":"query","resourceRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"dpc-contract-probe"}}]}}'
	lifecycle_wait 'redeclared independent check recovers current readiness' 180 contract_readiness True ContractsReady
	# Store both controller gates in Helm's operational values for the rollback coordinator.
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true --set contractReadiness.enabled=true
	lifecycle_wait 'installed release retains healthy independent contracts' 180 contract_readiness True ContractsReady
}

contract_disabled_probe() {
	local address
	address=$(kube get pods -l app.kubernetes.io/component=contract-probe -o json | jq -er '
    [.items[] | select(.metadata.deletionTimestamp == null) |
      select(any(.spec.containers[].env[]?; .name == "CONTRACT_READINESS_ENABLED" and .value == "false")) |
      .status.podIP // empty] | first // error("disabled probe Pod unavailable")')
	probe --url "http://$address:8081/readyz" --want-status 503 --contains FeatureDisabled
}

contract_matrix_rollback_check() {
	lifecycle_begin 'contract reachability after installed rollback' 120 || return 1
	local phase
	phase=$(lifecycle_phase)
	lifecycle_wait 'rollback retains a complete independent probe rollout' 90 lifecycle_rollout dpc-contract-probe full
	lifecycle_wait 'rollback restores selected contract readiness' 90 contract_readiness True ContractsReady
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	lifecycle_wait 'rollback completes a fresh independent contract observation' 90 lifecycle_metrics contract-probe 1 "$phase"
	contract_matrix_retention_check
}

contract_matrix_retention_capture() {
	lifecycle_product_uid=$(kube get dataproduct existing-export -o json | jq -er '.metadata.uid | select(type == "string" and length > 0)')
	contract_matrix_uids=$(kube get deployment/dpc-contract-probe -o json | jq -ceS \
		-f "$repo_root/tests/source/lifecycle-state.jq" --arg mode ownership --arg product_uid "$lifecycle_product_uid" \
		--argjson wanted '[{"kind":"Deployment","name":"dpc-contract-probe"}]')
}

contract_matrix_retention_check() {
	local current
	current=$(kube get deployment/dpc-contract-probe -o json | jq -ceS \
		-f "$repo_root/tests/source/lifecycle-state.jq" --arg mode ownership --arg product_uid "${lifecycle_product_uid:?capture ownership before rollback/deletion}" \
		--argjson wanted '[{"kind":"Deployment","name":"dpc-contract-probe"}]')
	[[ "$current" == "${contract_matrix_uids:?capture probe identity}" ]] || {
		echo 'independent contract probe identity changed during lifecycle acceptance' >&2
		return 1
	}
	echo 'PASS: independent contract probe retains its original identity without product ownership'
}
