#!/usr/bin/env bash
# Sourced inside run.sh's owned ephemeral cluster. This is an observation-contract
# fixture, not a CloudNativePG operator installation or PostgreSQL availability proof.
: "${repo_root:?run through tests/source/run.sh}"
: "${test_dir:?run through tests/source/run.sh}"

engine_started_at=$SECONDS
engine_deadline=$((SECONDS + 480))
engine_product_file="$test_dir/engine-product.json"
engine_cluster_file="$test_dir/engine-cluster.yaml"

engine_remaining() {
	local remaining=$((engine_deadline - SECONDS))
	if ((remaining <= 0)); then
		echo 'engine-provider acceptance exceeded its eight-minute bound' >&2
		return 1
	fi
	printf '%s\n' "$remaining"
}

engine_wait() {
	local description=$1 remaining
	shift
	remaining=$(engine_remaining)
	((remaining <= 45)) || remaining=45
	wait_for "$description" "$remaining" "$@"
}

engine_rollout() {
	local remaining
	remaining=$(engine_remaining)
	((remaining <= 60)) || remaining=60
	kube --request-timeout=0 rollout status deployment/dpc --timeout="${remaining}s"
}

engine_ready() {
	local status=$1 reason=$2
	kube get dataproduct engine-warehouse -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    ([.status.conditions[]? | select(.type == "Ready" or .type == "SourceReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation)] | length == 2) and
    any(.status.conditions[]?; .type == "SourceReady" and .reason == $reason)' >/dev/null &&
		probe --url http://dpc/api/v1/products --contains "\"ready\":$(if [[ "$status" == True ]]; then echo true; else echo false; fi)"
}

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

engine_publish_secret() {
	local cluster_uid=$1
	jq -n --arg uid "$cluster_uid" '{apiVersion:"v1",kind:"Secret",
    metadata:{name:"warehouse-app",namespace:"products",ownerReferences:[
      {apiVersion:"postgresql.cnpg.io/v1",kind:"Cluster",name:"warehouse",uid:$uid}]},
    type:"kubernetes.io/basic-auth",stringData:{username:"app",password:"synthetic-engine-password"}}' |
		kube apply -f - >/dev/null
}

engine_healthy_status() {
	kube patch cluster.postgresql.cnpg.io warehouse --subresource=status --type=merge -p '{"status":{
    "instances":1,"readyInstances":1,"currentPrimary":"warehouse-1","targetPrimary":"warehouse-1",
    "conditions":[{"type":"Ready","status":"True","reason":"SyntheticReady","message":"Observation fixture."}]}}' >/dev/null
}

engine_retained_uids() {
	local product_uid=$1
	kube get cluster.postgresql.cnpg.io/warehouse secret/warehouse-app -o json |
		jq -ceS --arg product_uid "$product_uid" '
    if (.items | length) == 2 and all(.items[];
      .metadata.uid != null and .metadata.uid != "" and .metadata.deletionTimestamp == null and
      all(.metadata.ownerReferences[]?; .uid != $product_uid))
    then [.items[] | {key: (.kind + "/" + .metadata.name), value: .metadata.uid}] | from_entries
    else error("provider and connection publication must remain independently owned") end'
}

# The fixture owns this API only in the disposable cluster. Never replace an
# installed operator's CRD, even when this script is reused by another harness.
engine_existing_crd=$(kubectl --request-timeout=15s get crd clusters.postgresql.cnpg.io --ignore-not-found -o name)
[[ -z "$engine_existing_crd" ]] || {
	echo 'engine observation fixture requires an absent CloudNativePG Cluster API' >&2
	exit 1
}
[[ "$(kube get dataproducts -o json | jq '.items | length')" == 0 ]] || {
	echo 'engine acceptance requires the preceding examples to have been removed' >&2
	exit 1
}
cat <<'YAML' | kubectl --request-timeout=15s apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusters.postgresql.cnpg.io
  labels:
    data.devantler.tech/test-fixture: engine-provider
