#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
for command in ksail docker kubectl helm jq yq openssl timeout curl cosign; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done
[[ $(uname -m) == x86_64 ]] || {
	echo 'ArangoDB acceptance requires an amd64 runner' >&2
	exit 1
}
provider_name=arango
product_name=graph-product
product_id=urn:example:lineage
consumer_pod=graph-consumer
source_api_group=database.arangodb.com
source_resource=arangodeployments
source "$repo_root/tests/provider/common.sh"

# database_ready requires the real operator to report its initialized Single member ready.
database_ready() {
	kube get arangodeployment lineage -o json | jq -e '
		.status.phase == "Running" and
		any(.status.conditions[]?; .type == "BootstrapCompleted" and .status == "True") and
		any(.status.conditions[]?; .type == "BootstrapSucceded" and .status == "True") and
    (.status.members.single | length == 1) and
    any(.status.conditions[]?; .type == "Ready" and .status == "True") and
    any(.status.members.single[0].conditions[]?; .type == "Ready" and .status == "True")' >/dev/null
}
# bind_publication binds the independent reader publication to the current source UID.
bind_publication() {
	kube patch secret lineage-reader --type=merge -p \
		"$(jq -nc --arg uid "$1" '{metadata:{ownerReferences:[{apiVersion:"database.arangodb.com/v1",kind:"ArangoDeployment",name:"lineage",uid:$uid}]}}')" >/dev/null
}
# retained_identities captures live source, credential and storage identities without secret data.
retained_identities() {
	kube get arangodeployment,secret,pvc -o json | jq -ceS '[.items[] | . as $resource |
    select(.kind == "PersistentVolumeClaim" or
      (.kind == "ArangoDeployment" and .metadata.name == "lineage") or
      (.kind == "Secret" and (["lineage-reader","lineage-ca","lineage-jwt","lineage-root-password"] | index($resource.metadata.name)))) |
    {kind,name:.metadata.name,uid:.metadata.uid,deleting:.metadata.deletionTimestamp}] |
    if length >= 6 and all(.[]; .uid != null and .deleting == null) then sort_by(.kind,.name) else error("retention incomplete") end'
}
# apply_workloads applies the restricted query, writer and consumer fixtures after separate bootstrap.
apply_workloads() {
	export DPC_PROVIDER_FIXTURE_IMAGE="$fixture_image"
	# Reuse the restricted, health-probed workload template, replacing its public
	# TLS trust and independent password projections. No database server key is mounted.
	yq '(.metadata.name |= sub("document"; "graph")) |
      with(select(.kind == "Deployment");
        .spec.selector.matchLabels.app="graph-query" |
        .spec.template.metadata.labels.app="graph-query" |
        .spec.template.spec.containers[0].image=strenv(DPC_PROVIDER_FIXTURE_IMAGE) |
        .spec.template.spec.containers[0].env=[{"name":"PROVIDER_ENGINE","value":"graph"}] |
        .spec.template.spec.containers[0].volumeMounts |= map(select(.name != "database-client-tls")) |
        .spec.template.spec.volumes |= map(select(.name != "database-client-tls"))) |
      with(select(.kind == "Service"); .spec.selector.app="graph-query") |
      with(select(.kind == "Pod");
        .metadata.labels.app |= sub("document";"graph") |
        .spec.containers[0].image=strenv(DPC_PROVIDER_FIXTURE_IMAGE) |
        .spec.containers[0].env=[{"name":"PROVIDER_ENGINE","value":"graph"}] |
        .spec.containers[0].volumeMounts |= map(select(.name != "database-client-tls")) |
        .spec.volumes |= map(select(.name != "database-client-tls"))) |
      (.. | select(has("secretName")) | .secretName) |=
        sub("documents-stale-reader";"lineage-stale-reader") |
      (.. | select(has("secretName")) | .secretName) |=
        sub("documents-reader";"lineage-reader") |
      (.. | select(has("secretName")) | .secretName) |=
        sub("documents-writer";"lineage-writer") |
      (.. | select(has("secretName")) | .secretName) |=
        sub("documents-ssl";"lineage-ca") |
      (.. | select(has("secretName")) | .secretName) |=
        sub("document-query-tls";"graph-query-tls")' "$repo_root/tests/provider/workloads.yaml" >"$test_dir/graph-workloads.yaml"
	# This Pod has no Service or incoming traffic. Only it receives the bootstrap
	# password; the query and writer never receive the root or JWT credentials.
	yq 'select(.kind == "Pod" and .metadata.name == "graph-writer") |
      .metadata.name="graph-bootstrap" | .spec.containers[0].name="bootstrap" |
      .spec.volumes[0].secret.secretName="lineage-reader" |
      .spec.volumes += [{"name":"writer-password","secret":{"secretName":"lineage-writer"}},
        {"name":"root-password","secret":{"secretName":"lineage-root-password"}}] |
      .spec.containers[0].volumeMounts += [{"name":"writer-password","mountPath":"/writer-password","readOnly":true},
        {"name":"root-password","mountPath":"/root-password","readOnly":true}]' \
		"$test_dir/graph-workloads.yaml" >"$test_dir/graph-bootstrap.yaml"
	kube apply -f "$test_dir/graph-workloads.yaml" -f "$test_dir/graph-bootstrap.yaml" >/dev/null
}

