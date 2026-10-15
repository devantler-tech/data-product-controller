#!/usr/bin/env bash
# Sourced only inside the disposable source acceptance harness.
: "${repo_root:?run through tests/source/run.sh}" "${test_dir:?run through tests/source/run.sh}"

# publication_budget bounds every new command by the existing absolute suite deadline.
publication_budget() {
	: "${integration_deadline:?owned suite deadline required}"
	local remaining=$((integration_deadline - SECONDS))
	((remaining > 0)) || {
		echo 'publication migration exceeded suite deadline' >&2
		return 1
	}
	((remaining <= 240)) || remaining=240
	printf '%s\n' "$remaining"
}
publication_helm() {
	: "${cluster_context:?owned fixture context required}"
	local allowance
	allowance=$(publication_budget) || return 1
	timeout "${allowance}s" helm --kubeconfig "$KUBECONFIG" --kube-context "$cluster_context" \
		"$@" --namespace products
}
publication_kube() {
	: "${cluster_context:?owned fixture context required}"
	local allowance
	allowance=$(publication_budget) || return 1
	timeout "${allowance}s" kubectl --kubeconfig "$KUBECONFIG" --context "$cluster_context" \
		--request-timeout=15s --namespace products "$@"
}
# publication_snapshot requires both stored and live spec.ui states, retaining exact Helm ownership and non-UI specs.
publication_snapshot() {
	local phase=$1 has_ui=$2
	publication_helm get manifest dpc-publication >"$test_dir/publication-$phase.yaml"
	yq ea -o=json -I=0 '.' "$test_dir/publication-$phase.yaml" | jq -Sse --argjson has_ui "$has_ui" '
    if length == 2 and (map(.metadata.name) | sort == ["harbour-observations", "ksail-releases"]) and
      all(.[]; .apiVersion == "data.devantler.tech/v1alpha1" and .kind == "DataProduct" and
        .metadata.namespace == "products" and (.spec | has("ui")) == $has_ui)
    then sort_by(.metadata.name) else error("unexpected stored product declarations") end
  ' >"$test_dir/publication-$phase-stored.json"
	publication_kube get dataproduct harbour-observations ksail-releases -o json >"$test_dir/publication-$phase-live.json"
	jq -e --argjson has_ui "$has_ui" '
    (.items | length == 2) and all(.items[];
      (.spec | has("ui")) == $has_ui and .metadata.deletionTimestamp == null and
      .metadata.annotations["meta.helm.sh/release-name"] == "dpc-publication" and
      .metadata.annotations["meta.helm.sh/release-namespace"] == "products" and
      .metadata.labels["app.kubernetes.io/managed-by"] == "Helm")
  ' "$test_dir/publication-$phase-live.json" >/dev/null
	jq -S '[.items[] | {uid:.metadata.uid, name:.metadata.name, namespace:.metadata.namespace,
    spec:(.spec | del(.ui))}] | sort_by(.name)' "$test_dir/publication-$phase-live.json" >"$test_dir/publication-$phase-spec.json"
	jq -S '[.[] | {name:.metadata.name, namespace:.metadata.namespace, spec:(.spec | del(.ui))}]' \
		"$test_dir/publication-$phase-stored.json" >"$test_dir/publication-$phase-stored-spec.json"
	jq -S 'map(del(.uid))' "$test_dir/publication-$phase-spec.json" >"$test_dir/publication-$phase-live-spec.json"
	cmp "$test_dir/publication-$phase-stored-spec.json" "$test_dir/publication-$phase-live-spec.json"
	if [[ $phase != baseline ]]; then
		cmp "$test_dir/publication-baseline-spec.json" "$test_dir/publication-$phase-spec.json"
		cmp "$test_dir/publication-baseline-stored-spec.json" "$test_dir/publication-$phase-stored-spec.json"
	fi
	if [[ $has_ui == true ]]; then
		jq -S '[.[] | {name:.metadata.name,ui:.spec.ui}]' "$test_dir/publication-$phase-stored.json" >"$test_dir/publication-$phase-stored-ui.json"
		jq -S '[.items[] | {name:.metadata.name,ui:.spec.ui}] | sort_by(.name)' "$test_dir/publication-$phase-live.json" >"$test_dir/publication-$phase-live-ui.json"
		cmp "$test_dir/publication-$phase-stored-ui.json" "$test_dir/publication-$phase-live-ui.json"
		if [[ $phase != baseline ]]; then
			cmp "$test_dir/publication-baseline-stored-ui.json" "$test_dir/publication-$phase-stored-ui.json"
		fi
	fi
	publication_kube get crd/dataproducts.data.devantler.tech -o json | jq -S '{uid:.metadata.uid,spec:.spec}' >"$test_dir/publication-$phase-crd.json"
	cmp "$test_dir/publication-original-crd.json" "$test_dir/publication-$phase-crd.json"
}