spec:
  group: postgresql.cnpg.io
  names:
    kind: Cluster
    plural: clusters
    singular: cluster
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
kubectl --request-timeout=0 wait crd/clusters.postgresql.cnpg.io --for=condition=Established --timeout=30s
engine_crd_uid=$(kubectl --request-timeout=15s get crd clusters.postgresql.cnpg.io -o jsonpath='{.metadata.uid}')
cat >"$engine_cluster_file" <<'YAML'
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: warehouse
  namespace: products
spec:
  instances: 1
YAML
cat >"$engine_product_file" <<'JSON'
{
  "apiVersion": "data.devantler.tech/v1alpha1",
  "kind": "DataProduct",
  "metadata": {"name": "engine-warehouse", "namespace": "products"},
  "spec": {
    "id": "urn:example:engine-warehouse",
    "name": "Engine warehouse",
    "description": "Synthetic SQL provider observation acceptance.",
    "version": "v1.0.0",
    "owner": {"name": "Integration fixture"},
    "source": {
      "adapter": "cnpg/v1",
      "engine": {"apiVersion": "engine-provider/v1", "type": "sql", "provider": "native"},
      "resourceRef": {"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "name": "warehouse"},
      "connectionSecretRef": {"name": "warehouse-app"}
    },
    "outputs": [{"name": "query", "protocol": "OpenAPI", "url": "https://warehouse.example.com/query",
      "contractUrl": "https://warehouse.example.com/openapi.json"}]
  }
}
JSON
kube apply --dry-run=server -f "$engine_product_file" >/dev/null
jq 'del(.spec.source.engine) | .spec.source.adapter="crossplane/v1" |
  .spec.source.resourceRef.apiVersion="database.example.org/v1alpha1" |
  .spec.source.resourceRef.kind="Database" | .spec.source.connectionSecretRef.name="warehouse-connection"' \
	"$engine_product_file" | kube apply --dry-run=server -f - >/dev/null
echo 'PASS: engine admission accepts SQL/native and legacy Crossplane references'
engine_reject 'unimplemented graph/native' '.spec.source.engine.type="graph"'
engine_reject 'unimplemented document/hybrid' '.spec.source.engine.type="document" | .spec.source.engine.provider="cnpg-hybrid"'
engine_reject 'unversioned engine selection' '.spec.source.engine.apiVersion="engine-provider/v2"'
engine_reject 'CNPG without typed selection' 'del(.spec.source.engine)'
engine_reject 'typed selection through another adapter' '.spec.source.adapter="crossplane/v1"'
engine_reject 'wrong provider API' '.spec.source.resourceRef.apiVersion="postgresql.cnpg.io/v2"'
engine_reject 'wrong provider kind' '.spec.source.resourceRef.kind="Database"'
engine_reject 'another connection publication' '.spec.source.connectionSecretRef.name="warehouse-superuser"'

kube apply -f "$engine_cluster_file" >/dev/null
engine_healthy_status
engine_cluster_uid=$(kube get cluster.postgresql.cnpg.io warehouse -o jsonpath='{.metadata.uid}')
engine_publish_secret "$engine_cluster_uid"
kube apply -f "$engine_product_file" >/dev/null
engine_wait 'typed sources retain the default-off provisioned-source gate' engine_ready False SourceFeatureDisabled
install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true \
	--set provisionedSources.enabled=true
engine_rollout
engine_wait 'typed providers retain their independent default-off gate' engine_ready False EngineProviderFeatureDisabled
kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=true >/dev/null
engine_rollout
engine_wait 'enabled providers require their exact-resource grant' engine_ready False SourceAccessDenied
cat >"$test_dir/engine-observer-rbac.yaml" <<'YAML'
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: engine-source-observer
  namespace: products
rules:
  - apiGroups: [postgresql.cnpg.io]
    resources: [clusters]
    resourceNames: [warehouse]
    verbs: [get]
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [warehouse-app]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: engine-source-observer
  namespace: products
subjects:
  - kind: ServiceAccount
    name: dpc
    namespace: products
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: engine-source-observer
YAML
kube apply -f "$test_dir/engine-observer-rbac.yaml" >/dev/null
engine_wait 'scoped provider readiness reaches product and registry' engine_ready True SourceReady
probe --url http://dpc/api/v1/products --contains '"id":"urn:example:engine-warehouse"'

# No product update or controller restart: ordinary polling observes external changes.
kube patch cluster.postgresql.cnpg.io warehouse --subresource=status --type=merge \
	-p '{"status":{"readyInstances":0}}' >/dev/null
engine_wait 'provider readiness loss reaches product and registry' engine_ready False SourceNotReady
engine_healthy_status
engine_wait 'provider readiness recovery reaches product and registry' engine_ready True SourceReady
kube delete role engine-source-observer >/dev/null
engine_wait 'provider permission revocation is observed without a restart' engine_ready False SourceAccessDenied
kube apply -f "$test_dir/engine-observer-rbac.yaml" >/dev/null

# Orphaning retains the publication UID while removing its old owner. A recreated
# source cannot satisfy ownership until its independent publisher binds it again.
engine_secret_uid=$(kube get secret warehouse-app -o jsonpath='{.metadata.uid}')
kube delete cluster.postgresql.cnpg.io warehouse --cascade=orphan --wait=true --timeout=20s >/dev/null
engine_wait 'restored access observes source deletion in product and registry' engine_ready False SourceNotFound
kube apply -f "$engine_cluster_file" >/dev/null
engine_healthy_status
engine_recreated_uid=$(kube get cluster.postgresql.cnpg.io warehouse -o jsonpath='{.metadata.uid}')
[[ -n "$engine_recreated_uid" && "$engine_recreated_uid" != "$engine_cluster_uid" ]] || {
	echo 'source recreation did not produce a new UID' >&2
	exit 1
}
engine_wait 'retained credentials cannot satisfy recreated-source ownership' engine_ready False ConnectionOwnerMismatch
[[ "$(kube get secret warehouse-app -o jsonpath='{.metadata.uid}')" == "$engine_secret_uid" ]] || {
	echo 'orphaned connection publication was unexpectedly replaced' >&2
	exit 1
}
engine_publish_secret "$engine_recreated_uid"
engine_wait 'rebinding the independent publication restores readiness' engine_ready True SourceReady
kube patch secret warehouse-app --type=merge -p '{"stringData":{"password":"synthetic-engine-rotated"}}' >/dev/null
[[ "$(kube get secret warehouse-app -o jsonpath='{.metadata.uid}')" == "$engine_secret_uid" ]] || {
	echo 'credential rotation replaced its publication' >&2
	exit 1
}
engine_ready True SourceReady
echo 'PASS: credential rotation preserves connection publication identity'

kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=false >/dev/null
engine_rollout
engine_wait 'disabling the provider gate withdraws product readiness' engine_ready False EngineProviderFeatureDisabled
engine_product_uid=$(kube get dataproduct engine-warehouse -o jsonpath='{.metadata.uid}')
engine_retained=$(engine_retained_uids "$engine_product_uid")
kube delete dataproduct engine-warehouse >/dev/null
engine_wait 'deleting the typed product removes its registry entry' probe --url http://dpc/api/v1/products --contains '"products":[]'
[[ "$(engine_retained_uids "$engine_product_uid")" == "$engine_retained" ]] || {
	echo 'product deletion changed external provider or publication ownership' >&2
	exit 1
}
echo 'PASS: typed product deletion retains provider and publication UIDs'

kube delete -f "$test_dir/engine-observer-rbac.yaml" >/dev/null
kube delete cluster.postgresql.cnpg.io warehouse --cascade=orphan --wait=true --timeout=20s >/dev/null
kube delete secret warehouse-app >/dev/null
kubectl --request-timeout=15s get crd clusters.postgresql.cnpg.io -o json |
	jq -e --arg uid "$engine_crd_uid" '.metadata.uid == $uid and
    .metadata.labels["data.devantler.tech/test-fixture"] == "engine-provider"' >/dev/null || {
	echo 'engine fixture CRD ownership changed before cleanup' >&2
	exit 1
}
kubectl --request-timeout=15s delete crd clusters.postgresql.cnpg.io --wait=true --timeout=30s >/dev/null
engine_remaining >/dev/null
echo "PASS: synthetic engine admission and observation lifecycle ($((SECONDS - engine_started_at)) seconds); real CNPG acceptance remains separate"