start_cluster
bounded curl --fail --location --retry 3 --max-time 120 --output "$test_dir/arango.tgz" \
	https://github.com/arangodb/kube-arangodb/releases/download/1.4.5/kube-arangodb-1.4.5.tgz
printf '%s  %s\n' db2d3a42e4f5970fecf9d8e0500a8ac3eb8369f0cab63617c1310f5892d70a85 "$test_dir/arango.tgz" | sha256sum --check -
bounded helm upgrade --install arango "$test_dir/arango.tgz" --namespace products \
	--set operator.scope=namespaced \
	--set operator.image=arangodb/kube-arangodb:1.4.5@sha256:f579e339ab083998f648351293a5d55dd81b8a96849b0bc8f03379f65610cfa7 \
	--set operator.resources.requests.cpu=100m --set operator.resources.limits.cpu=500m
bounded kubectl --request-timeout=0 wait crd/arangodeployments.database.arangodb.com --for=condition=Established --timeout="$(remaining)s"

mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -sha256 -subj /CN=disposable-graph-ca \
	-keyout "$test_dir/tls/ca.key" -out "$test_dir/tls/ca.crt" >/dev/null 2>&1
kube create secret generic lineage-ca --from-file=ca.crt="$test_dir/tls/ca.crt" --from-file=ca.key="$test_dir/tls/ca.key" >/dev/null
make_certificate graph-query 'DNS:graph-query.products.svc.cluster.local'
kube create secret generic graph-query-tls --from-file=ca.crt="$test_dir/tls/ca.crt" \
	--from-file=tls.crt="$test_dir/tls/graph-query.crt" --from-file=tls.key="$test_dir/tls/graph-query.key" >/dev/null
openssl rand -hex 32 >"$test_dir/jwt-token"
kube create secret generic lineage-jwt --from-file=token="$test_dir/jwt-token" >/dev/null
for identity in reader writer root; do
	printf 'synthetic-graph-%s-original' "$identity" >"$test_dir/$identity-password"
	name="lineage-$identity"
	[[ $identity != root ]] || name=lineage-root-password
	kube create secret generic "$name" --from-file=password="$test_dir/$identity-password" >/dev/null
done
kube create secret generic lineage-stale-reader --from-file=password="$test_dir/reader-password" >/dev/null
kube annotate secret lineage-reader data.devantler.tech/arango-publication=v1 \
	data.devantler.tech/arango-user=catalog-reader data.devantler.tech/arango-database=catalog \
	data.devantler.tech/arango-graph=lineage data.devantler.tech/arango-access=read-only \
	data.devantler.tech/arango-collections=products,relations >/dev/null
kube apply -f "$repo_root/tests/provider/arango.yaml" >/dev/null
yq '(.. | select(has("app")) | .app) |= sub("document";"graph") |
  .metadata.name |= sub("document";"graph") |
  (.. | select(has("app.kubernetes.io/name")) | ."app.kubernetes.io/name")="kube-arangodb" |
  (.. | select(has("port") and .port == 27017) | .port) = 8529' \
	"$repo_root/tests/provider/network-policy.yaml" | kube apply -f - >/dev/null

phase 'real Graph operator startup and query' 600
server_digest=$(platform_digest 'arangodb@sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf' "$test_dir/server-index.json")
operator_digest=$(platform_digest 'arangodb/kube-arangodb@sha256:f579e339ab083998f648351293a5d55dd81b8a96849b0bc8f03379f65610cfa7' "$test_dir/operator-index.json")
bounded kubectl --request-timeout=0 -n products rollout status deployment/arango-arango-operator --timeout="$(remaining)s"
wait_for 'real operator creates a ready authenticated Single server' database_ready
kube get arangodeployment lineage -o json | jq '{uid:.metadata.uid,phase:.status.phase,accepted:.status.acceptedSpecVersion,applied:.status.appliedVersion,currentImage:.status."current-image"}'
kube get pods -o json | jq '[.items[] | {name:.metadata.name,uid:.metadata.uid,images:[.status.containerStatuses[]? | {name,imageID}]}]'
kubectl --request-timeout=15s get node "$control_node" -o json | jq -e \
	'.status.nodeInfo.architecture == "amd64" and .status.nodeInfo.operatingSystem == "linux"' >/dev/null