# chart_publication_lifecycle uses a separate metadata-only release; workload/query evidence remains in the general lifecycle.
chart_publication_lifecycle() {
	: "${candidate_chart:?packaged candidate required}"
	local baseline_dir=$test_dir/publication-release revision allowance
	publication_helm list --all --filter '^dpc-publication$' --output json | jq -e 'length == 0' >/dev/null
	publication_kube get dataproduct harbour-observations ksail-releases --ignore-not-found -o json | jq -e '.items | length == 0' >/dev/null
	allowance=$(publication_budget)
	((allowance <= 180)) || allowance=180
	bash "$repo_root/scripts/verify-release-artifacts.sh" --tag v4.0.0 \
		--source-sha 4fbc7d215b3ce30091e74e9909a3ad822e878a05 \
		--image-digest sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862 \
		--chart-digest sha256:338521dc3e85774d6fff817a1af073ed1a462ee67e61459bf37f15e9a370ec89 \
		--publisher-sha ea4df9004d4ab43b6c9af5b0c5f55712f7211328 \
		--platform linux/amd64 --output-dir "$baseline_dir" --timeout "$allowance"
	jq -e '.complete == true and .tag == "v4.0.0"' "$baseline_dir/release.json" >/dev/null
	cat >"$test_dir/publication-values.yaml" <<'YAML'
route:
  enabled: true
  host: products.example.test
demoProduct:
  enabled: true
  ksailReleases:
    enabled: true
uiContract:
  enabled: true
uiAppearance:
  enabled: true
YAML
	publication_kube get crd/dataproducts.data.devantler.tech -o json | jq -S '{uid:.metadata.uid,spec:.spec}' >"$test_dir/publication-original-crd.json"
	# --skip-crds is essential: Helm handles crds/ separately from post-rendered templates.
	publication_helm upgrade --install dpc-publication "$baseline_dir/release-chart.tgz" --reset-values \
		--skip-crds --values "$test_dir/publication-values.yaml" \
		--post-renderer "$repo_root/tests/source/publication-products.sh" --wait --timeout 180s >/dev/null
	revision=$(publication_helm history dpc-publication --output json | jq -er '
    [.[] | select(.status == "deployed")] | if length == 1 then .[0].revision else error("ambiguous revision") end')
	[[ $revision =~ ^[1-9][0-9]*$ ]]
	publication_snapshot baseline true
	publication_helm upgrade dpc-publication "$candidate_chart" --reset-values --skip-crds \
		--values "$test_dir/publication-values.yaml" --post-renderer "$repo_root/tests/source/publication-products.sh" \
		--wait --timeout 180s >/dev/null
	publication_snapshot candidate false
	installed_query_check
	publication_helm rollback dpc-publication "$revision" --wait --timeout 180s >/dev/null
	publication_snapshot rollback true
	publication_helm upgrade dpc-publication "$candidate_chart" --reset-values --skip-crds \
		--values "$test_dir/publication-values.yaml" --post-renderer "$repo_root/tests/source/publication-products.sh" \
		--wait --timeout 180s >/dev/null
	publication_snapshot restored false
	publication_helm uninstall dpc-publication --wait --timeout 60s >/dev/null
	publication_kube get dataproduct harbour-observations ksail-releases --ignore-not-found -o json | jq -e '.items | length == 0' >/dev/null
	installed_query_check
	echo 'PASS: signed v4 product metadata, real Helm UI removal, stored rollback, repeated removal, stable identities and fixture-only cleanup'
}
