#!/usr/bin/env bash
# Synthetic released API status in run.sh's owned cluster, not a database/operator installation.
: "${repo_root:?run through tests/source/run.sh}"
: "${test_dir:?run through tests/source/run.sh}"
engine_start_budget
graph_started_at=$SECONDS
engine_product_file="$test_dir/graph-product.json"
graph_source_file="$test_dir/graph-source.json"
graph_fixture="$repo_root/tests/source/fixtures/arango-deployment.json"

graph_ready() {
	local status=$1 reason=$2
	kube get dataproduct graph-product -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    ([.status.conditions[]? | select(.type == "Ready" or .type == "SourceReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation)] | length == 2) and
    any(.status.conditions[]?; .type == "SourceReady" and .reason == $reason)' >/dev/null &&
		registry_ready "$(if [[ "$status" == True ]]; then echo true; else echo false; fi)" graph-product
}

graph_publish() {
	jq -n --arg uid "$1" '{apiVersion:"v1",kind:"Secret",metadata:{name:"lineage-reader",namespace:"products",
    ownerReferences:[{apiVersion:"database.arangodb.com/v1",kind:"ArangoDeployment",name:"lineage",uid:$uid}],
    annotations:{"data.devantler.tech/arango-publication":"v1","data.devantler.tech/arango-user":"catalog-reader",
      "data.devantler.tech/arango-database":"catalog","data.devantler.tech/arango-graph":"lineage",
      "data.devantler.tech/arango-access":"read-only","data.devantler.tech/arango-collections":"products,relations"}},
    stringData:{password:"synthetic-graph-password"}}' | kube apply -f - >/dev/null
}

graph_healthy() {
	jq '{status}' "$graph_fixture" >"$test_dir/graph-status.json"
	kube patch arangodeployment lineage --subresource=status --type=merge --patch-file "$test_dir/graph-status.json" >/dev/null
}

graph_retained() {
	kube get arangodeployment/lineage secret/lineage-reader -o json | jq -ceS --arg product_uid "$1" '
    if (.items | length) == 2 and all(.items[];
      .metadata.uid != null and .metadata.uid != "" and .metadata.deletionTimestamp == null and
      all(.metadata.ownerReferences[]?; .uid != $product_uid))
    then [.items[] | {key:(.kind + "/" + .metadata.name),value:.metadata.uid}] | from_entries
    else error("Graph source and publication must remain independently owned") end'
}

graph_existing_crd=$(kubectl --request-timeout=15s get crd arangodeployments.database.arangodb.com --ignore-not-found -o name)
[[ -z "$graph_existing_crd" ]] || {
	echo 'Graph fixture requires an absent ArangoDB API' >&2
	exit 1
}
[[ "$(kube get dataproducts -o json | jq '.items | length')" == 0 ]] || {
	echo 'preceding products must be removed' >&2
	exit 1
}
cat <<'YAML' | kubectl --request-timeout=15s apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: arangodeployments.database.arangodb.com
  labels:
    data.devantler.tech/test-fixture: graph-provider
spec:
  group: database.arangodb.com
  names:
    kind: ArangoDeployment
    plural: arangodeployments
    singular: arangodeployment
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      subresources:
        status: {}
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              x-kubernetes-preserve-unknown-fields: true
            status:
              type: object
              x-kubernetes-preserve-unknown-fields: true
YAML
kubectl --request-timeout=0 wait crd/arangodeployments.database.arangodb.com --for=condition=Established --timeout=30s
graph_crd_uid=$(kubectl --request-timeout=15s get crd arangodeployments.database.arangodb.com -o jsonpath='{.metadata.uid}')
jq 'del(.metadata.uid,.metadata.generation,.status)' "$graph_fixture" >"$graph_source_file"
yq -o=json "$repo_root/docs/examples/graph-provider-product.yaml" >"$engine_product_file"
kube apply --dry-run=server -f "$engine_product_file" >/dev/null
engine_reject 'Graph with SQL adapter' '.spec.source.adapter="cnpg/v1"'
engine_reject 'Graph with wrong API' '.spec.source.resourceRef.apiVersion="database.arangodb.com/v2"'
engine_reject 'Graph with wrong kind' '.spec.source.resourceRef.kind="Cluster"'
engine_reject 'Graph without engine selection' 'del(.spec.source.engine)'
engine_reject 'Graph hybrid strategy' '.spec.source.engine.provider="cnpg-hybrid"'

kube set env deployment/dpc PROVISIONED_SOURCES_ENABLED=false ENGINE_PROVIDERS_ENABLED=false >/dev/null
engine_rollout
kube apply -f "$engine_product_file" >/dev/null
engine_wait 'Graph source gate defaults off' graph_ready False SourceFeatureDisabled
kube set env deployment/dpc PROVISIONED_SOURCES_ENABLED=true >/dev/null
engine_rollout
engine_wait 'Graph engine gate defaults off' graph_ready False EngineProviderFeatureDisabled
kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=true >/dev/null
engine_rollout
engine_wait 'Graph needs scoped observation access' graph_ready False SourceAccessDenied
yq 'with(select(.kind == "RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' \
	"$repo_root/docs/examples/graph-provider-observer-rbac.yaml" >"$test_dir/graph-rbac.yaml"
kube apply -f "$test_dir/graph-rbac.yaml" >/dev/null
engine_wait 'Graph missing source is actionable' graph_ready False SourceNotFound
kube apply -f "$graph_source_file" >/dev/null
graph_healthy
engine_wait 'Graph waits for application publication' graph_ready False ConnectionNotPublished
graph_source_uid=$(kube get arangodeployment lineage -o jsonpath='{.metadata.uid}')
graph_publish "$graph_source_uid"
engine_wait 'Graph readiness reaches the registry' graph_ready True SourceReady

kube patch arangodeployment lineage --type=merge -p '{"spec":{"downtimeAllowed":true}}' >/dev/null
engine_wait 'Graph spec change rejects retained ready status' graph_ready False SourceNotReady
# Independently frozen checksum for the same raw spec with downtimeAllowed=true.
kube patch arangodeployment lineage --subresource=status --type=merge \
	-p '{"status":{"acceptedSpecVersion":"f45d239468706d156b1d80f84a966f9eac1ea15c72c07a59c81cfc2b3d0602b9","appliedVersion":"f45d239468706d156b1d80f84a966f9eac1ea15c72c07a59c81cfc2b3d0602b9"}}' >/dev/null
engine_wait 'Graph applied current spec recovers readiness' graph_ready True SourceReady
engine_delete role lineage-observer >/dev/null
engine_wait 'Graph revoked permissions withdraw readiness' graph_ready False SourceAccessDenied
kube apply -f "$test_dir/graph-rbac.yaml" >/dev/null
engine_wait 'Graph restored permissions recover' graph_ready True SourceReady

graph_secret_uid=$(kube get secret lineage-reader -o jsonpath='{.metadata.uid}')
engine_delete arangodeployment lineage --cascade=orphan >/dev/null
kube apply -f "$graph_source_file" >/dev/null
graph_healthy
graph_new_uid=$(kube get arangodeployment lineage -o jsonpath='{.metadata.uid}')
[[ -n "$graph_new_uid" && "$graph_new_uid" != "$graph_source_uid" ]] || {
	echo 'Graph UID did not change' >&2
	exit 1
}
engine_wait 'Graph recreation rejects retained publication ownership' graph_ready False ConnectionOwnerMismatch
graph_publish "$graph_new_uid"
engine_wait 'Graph independent publisher rebinds ownership' graph_ready True SourceReady
kube patch secret lineage-reader --type=merge -p '{"stringData":{"password":"synthetic-graph-rotated"}}' >/dev/null
[[ "$(kube get secret lineage-reader -o jsonpath='{.metadata.uid}')" == "$graph_secret_uid" ]] || {
	echo 'Graph rotation replaced publication' >&2
	exit 1
}
graph_ready True SourceReady

kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=false >/dev/null
engine_rollout
engine_wait 'Graph rollback withdraws readiness' graph_ready False EngineProviderFeatureDisabled
graph_product_uid=$(kube get dataproduct graph-product -o jsonpath='{.metadata.uid}')
graph_uids=$(graph_retained "$graph_product_uid")
engine_delete dataproduct graph-product >/dev/null
engine_wait 'Graph deletion removes the registry entry' probe --url http://dpc/api/v1/products --contains '"products":[]'
[[ "$(graph_retained "$graph_product_uid")" == "$graph_uids" ]] || {
	echo 'Graph deletion changed external resources' >&2
	exit 1
}
engine_delete -f "$test_dir/graph-rbac.yaml" >/dev/null
engine_delete arangodeployment lineage --cascade=orphan >/dev/null
engine_delete secret lineage-reader >/dev/null
kubectl --request-timeout=15s get crd arangodeployments.database.arangodb.com -o json |
	jq -e --arg uid "$graph_crd_uid" '.metadata.uid == $uid and .metadata.labels["data.devantler.tech/test-fixture"] == "graph-provider"' >/dev/null || {
	echo 'Graph CRD ownership changed before cleanup' >&2
	exit 1
}
engine_delete crd arangodeployments.database.arangodb.com >/dev/null
engine_remaining >/dev/null
echo "PASS: synthetic Graph lifecycle ($((SECONDS - graph_started_at)) seconds); real ArangoDB/AQL acceptance remains separate"