require_pinned_image app=graph-database server \
	'docker.io/library/arangodb@sha256:4bc086d5050ca7ea11c6d00a36d8b910c838bb54ad553f8c1b715769d3499bcf' "$server_digest"
require_pinned_image app.kubernetes.io/name=kube-arangodb operator \
	'arangodb/kube-arangodb:1.4.5@sha256:f579e339ab083998f648351293a5d55dd81b8a96849b0bc8f03379f65610cfa7' "$operator_digest"
kubectl --request-timeout=15s version -o json | jq '.serverVersion'
apply_workloads
bounded kubectl --request-timeout=0 -n products rollout status deployment/graph-query --timeout="$(remaining)s"
bounded kubectl --request-timeout=0 -n products wait pod/graph-writer pod/graph-bootstrap pod/graph-consumer --for=condition=Ready --timeout="$(remaining)s"
bounded kubectl --request-timeout=0 -n products exec graph-bootstrap -- /fixture bootstrap
bounded kubectl --request-timeout=0 -n products exec graph-writer -- /fixture seed
bounded kubectl --request-timeout=0 -n products exec graph-writer -- /fixture writer-check
wait_for 'verified HTTPS returns the persistent two-hop traversal' query
query contract
bounded kubectl --request-timeout=0 -n products exec deployment/graph-query -- /fixture privileges
bounded kubectl --request-timeout=0 -n products exec graph-writer -- /fixture writer-check
echo 'PASS: application reads real lineage but cannot mutate records, administer users or access other databases'
kube label pod graph-consumer app=unapproved --overwrite >/dev/null
wait_for 'unauthorized consumer has a network timeout and no HTTP response' query denied
kube label pod graph-consumer app=graph-consumer --overwrite >/dev/null
wait_for 'restored consumer identity restores the same verified query' query

phase 'released regression control and current publication' 240
install_controller ghcr.io/devantler-tech/data-product-controller 1.14.0 "${released_image##*@}"
yq 'with(select(.kind == "RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' \
	"$repo_root/docs/examples/graph-provider-observer-rbac.yaml" | kube apply -f - >/dev/null
yq '.spec.outputs[0].url="https://graph-query.products.svc.cluster.local:8443/api/lineage" |
  .spec.outputs[0].contractUrl="https://graph-query.products.svc.cluster.local:8443/openapi.json"' \
	"$repo_root/docs/examples/graph-provider-product.yaml" | kube apply -f - >/dev/null
wait_for 'released v1.14.0 reproduces the immutable-image rejection' product_ready False SourceVersionUnsupported
install_controller dpc-provider "$cluster_name" ''
wait_for 'current controller rejects missing publication ownership' product_ready False ConnectionOwnerMismatch
source_uid=$(kube get arangodeployment lineage -o jsonpath='{.metadata.uid}')
bind_publication "$source_uid"
wait_for 'current UID and real immutable profile publish source and registry readiness' product_ready True SourceReady
query
[[ $(audit_reads) -gt 0 ]] || {
	echo 'API audit observation produced no data' >&2
	exit 1
}
query_pod_uid=$(kube get pod -l app=graph-query -o jsonpath='{.items[0].metadata.uid}')
secret_uid=$(kube get secret lineage-reader -o jsonpath='{.metadata.uid}')

phase 'actual scheduling outage and recovery' 360
# An impossible selector withdraws the real server while retaining its 1-GiB
# resource/storage bounds. The operator supplies all readiness and spec hashes.
kube patch arangodeployment lineage --type=merge -p '{"spec":{"single":{"nodeSelector":{"data.devantler.tech/acceptance-node":"unavailable"}}}}' >/dev/null
wait_for 'actual operator update withdraws current readiness' product_ready False SourceNotReady
wait_for 'real query reports the missing server' query outage
kube patch arangodeployment lineage --type=merge -p '{"spec":{"single":{"nodeSelector":null}}}' >/dev/null
wait_for 'operator reconciles and restarts the retained source' database_ready
wait_for 'same two-hop lineage survives restart' query
wait_for 'operator current spec republishes readiness' product_ready True SourceReady

