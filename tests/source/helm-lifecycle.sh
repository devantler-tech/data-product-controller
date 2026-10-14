#!/usr/bin/env bash
# Sourced only inside the disposable source acceptance harness.
: "${repo_root:?run through tests/source/run.sh}" "${test_dir:?run through tests/source/run.sh}"
helm_scoped() {
	: "${cluster_context:?owned fixture context required}"
	helm --kubeconfig "$KUBECONFIG" --kube-context "$cluster_context" "$@" --namespace products
}

helm_installed_identity() {
	local version=$1 image=$2 metadata revision final
	metadata=$(helm_scoped get metadata dpc --output json) || return 1
	revision=$(jq -ser --arg version "$version" '
    if length == 1 then .[0] else error("ambiguous metadata") end |
    select(.status == "deployed" and .chart == "data-product-controller" and
      .version == $version and .appVersion == $version and
      (.revision | type == "number" and . > 0 and . == floor)) | .revision' <<<"$metadata") || return 1
	[[ $revision =~ ^[1-9][0-9]*$ ]] || return 1
	helm_scoped get values dpc --revision "$revision" --output json | jq -se --arg image "$image" '
    length == 1 and (.[0] | (.image.repository + "@" + .image.digest) == $image)' >/dev/null || return 1
	final=$(helm_scoped get metadata dpc --output json) || return 1
	jq -ne --argjson before "$metadata" --argjson after "$final" '$before == $after' >/dev/null
}

install_chart() {
	: "${candidate_chart:?packaged candidate required}"
	helm_scoped upgrade --install dpc "$candidate_chart" --reset-values \
		--values "$test_dir/values.yaml" --post-renderer "$repo_root/tests/source/trust-test-ca.sh" \
		--wait --timeout 240s "$@" >/dev/null
}

installed_observation() {
	local phase=$1 image=$2 runtime=$3
	bash "$repo_root/scripts/observe-rollout.sh" --kubeconfig "$KUBECONFIG" \
		--context "$cluster_context" --namespace products --product existing-export \
		--deployment dpc:controller --deployment dpc-http-source:http-source \
		--condition Ready --condition ConnectorReady --image "$image" \
		--runtime-digest "$runtime" --timeout 180 --evidence-dir "$test_dir/observed-$phase"
	registry_ready true
	registry_replicas_ready
}

installed_query_check() {
	probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	probe --url http://dpc-http-source/openapi.json --contains '"openapi"'
	[[ $(kube get dataproduct existing-export -o json | jq -er '.metadata.uid') == "$lifecycle_product_uid" ]]
	[[ $(independent_resource_uids "$lifecycle_product_uid") == "$lifecycle_resource_uids" ]]
}

helm_release_lifecycle() {
	: "${product_digest:?immutable candidate image required}"
	local baseline_dir=$test_dir/baseline baseline_image baseline_runtime baseline_revision baseline_crd_uid candidate_version
	bash "$repo_root/scripts/verify-release-artifacts.sh" --tag v1.12.0 \
		--source-sha ff7ddcf264f8ce4813ccdb6675a45b4d1e6d1b78 \
		--image-digest sha256:62350d68a853708357f463b74ea9ff847d94100d042125b4f2d0c6f25da54886 \
		--chart-digest sha256:9d57e4c7b26664a560a2b72cb8f89dfee4c1466223081a039579cc9ffa3d8575 \
		--publisher-sha df7fd4f83edade31c121a9de563d9a7b9b1f900d \
		--platform linux/amd64 --output-dir "$baseline_dir" --timeout 180
	baseline_image="ghcr.io/devantler-tech/data-product-controller@$(jq -er '.imageDigest' "$baseline_dir/release.json")"
	baseline_runtime=$(jq -er '.runtimeDigest' "$baseline_dir/release.json")
	jq --arg digest "${baseline_image##*@}" '.image.repository="ghcr.io/devantler-tech/data-product-controller" |
    .image.digest=$digest | .httpSource.enabled=true | .connectorReadiness.enabled=true' \
		"$test_dir/values.yaml" >"$test_dir/baseline-values.yaml"
	helm_scoped upgrade --install dpc "$baseline_dir/release-chart.tgz" --reset-values \
		--values "$test_dir/baseline-values.yaml" --post-renderer "$repo_root/tests/source/trust-test-ca.sh" \
		--wait --timeout 240s >/dev/null
	helm_installed_identity 1.12.0 "$baseline_image"
	baseline_revision=$(helm_scoped history dpc --output json | jq -er '[.[] | select(.status == "deployed")] | if length == 1 then .[0].revision else error("ambiguous deployed revision") end')
	[[ $baseline_revision =~ ^[1-9][0-9]*$ ]] || {
		echo 'invalid installed baseline revision' >&2
		return 1
	}
	baseline_crd_uid=$(kubectl --request-timeout=15s get crd/dataproducts.data.devantler.tech -o json | jq -er '.metadata.uid')
	# Helm does not upgrade CRDs. Prove the installed release has the older profile before applying the candidate explicitly.
	kubectl --request-timeout=15s get crd/dataproducts.data.devantler.tech -o json | jq -e '
    .spec.versions[] | select(.name == "v1alpha1") | .schema.openAPIV3Schema.properties.spec.properties.source.properties.adapter.enum |
    index("percona-mongodb/v1") == null' >/dev/null
	yq '.spec.connector.resourceRef.name = "dpc-http-source"' "$repo_root/docs/examples/http-source-product.yaml" | kube apply -f - >/dev/null
	yq 'with(select(.kind == "Role"); .rules[0].resourceNames = ["dpc-http-source"]) |
    with(select(.kind == "RoleBinding"); .subjects[0].name = "dpc" | .subjects[0].namespace = "products")' \
		"$repo_root/docs/examples/connector-observer-rbac.yaml" >"$test_dir/observer-rbac.yaml"
	kube apply -f "$test_dir/observer-rbac.yaml" >/dev/null
	wait_for 'installed signed baseline reaches exact product readiness' 240 readiness True true
	lifecycle_product_uid=$(kube get dataproduct existing-export -o json | jq -er '.metadata.uid')
	lifecycle_resource_uids=$(independent_resource_uids "$lifecycle_product_uid")
	installed_observation baseline "$baseline_image" "$baseline_runtime"
	installed_query_check
	helm show crds "$candidate_chart" | kubectl --request-timeout=15s apply -f - >/dev/null
	kubectl --request-timeout=0 wait crd/dataproducts.data.devantler.tech --for=condition=Established --timeout=60s >/dev/null
	kubectl --request-timeout=15s get crd/dataproducts.data.devantler.tech -o json | jq -e --arg uid "$baseline_crd_uid" '
    .metadata.uid == $uid and any(.spec.versions[]; .name == "v1alpha1" and
    (.schema.openAPIV3Schema.properties.spec.properties.source.properties.adapter.enum | index("percona-mongodb/v1") != null))' >/dev/null
	candidate_version=$(helm show chart "$candidate_chart" | yq -r '.version')
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true
	helm_installed_identity "$candidate_version" "localhost:5055/data-product-controller@$product_digest"
	wait_for 'installed candidate upgrade restores exact product readiness' 240 readiness True true
	installed_observation candidate "localhost:5055/data-product-controller@$product_digest" "$product_digest"
	installed_query_check
	helm_scoped rollback dpc "$baseline_revision" --wait --timeout 240s >/dev/null
	helm_installed_identity 1.12.0 "$baseline_image"
	wait_for 'real Helm rollback restores the released baseline and query' 240 readiness True true
	installed_observation rollback "$baseline_image" "$baseline_runtime"
	installed_query_check
	[[ $(kubectl --request-timeout=15s get crd/dataproducts.data.devantler.tech -o json | jq -er '.metadata.uid') == "$baseline_crd_uid" ]]
	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true
	installed_observation restored-candidate "localhost:5055/data-product-controller@$product_digest" "$product_digest"
	installed_query_check
	kube delete dataproduct existing-export --wait=true --timeout=20s >/dev/null
	[[ $(independent_resource_uids "$lifecycle_product_uid") == "$lifecycle_resource_uids" ]]
	kube delete -f "$test_dir/observer-rbac.yaml" --wait=true --timeout=20s >/dev/null
	# Reset the owned fixture so the remaining flag-off and exact-grant cases begin at their declared default.
	install_chart
	echo 'PASS: signed packaged baseline install, explicit retained CRD upgrade, candidate upgrade, real rollback and independent source retention'
}
