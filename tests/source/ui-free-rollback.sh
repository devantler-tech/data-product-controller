#!/usr/bin/env bash
# Sourced only inside the disposable source acceptance harness.
: "${repo_root:?run through tests/source/run.sh}" "${test_dir:?run through tests/source/run.sh}"

# ui_free_release_receipt rejects incomplete, ambiguous or incorrectly bound release-verifier output.
ui_free_release_receipt() {
	local receipt=$1
	[[ -f $receipt && ! -L $receipt && $(wc -c <"$receipt") -le 65536 ]] || return 1
	jq -se 'length == 1 and (.[0] |
    .complete == true and .tag == "v5.0.0" and .platform == "linux/amd64" and
    .releaseProfile == "data-product-controller" and
    .imageReference == ("ghcr.io/devantler-tech/data-product-controller@" + .imageDigest) and
    .chartReference == ("ghcr.io/devantler-tech/charts/data-product-controller@" + .chartDigest) and
    .sourceSHA == "29b605885064a3b9f24c799a0d7112d3a6d4333b" and
    .publisherSHA == "ea4df9004d4ab43b6c9af5b0c5f55712f7211328" and
    .imageDigest == "sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c" and
    .chartDigest == "sha256:0707d9669f46cde154e4d9f38ef4d47bc4312e663f0e21d12a8afedb6e93e0ea" and
    (.runtimeDigest | type == "string" and test("^sha256:[a-f0-9]{64}$")))' "$receipt" >/dev/null
}

# ui_free_rollback_lifecycle restores actual stored signed revisions for workloads and product declarations together.
ui_free_rollback_lifecycle() {
	: "${product_digest:?immutable candidate image required}" "${candidate_chart:?packaged candidate required}"
	local release_dir=$test_dir/ui-free-release allowance phase chart version image runtime values
	local workload_revision publication_revision saved_workload saved_publication previous_workload=0 previous_publication=0
	local controller_uid baseline_controller_uid
	local candidate_version candidate_image="localhost:5055/data-product-controller@$product_digest"
	allowance=$(publication_budget)
	((allowance <= 180)) || allowance=180
	bash "$repo_root/scripts/verify-release-artifacts.sh" --profile data-product-controller --tag v5.0.0 \
		--source-sha 29b605885064a3b9f24c799a0d7112d3a6d4333b \
		--image-digest sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c \
		--chart-digest sha256:0707d9669f46cde154e4d9f38ef4d47bc4312e663f0e21d12a8afedb6e93e0ea \
		--publisher-sha ea4df9004d4ab43b6c9af5b0c5f55712f7211328 \
		--platform linux/amd64 --output-dir "$release_dir" --timeout "$allowance"
	ui_free_release_receipt "$release_dir/release.json"
	# Read and version-check the verifier's actual downloaded chart, never a local reconstruction.
	helm show chart "$release_dir/release-chart.tgz" | yq -e '.name == "data-product-controller" and .version == "5.0.0" and .appVersion == "5.0.0"' >/dev/null
	candidate_version=$(helm show chart "$candidate_chart" | yq -r '.version')

	for phase in baseline candidate rollback restored; do
		if [[ $phase == baseline || $phase == rollback ]]; then
			chart=$release_dir/release-chart.tgz
			chart_name=data-product-controller
			version=5.0.0
			image=$(jq -er '.imageReference' "$release_dir/release.json")
			runtime=$(jq -er '.runtimeDigest' "$release_dir/release.json")
		else
			chart=$candidate_chart
			chart_name=$candidate_chart_name
			version=$candidate_version
			image=$candidate_image
			runtime=$product_digest
		fi
		if [[ $phase == rollback ]]; then
			publication_helm rollback dpc "$saved_workload" --wait --timeout 240s >/dev/null
			publication_helm rollback dpc-publication "$saved_publication" --wait --timeout 180s >/dev/null
		else
			values=$test_dir/ui-free-$phase-values.json
			jq --arg repository "${image%@*}" --arg digest "${image##*@}" '
        .image.repository=$repository | .image.digest=$digest |
        .httpSource.enabled=true | .connectorReadiness.enabled=true' "$test_dir/values.yaml" >"$values"
			publication_helm upgrade dpc "$chart" --reset-values --skip-crds --values "$values" \
				--post-renderer "$repo_root/tests/source/trust-test-ca.sh" --wait --timeout 240s >/dev/null
			publication_helm upgrade dpc-publication "$chart" --reset-values --skip-crds \
				--values "$test_dir/publication-values.yaml" --set-string image.repository="${image%@*}" \
				--set-string image.digest="${image##*@}" --post-renderer "$repo_root/tests/source/publication-products.sh" \
				--wait --timeout 180s >/dev/null
		fi
		workload_revision=$(helm_installed_revision dpc "$version" "$image" "$chart_name")
		publication_revision=$(helm_installed_revision dpc-publication "$version" "$image" "$chart_name")
		((workload_revision > previous_workload && publication_revision > previous_publication))
		controller_uid=$(publication_kube get deployment dpc -o json | jq -er '.metadata.uid | select(type == "string" and length > 0)')
		publication_helm get manifest dpc --revision "$workload_revision" >"$test_dir/ui-free-$phase-workload.yaml"
		publication_helm get manifest dpc-publication --revision "$publication_revision" >"$test_dir/ui-free-$phase-products.yaml"
		if [[ $phase == baseline ]]; then
			saved_workload=$workload_revision
			saved_publication=$publication_revision
			baseline_controller_uid=$controller_uid
		elif [[ $phase == rollback ]]; then
			cmp "$test_dir/ui-free-baseline-workload.yaml" "$test_dir/ui-free-rollback-workload.yaml"
			cmp "$test_dir/ui-free-baseline-products.yaml" "$test_dir/ui-free-rollback-products.yaml"
		fi
		[[ $controller_uid == "$baseline_controller_uid" ]]
		wait_for "signed UI-free $phase reaches current product readiness" 240 readiness True true
		installed_observation "ui-free-$phase" "$image" "$runtime" KHoist
		installed_query_check
		publication_snapshot "ui-free-$phase" false
		publication_budget >/dev/null
		previous_workload=$workload_revision
		previous_publication=$publication_revision
		echo "PASS: UI-free $phase workload revision $workload_revision and publication revision $publication_revision preserve current queries and identities"
	done
	jq -n --slurpfile release "$release_dir/release.json" --argjson workload "$saved_workload" \
		--argjson publication "$saved_publication" --argjson restoredWorkload "$workload_revision" \
		--argjson restoredPublication "$publication_revision" \
		'{result:"verified-disposable-ui-free-rollback",releasedBaseline:$release[0],
      storedWorkloadRevision:$workload,storedPublicationRevision:$publication,
      restoredCandidateWorkloadRevision:$restoredWorkload,restoredCandidatePublicationRevision:$restoredPublication,
      productionAdoption:false}'
}
