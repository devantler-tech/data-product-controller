#!/usr/bin/env bash
# Sourced by run.sh inside its owned ephemeral cluster and cleanup boundary.
: "${repo_root:?run through tests/source/run.sh}"

composition_ready() {
	local reason=$1 status=$2
	kube get dataproduct coastal-summary -o json | jq -e --arg reason "$reason" --arg status "$status" '
    . as $product |
    any(.status.conditions[]?; .type == "Ready" and .reason == $reason and
      .status == $status and .observedGeneration == $product.metadata.generation)' >/dev/null
}

cycle_lineage_unready() {
	kube get dataproduct coastal-summary -o json | jq -e '
    any(.status.inputs[]?; .name == "forecast" and .ready == false and .reason == "DependencyNotReady")' >/dev/null
}

kube apply -f "$repo_root/docs/examples/composition.yaml"
wait_for 'required input contracts fail closed while composition is disabled' 120 composition_ready CompositionFeatureDisabled False
kube set env deployment/dpc COMPOSITION_ENABLED=true
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
wait_for 'three products reach compatible composition readiness' 120 composition_ready DependenciesReady True
wait_for 'public registry exposes verified composition' 120 probe --url http://dpc/api/v1/products --contains '"composition":{"reason":"CompositionVerified"'
probe --url http://dpc/api/v1/products --contains '"productID":"urn:example:harbour"'
echo 'PASS: public registry exposes observed producer lineage'

# A namespace-scoped author must not gain producer metadata through the controller.
kubectl --request-timeout=15s create namespace private-products
kube get dataproduct harbour -o json | jq 'del(.metadata, .status) |
  .metadata={name:"private-harbour",namespace:"private-products"} |
  .spec.owner.name="Private producer team"' | kubectl --request-timeout=15s apply -f -
kube create serviceaccount composition-reader
kube create role composition-reader --verb=get --resource=dataproducts.data.devantler.tech
kube create rolebinding composition-reader --role=composition-reader --serviceaccount=products:composition-reader
if kubectl --request-timeout=15s --as=system:serviceaccount:products:composition-reader \
	-n private-products get dataproduct private-harbour >/dev/null 2>&1; then
	echo 'namespace-scoped reader unexpectedly accessed the foreign producer' >&2
	exit 1
fi
kube patch dataproduct coastal-summary --type=json -p '[{"op":"replace","path":"/spec/inputs/0/productRef/name","value":"private-harbour"},{"op":"add","path":"/spec/inputs/0/productRef/namespace","value":"private-products"}]'
wait_for 'cross-namespace composition is denied before observing the producer' 120 composition_ready CrossNamespaceDependencyDenied False
kube --as=system:serviceaccount:products:composition-reader get dataproduct coastal-summary -o json |
	jq -e '.status.inputs[] | select(.name=="observations") |
    .reason=="CrossNamespaceDependencyDenied" and .ready==false and
    .owner==null and .version==null and .productID==null and .output==null' >/dev/null
echo 'PASS: namespace-scoped reader cannot obtain foreign producer metadata from consumer status'
kube patch dataproduct coastal-summary --type=json -p '[{"op":"replace","path":"/spec/inputs/0/productRef/name","value":"harbour"},{"op":"remove","path":"/spec/inputs/0/productRef/namespace"}]'
wait_for 'same-namespace producer restores composition after a denied reference' 120 composition_ready DependenciesReady True
kubectl --request-timeout=15s delete namespace private-products --wait=false
kubectl --request-timeout=15s -n private-products wait --for=delete dataproduct/private-harbour --timeout=60s

kube patch dataproduct harbour --type=merge -p '{"spec":{"version":"v2.0.0"}}'
wait_for 'breaking producer upgrade makes its consumer incompatible' 120 composition_ready ContractIncompatible False
wait_for 'public registry reports incompatible composition' 120 probe --url http://dpc/api/v1/products --contains '"composition":{"reason":"ContractIncompatible"'
kube patch dataproduct harbour --type=merge -p '{"spec":{"version":"v1.3.0"}}'
wait_for 'compatible producer upgrade recovers its consumer' 120 composition_ready DependenciesReady True

kube patch dataproduct weather --type=merge -p '{"spec":{"inputs":[{"name":"summary","productRef":{"name":"coastal-summary","output":"query"}}]}}'
wait_for 'circular dependency reports a cycle instead of waiting forever' 120 composition_ready DependencyCycle False
wait_for 'cycle participants converge on unready input lineage' 120 cycle_lineage_unready
cycle_version=$(kube get dataproduct coastal-summary -o jsonpath='{.metadata.resourceVersion}')
sleep 5
[[ "$(kube get dataproduct coastal-summary -o jsonpath='{.metadata.resourceVersion}')" == "$cycle_version" ]] || {
	echo 'unchanged cycle caused status writes' >&2
	exit 1
}
echo 'PASS: stable dependency cycle does not hot-loop status writes'
kube patch dataproduct weather --type=merge -p '{"spec":{"inputs":null}}'
wait_for 'removing circular reference recovers dependent products' 120 composition_ready DependenciesReady True

kube patch dataproduct coastal-summary --type=json -p '[{"op":"replace","path":"/spec/inputs/0/productRef/output","value":"missing"}]'
wait_for 'missing selected port is actionable' 120 composition_ready OutputNotFound False
kube patch dataproduct coastal-summary --type=json -p '[{"op":"replace","path":"/spec/inputs/0/productRef/output","value":"query"}]'
wait_for 'restored selected port recovers composition' 120 composition_ready DependenciesReady True
kube delete dataproduct weather
wait_for 'producer deletion reaches consumer readiness' 120 composition_ready DependencyNotFound False
kube apply -f "$repo_root/docs/examples/composition.yaml"
wait_for 'producer recreation recovers composition' 120 composition_ready DependenciesReady True

kube set env deployment/dpc COMPOSITION_ENABLED=false
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
wait_for 'disabling composition invalidates required contracts after rollout' 120 composition_ready CompositionFeatureDisabled False
lineage_count=$(kube get dataproduct coastal-summary -o json | jq '.status.inputs // [] | length')
[[ "$lineage_count" == 0 ]] || {
	echo 'disabled composition retained lineage' >&2
	exit 1
}
echo 'PASS: disabled composition clears observed lineage'
kube delete -f "$repo_root/docs/examples/composition.yaml"
wait_for 'composition example deletion clears the registry' 120 probe --url http://dpc/api/v1/products --contains '"products":[]'
