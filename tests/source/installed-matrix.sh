#!/usr/bin/env bash
# Real packaged releases in the same disposable cluster used by the source suite.
: "${repo_root:?owned coordinator required}" "${test_dir:?owned scratch directory required}"
: "${cluster_context:?owned cluster required}" "${product_digest:?immutable candidate required}" "${candidate_chart:?packaged candidate required}"

# matrix_observation joins immutable workload identity, current product conditions and both serving registry replicas for one phase.
matrix_observation() {
	local phase=$1 image=$2 runtime=$3
	bash "$repo_root/scripts/observe-rollout.sh" --kubeconfig "$KUBECONFIG" \
		--context "$cluster_context" --namespace products --product existing-export \
		--deployment dpc:controller --deployment dpc-http-source:http-source \
		--deployment dpc-contract-probe:contract-probe \
		--condition Ready --condition ConnectorReady --condition ContractsReady \
		--image "$image" --runtime-digest "$runtime" --timeout 180 \
		--evidence-dir "$test_dir/observed-matrix-$phase"
	[[ $(kube get dataproduct existing-export -o json | jq -er '.metadata.uid') == "$matrix_product_uid" ]]
	registry_ready true
	registry_replicas_ready
}

# installed_lifecycle_matrix runs candidate faults, a stored released rollback and candidate restoration before testing product deletion.
installed_lifecycle_matrix() {
	local baseline_image baseline_runtime baseline_revision candidate_image candidate_version
	baseline_image="ghcr.io/devantler-tech/data-product-controller@$(jq -er '.imageDigest' "$test_dir/baseline/release.json")"
	baseline_runtime=$(jq -er '.runtimeDigest' "$test_dir/baseline/release.json")
	candidate_image="localhost:5055/data-product-controller@$product_digest"
	candidate_version=$(helm show chart "$candidate_chart" | yq -r '.version')
	matrix_product_uid=$(kube get dataproduct existing-export -o json | jq -er '.metadata.uid')
	# The released operational revision includes the independent probe. Rolling back
	# to a revision without it would delete it rather than prove UID continuity.
	jq --arg cidr "$DPC_SOURCE_IP/32" '.contractProbe={enabled:true,
	  url:"https://source.products.svc.cluster.local/contract",targetCIDR:$cidr,
	  monitorPodLabels:{app:"source-consumer"}}' "$test_dir/values.yaml" >"$test_dir/operational-values.yaml"
	mv "$test_dir/operational-values.yaml" "$test_dir/values.yaml"
	jq --arg digest "${baseline_image##*@}" '.image.repository="ghcr.io/devantler-tech/data-product-controller" |
	  .image.digest=$digest | .httpSource.enabled=true | .connectorReadiness.enabled=true |
	  .contractReadiness.enabled=true' "$test_dir/values.yaml" >"$test_dir/operational-baseline-values.yaml"
	helm_scoped upgrade dpc "$test_dir/baseline/release-chart.tgz" --reset-values \
		--values "$test_dir/operational-baseline-values.yaml" \
		--post-renderer "$repo_root/tests/source/trust-test-ca.sh" --wait --timeout 240s >/dev/null
	helm_installed_identity 1.12.0 "$baseline_image"
	baseline_revision=$(helm_scoped get metadata dpc --output json | jq -er '.revision | select(type == "number" and . > 0 and . == floor)')
	[[ $baseline_revision =~ ^[1-9][0-9]*$ ]]
	wait_for 'operational released baseline has a complete independent probe' 180 lifecycle_rollout dpc-contract-probe full
	wait_for 'operational baseline retains the declared source' 180 readiness True true
	[[ $(kube get dataproduct existing-export -o json | jq -er '.metadata.uid') == "$matrix_product_uid" ]]
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true --set contractReadiness.enabled=false
	helm_installed_identity "$candidate_version" "$candidate_image"
	wait_for 'candidate operational source has a full current rollout' 180 lifecycle_rollout dpc-http-source full
	wait_for 'candidate restores the exact selected product' 180 readiness True true
	source_lifecycle_run
	source_lifecycle_replicas_run
	contract_matrix_run
	connector_matrix_run
	matrix_observation candidate "$candidate_image" "$product_digest"
	# Helm's actual stored revision, not a reapplied baseline rendering.
	helm_scoped rollback dpc "$baseline_revision" --wait --timeout 240s >/dev/null
	helm_installed_identity 1.12.0 "$baseline_image"
	wait_for 'operational rollback restores independent contracts' 180 contract_readiness True ContractsReady
	matrix_observation rollback "$baseline_image" "$baseline_runtime"
	source_lifecycle_rollback_check
	connector_matrix_rollback_check
	contract_matrix_rollback_check
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true --set contractReadiness.enabled=true
	helm_installed_identity "$candidate_version" "$candidate_image"
	wait_for 'candidate restoration recovers the selected contract' 180 contract_readiness True ContractsReady
	matrix_observation restored-candidate "$candidate_image" "$product_digest"
	source_lifecycle_retention_check
	contract_matrix_retention_check
	kube delete dataproduct existing-export --wait=true --timeout=20s >/dev/null
	wait_for 'deleted exact product is absent from the registry' 120 probe --url http://dpc/api/v1/products --registry-absent products/existing-export
	source_lifecycle_retention_check
	contract_matrix_retention_check
	echo 'PASS: installed source, connector and contract matrices survive real released rollback, candidate restore and product deletion'
}
