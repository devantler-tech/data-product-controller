#!/usr/bin/env bash
# Sourced after engine-provider.sh in run.sh's owned ephemeral cluster. This is
# a synthetic observation fixture, not a Percona installation or MongoDB query proof.
: "${repo_root:?run through tests/source/run.sh}"
: "${test_dir:?run through tests/source/run.sh}"

# Reuse the engine module's bounded waits, rollouts, deletes and admission-error check.
engine_start_budget
document_started_at=$SECONDS
engine_product_file="$test_dir/document-product.json"
document_source_file="$test_dir/document-source.yaml"

# document_ready checks current product conditions and the actual registry projection together.
document_ready() {
	local status=$1 reason=$2
	kube get dataproduct document-product -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    ([.status.conditions[]? | select(.type == "Ready" or .type == "SourceReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation)] | length == 2) and
    any(.status.conditions[]?; .type == "SourceReady" and .reason == $reason)' >/dev/null &&
		registry_ready "$(if [[ "$status" == True ]]; then echo true; else echo false; fi)"
}

# document_publish creates synthetic fixture credentials and independently binds source ownership.
document_publish() {
	jq -n --arg uid "$1" '{apiVersion:"v1",kind:"Secret",
    metadata:{name:"documents-reader",namespace:"products",ownerReferences:[
      {apiVersion:"psmdb.percona.com/v1",kind:"PerconaServerMongoDB",name:"documents",uid:$uid}]},
    stringData:{password:"synthetic-document-password"}}' | kube apply -f - >/dev/null
}

# document_healthy supplies synthetic operator status; no database runs in this fixture.
document_healthy() {
	kube patch perconaservermongodb documents --subresource=status --type=merge \
		-p '{"status":{"state":"ready","size":3,"ready":3}}' >/dev/null
}

# document_retained records external identities and rejects deletion or product adoption.
document_retained() {
	kube get perconaservermongodb/documents secret/documents-reader -o json | jq -ceS --arg product_uid "$1" '
    if (.items | length) == 2 and all(.items[];
      .metadata.uid != null and .metadata.uid != "" and .metadata.deletionTimestamp == null and
      all(.metadata.ownerReferences[]?; .uid != $product_uid))
    then [.items[] | {key: (.kind + "/" + .metadata.name), value: .metadata.uid}] | from_entries
    else error("Document source and publication must remain independently owned") end'
}

document_existing_crd=$(kubectl --request-timeout=15s get crd perconaservermongodbs.psmdb.percona.com --ignore-not-found -o name)
[[ -z "$document_existing_crd" ]] || {
	echo 'Document fixture requires an absent Percona API' >&2
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
  name: perconaservermongodbs.psmdb.percona.com
  labels:
    data.devantler.tech/test-fixture: document-provider
spec:
  group: psmdb.percona.com
  names:
    kind: PerconaServerMongoDB
    plural: perconaservermongodbs
    singular: perconaservermongodb
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
kubectl --request-timeout=0 wait crd/perconaservermongodbs.psmdb.percona.com --for=condition=Established --timeout=30s
document_crd_uid=$(kubectl --request-timeout=15s get crd perconaservermongodbs.psmdb.percona.com -o jsonpath='{.metadata.uid}')
cat >"$document_source_file" <<'YAML'
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: documents
  namespace: products
spec:
  crVersion: 1.23.0
  replsets:
    - name: rs0
      size: 3
  users:
    - name: catalog-reader
      db: admin
      passwordSecretRef:
        name: documents-reader
        key: password
      roles:
        - name: read
          db: catalog
YAML
yq -o=json "$repo_root/docs/examples/document-provider-product.yaml" >"$engine_product_file"
kube apply --dry-run=server -f "$engine_product_file" >/dev/null
engine_reject 'Document with SQL adapter' '.spec.source.adapter="cnpg/v1"'
engine_reject 'Document with wrong resource API' '.spec.source.resourceRef.apiVersion="psmdb.percona.com/v2"'
engine_reject 'Document with wrong kind' '.spec.source.resourceRef.kind="Cluster"'
engine_reject 'Document without typed selection' 'del(.spec.source.engine)'
engine_reject 'Document hybrid strategy' '.spec.source.engine.provider="cnpg-hybrid"'
engine_reject 'Document system password publication' '.spec.source.connectionSecretRef.name="percona-server-mongodb-users"'

# First remove both grants and gates: disabled observation cannot be mistaken for permissions failure.
kube set env deployment/dpc PROVISIONED_SOURCES_ENABLED=false ENGINE_PROVIDERS_ENABLED=false >/dev/null
engine_rollout
kube apply -f "$engine_product_file" >/dev/null
engine_wait 'Document source gate defaults off' document_ready False SourceFeatureDisabled
kube set env deployment/dpc PROVISIONED_SOURCES_ENABLED=true >/dev/null
engine_rollout
engine_wait 'Document engine gate defaults off' document_ready False EngineProviderFeatureDisabled
kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=true >/dev/null
engine_rollout
engine_wait 'Document requires scoped observation access' document_ready False SourceAccessDenied
yq 'with(select(.kind == "RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' \
	"$repo_root/docs/examples/document-provider-observer-rbac.yaml" >"$test_dir/document-rbac.yaml"
kube apply -f "$test_dir/document-rbac.yaml" >/dev/null
engine_wait 'Document missing source is actionable' document_ready False SourceNotFound
kube apply -f "$document_source_file" >/dev/null
document_healthy
engine_wait 'Document waits for application password publication' document_ready False ConnectionNotPublished
document_source_uid=$(kube get perconaservermongodb documents -o jsonpath='{.metadata.uid}')
document_publish "$document_source_uid"
engine_wait 'Document readiness reaches the registry' document_ready True SourceReady

kube patch perconaservermongodb documents --subresource=status --type=merge -p '{"status":{"ready":2}}' >/dev/null
engine_wait 'Document replica loss withdraws readiness' document_ready False SourceNotReady
document_healthy
engine_wait 'Document replica recovery restores readiness' document_ready True SourceReady
kube patch perconaservermongodb documents --type=json -p '[{"op":"add","path":"/spec/users/0/roles/-","value":{"name":"root","db":"admin"}}]' >/dev/null
engine_wait 'Document privileged publication is rejected' document_ready False ConnectionPublicationUnsupported
kube apply -f "$document_source_file" >/dev/null
engine_wait 'Document application publication recovery' document_ready True SourceReady
engine_delete role documents-observer >/dev/null
engine_wait 'Document permission revocation withdraws readiness' document_ready False SourceAccessDenied
kube apply -f "$test_dir/document-rbac.yaml" >/dev/null
engine_wait 'Document restored permissions recover' document_ready True SourceReady

document_secret_uid=$(kube get secret documents-reader -o jsonpath='{.metadata.uid}')
engine_delete perconaservermongodb documents --cascade=orphan >/dev/null
engine_wait 'Document deletion removes source readiness' document_ready False SourceNotFound
kube apply -f "$document_source_file" >/dev/null
document_healthy
document_new_uid=$(kube get perconaservermongodb documents -o jsonpath='{.metadata.uid}')
[[ -n "$document_new_uid" && "$document_new_uid" != "$document_source_uid" ]] || {
	echo 'Document UID did not change' >&2
	exit 1
}
engine_wait 'Document recreation rejects the retained password owner' document_ready False ConnectionOwnerMismatch
document_publish "$document_new_uid"
engine_wait 'Document independent publisher rebinds ownership' document_ready True SourceReady
kube patch secret documents-reader --type=merge -p '{"stringData":{"password":"synthetic-document-rotated"}}' >/dev/null
[[ "$(kube get secret documents-reader -o jsonpath='{.metadata.uid}')" == "$document_secret_uid" ]] || {
	echo 'Document rotation replaced the publication' >&2
	exit 1
}
document_ready True SourceReady

kube set env deployment/dpc ENGINE_PROVIDERS_ENABLED=false >/dev/null
engine_rollout
engine_wait 'Document rollback withdraws readiness' document_ready False EngineProviderFeatureDisabled
document_product_uid=$(kube get dataproduct document-product -o jsonpath='{.metadata.uid}')
document_uids=$(document_retained "$document_product_uid")
engine_delete dataproduct document-product >/dev/null
engine_wait 'Document deletion removes the registry entry' probe --url http://dpc/api/v1/products --contains '"products":[]'
[[ "$(document_retained "$document_product_uid")" == "$document_uids" ]] || {
	echo 'Document deletion changed external resources' >&2
	exit 1
}
engine_delete -f "$test_dir/document-rbac.yaml" >/dev/null
engine_delete perconaservermongodb documents --cascade=orphan >/dev/null
engine_delete secret documents-reader >/dev/null
kubectl --request-timeout=15s get crd perconaservermongodbs.psmdb.percona.com -o json |
	jq -e --arg uid "$document_crd_uid" '.metadata.uid == $uid and
    .metadata.labels["data.devantler.tech/test-fixture"] == "document-provider"' >/dev/null || {
	echo 'Document CRD ownership changed before cleanup' >&2
	exit 1
}
engine_delete crd perconaservermongodbs.psmdb.percona.com >/dev/null
engine_remaining >/dev/null
echo "PASS: synthetic Document lifecycle ($((SECONDS - document_started_at)) seconds); real Percona/MongoDB acceptance remains separate"