phase 'password rotation and retained source recreation' 600
printf 'synthetic-graph-reader-replacement' >"$test_dir/reader-password"
kube create secret generic lineage-reader --from-file=password="$test_dir/reader-password" --dry-run=client -o yaml | kube apply -f - >/dev/null
wait_for 'independent bootstrap publisher observes and installs the rotated projection' kube exec graph-bootstrap -- /fixture rotate
wait_for 'old password receives an actual authentication rejection' kube exec deployment/graph-query -- /fixture stale-password
wait_for 'same query workload uses the rotated password' query
[[ $(kube get secret lineage-reader -o jsonpath='{.metadata.uid}') == "$secret_uid" ]]
[[ $(kube get pod -l app=graph-query -o jsonpath='{.items[0].metadata.uid}') == "$query_pod_uid" ]]
product_ready True SourceReady
before_recreation=$(retained_identities)
kube get arangodeployment lineage -o json | jq -e '
  [.status.members.single[] | {id,persistentVolumeClaim,persistentVolumeClaimName}] as $members |
  if ($members | length == 1) and
    all($members[]; (.id | type == "string" and length > 0) and
      ((.persistentVolumeClaim.name // .persistentVolumeClaimName) | type == "string" and length > 0))
  then {status:{members:{single:$members}}} else error("incomplete retained member observation") end' >"$test_dir/recovery-members.json"
kube get pods,arangomembers -l arango_deployment=lineage -o json >"$test_dir/old-members.json"
jq -e '.items | length > 0 and any(.[]; .kind == "Pod") and
  all(.[]; (.kind == "Pod" or .kind == "ArangoMember") and .metadata.namespace == "products" and
    .metadata.labels.arango_deployment == "lineage" and .metadata.uid != null)' "$test_dir/old-members.json" >/dev/null
bounded kubectl --request-timeout=0 -n products delete arangodeployment lineage --cascade=orphan --timeout="$(remaining)s"
# Remove only recorded orphaned runtime objects. The upstream recovery procedure
# reuses observed member IDs and PVC names; it supplies no fabricated ready status.
bounded kubectl --request-timeout=0 -n products delete -f "$test_dir/old-members.json" --timeout="$(remaining)s"
yq '.metadata.annotations."deployment.arangodb.com/maintenance"="true"' "$repo_root/tests/provider/arango.yaml" | kube apply -f - >/dev/null
kube patch arangodeployment lineage --subresource=status --type=merge --patch-file "$test_dir/recovery-members.json" >/dev/null
new_uid=$(kube get arangodeployment lineage -o jsonpath='{.metadata.uid}')
[[ $new_uid != "$source_uid" ]]
kube annotate arangodeployment lineage deployment.arangodb.com/maintenance- >/dev/null
wait_for 'real operator recovers the recreated source using retained members' database_ready
wait_for 'stale application ownership is rejected' product_ready False ConnectionOwnerMismatch
bind_publication "$new_uid"
wait_for 'independent publisher binds the current source UID' product_ready True SourceReady
wait_for 'the recreated source retains the exact graph and edges' query
after_recreation=$(retained_identities)
[[ "$(jq -c 'map(select(.kind != "ArangoDeployment"))' <<<"$before_recreation")" == "$(jq -c 'map(select(.kind != "ArangoDeployment"))' <<<"$after_recreation")" ]]

phase 'disabled gates and independent data retention' 240
for gate in PROVISIONED_SOURCES_ENABLED ENGINE_PROVIDERS_ENABLED; do
	reason=SourceFeatureDisabled
	[[ $gate != ENGINE_PROVIDERS_ENABLED ]] || reason=EngineProviderFeatureDisabled
	capture_controller_logs
	kube set env deployment/dpc "$gate=false" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'disabled gate withdraws registry readiness' product_ready False "$reason"
	disabled_without_reads "$reason"
	query
	capture_controller_logs
	kube set env deployment/dpc "$gate=true" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'restored gate republishes the real source' product_ready True SourceReady
done
before_delete=$(retained_identities)
kube delete dataproduct graph-product --wait=false >/dev/null
wait_for 'descriptor deletion removes its registry entry' registry_empty
wait_for 'query survives deletion of only the descriptor' query
[[ $before_delete == "$(retained_identities)" ]]
capture_controller_logs
for marker in synthetic-graph-reader-original synthetic-graph-reader-replacement synthetic-graph-writer-original synthetic-graph-root-original persistent-lineage-middle persistent-lineage-target; do
	if grep -Fq "$marker" "$test_dir/controller.log" "$test_dir/products-seen.jsonl" "$test_dir/registry-seen.jsonl"; then
		echo 'control-plane output contains data-plane fixture content' >&2
		exit 1
	fi
done
require_read_only_source_audit
echo 'PASS: real ArangoDB traversal, privileges, rotation, recovery, gates and independent retention'
bounded docker stats --no-stream --format 'Owned cluster CPU={{.CPUPerc}} memory={{.MemUsage}}' "$control_node"
