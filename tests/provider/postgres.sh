#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
for command in ksail docker kubectl helm jq yq openssl timeout curl cosign; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done
provider_name=postgres
product_name=warehouse-product
product_id=urn:example:warehouse
consumer_pod=postgres-consumer
source_api_group=postgresql.cnpg.io
source_resource=clusters
source "$repo_root/tests/provider/common.sh"

# provider_diagnostics reports readiness and identity markers without credentials or database records.
provider_diagnostics() {
	kube get cluster warehouse -o json | jq '{
    uid:.metadata.uid,generation:.metadata.generation,image:.status.image,phase:.status.phase,
    instances:.status.instances,readyInstances:.status.readyInstances,
    currentPrimary:.status.currentPrimary,targetPrimary:.status.targetPrimary,
    conditions:[.status.conditions[]? | {type,status,reason,observedGeneration}]}'
	kube get dataproducts -o json | jq '[.items[] | {
    name:.metadata.name,generation:.metadata.generation,
    conditions:[.status.conditions[]? | {type,status,reason,observedGeneration}]}]'
}

# Bound to the anonymously readable, signed artifact produced by the owned AGE release.
age_image=ghcr.io/devantler-tech/data-product-controller-postgresql-age:17.11-age1.7.0-dpc1.16.0@sha256:0b6e2d75d5551586570979d767a28b255953c2ee86820409ad9fe37d91ce3fa8
age_source=04d6ec7b517b59e6d2cffcab484f8a42a711c1ce
age_release=v1.16.0
age_publisher=86f0f95e5ac93ec914f5717f561af878b7d09bf1
operator_image=ghcr.io/cloudnative-pg/cloudnative-pg:1.30.1@sha256:923c267ec29636db3bee20f993d0ec4973fa22998e1adad37da79e4d32b5bc07

# query_model runs the independent consumer assertion for one published data model.
query_model() {
	local model=$1
	shift
	kube exec postgres-consumer -- /fixture postgres-probe "$model" "$@"
}
# query_all checks the persisted SQL, JSONB and AGE query results.
query_all() {
	local model
	for model in sql document graph; do query_model "$model" || return 1; done
}
# database_ready requires actual operator instances and a settled primary.
database_ready() {
	kube get cluster warehouse -o json | jq -e '
    .status.instances == 1 and .status.readyInstances == 1 and
    .status.currentPrimary != "" and .status.currentPrimary == .status.targetPrimary and
    any(.status.conditions[]?; .type == "Ready" and .status == "True")' >/dev/null
}
# age_profile_ready verifies effective preload and installed AGE through the independent owner's local session.
age_profile_ready() {
	local primary observed
	primary=$(kube get cluster warehouse -o jsonpath='{.status.currentPrimary}') || return 1
	[[ -n $primary ]] || return 1
	observed=$(bounded kubectl --request-timeout=0 -n products exec -i "$primary" -c postgres -- \
		env 'PGOPTIONS=-c statement_timeout=5000' psql -X --no-password -qAt \
		-v ON_ERROR_STOP=1 -h /controller/run -p 5432 -U postgres -d catalog -f - \
		<"$repo_root/tests/provider/age-runtime.sql" 2>"$test_dir/age-profile.log") || return 1
	[[ $observed == t ]]
}
# publication_names lists the three independent publication names without credential data.
publication_names() { printf '%s\n' warehouse-app warehouse-document-reader warehouse-graph-reader; }
# bind_sql_publication republishes only recovered credential ownership after independent query/retention proof.
# CNPG deliberately does not adopt an existing application Secret without a Cluster owner.
bind_sql_publication() {
	local uid
	uid=$(kube get cluster warehouse -o jsonpath='{.metadata.uid}')
	[[ -n $uid ]] || return 1
	kube patch secret warehouse-app --type=merge -p "$(jq -nc --arg uid "$uid" '{metadata:{
    ownerReferences:[{apiVersion:"postgresql.cnpg.io/v1",kind:"Cluster",name:"warehouse",uid:$uid}]}}')" >/dev/null
}
# bind_hybrid_publications binds current source identity and generation to verified model capabilities.
bind_hybrid_publications() {
	local uid generation model
	uid=$(kube get cluster warehouse -o jsonpath='{.metadata.uid}')
	generation=$(kube get cluster warehouse -o jsonpath='{.metadata.generation}')
	for model in document graph; do
		kube patch secret "warehouse-$model-reader" --type=merge -p "$(jq -nc --arg uid "$uid" \
			--arg generation "$generation" --arg model "$model" '{metadata:{
        ownerReferences:[{apiVersion:"postgresql.cnpg.io/v1",kind:"Cluster",name:"warehouse",uid:$uid}],
        annotations:{"data.devantler.tech/cnpg-hybrid-publication":"v1",
          "data.devantler.tech/cnpg-hybrid-access":"read-only",
          "data.devantler.tech/cnpg-hybrid-source-generation":$generation,
          "data.devantler.tech/cnpg-hybrid-database":"catalog",
          "data.devantler.tech/cnpg-hybrid-user":($model+"_reader"),
          "data.devantler.tech/cnpg-hybrid-capability":(if $model=="document" then "jsonb/v1" else "age/1.7.0" end),
          "data.devantler.tech/cnpg-hybrid-schema":"public",
          "data.devantler.tech/cnpg-hybrid-table":"documents",
          "data.devantler.tech/cnpg-hybrid-column":"payload",
          "data.devantler.tech/cnpg-hybrid-graph":"lineage"}}}')" >/dev/null
	done
}
# matrix_ready checks current conditions and registry readiness for the complete model matrix.
matrix_ready() {
	local status=$1 reason=${2:-} model name
	for model in sql document graph; do
		name="postgres-$model-product"
		kube get dataproduct "$name" -o json >"$test_dir/product-$model.json" || return 1
		cat "$test_dir/product-$model.json" >>"$test_dir/products-seen.jsonl"
		jq -e --arg status "$status" --arg reason "$reason" '
      . as $p | [.status.conditions[]? | select(.type=="Ready" or .type=="SourceReady") |
        select(.status==$status and .observedGeneration==$p.metadata.generation)] | length==2' "$test_dir/product-$model.json" >/dev/null || return 1
		if [[ -n $reason ]]; then
			jq -e --arg reason "$reason" 'any(.status.conditions[]?; .type=="SourceReady" and .reason==$reason)' "$test_dir/product-$model.json" >/dev/null || return 1
		fi
	done
	curl --fail --silent --max-time 5 "http://127.0.0.1:$registry_port/api/v1/products" >"$test_dir/registry.json" || return 1
	cat "$test_dir/registry.json" >>"$test_dir/registry-seen.jsonl"
	jq -e --argjson ready "$(if [[ $status == True ]]; then echo true; else echo false; fi)" '
    .products | length==3 and (map(.id)|sort)==["urn:example:postgres-document","urn:example:postgres-graph","urn:example:postgres-sql"] and all(.[]; .ready==$ready)' "$test_dir/registry.json" >/dev/null
}
# model_unavailable checks one model's current failure conditions and the served registry.
model_unavailable() {
	local model=$1 reason=$2
	kube get dataproduct "postgres-$model-product" -o json >"$test_dir/hybrid-failure.json" || return 1
	cat "$test_dir/hybrid-failure.json" >>"$test_dir/products-seen.jsonl"
	jq -e --arg reason "$reason" '. as $p | any(.status.conditions[]?;
    .type=="SourceReady" and .status=="False" and .reason==$reason and .observedGeneration==$p.metadata.generation)' "$test_dir/hybrid-failure.json" >/dev/null || return 1
	curl --fail --silent --max-time 5 "http://127.0.0.1:$registry_port/api/v1/products" >"$test_dir/registry.json" || return 1
	cat "$test_dir/registry.json" >>"$test_dir/registry-seen.jsonl"
	jq -e --arg id "urn:example:postgres-$model" '.products | length==3 and any(.[]; .id==$id and .ready==false)' "$test_dir/registry.json" >/dev/null
}
# retained_identities captures source, credential and storage UIDs without reading Secret data.
retained_identities() {
	kube get cluster,secret,pvc -o json | jq -ceS '[.items[] | select(
    (.kind=="Cluster" and .metadata.name=="warehouse") or .kind=="PersistentVolumeClaim" or
    (.kind=="Secret" and (.metadata.name=="warehouse-app" or .metadata.name=="warehouse-document-reader" or .metadata.name=="warehouse-graph-reader"))) |
    {kind,name:.metadata.name,uid:.metadata.uid,deleting:.metadata.deletionTimestamp}] |
    if length>=5 and all(.[]; .uid!=null and .deleting==null) then sort_by(.kind,.name) else error("retention incomplete") end'
}

start_cluster
phase 'pinned operator and PostgreSQL startup' 600
bounded curl --fail --location --silent --show-error --max-time 90 \
	https://github.com/cloudnative-pg/charts/releases/download/cloudnative-pg-v0.29.1/cloudnative-pg-0.29.1.tgz -o "$test_dir/operator.tgz"
printf '%s  %s\n' b53d3991fe84bcf38767e7702cae78666265427a127a26fff168ab4207d2b1df "$test_dir/operator.tgz" | sha256sum --check --status
bounded cosign verify \
	--certificate-identity https://github.com/cloudnative-pg/cloudnative-pg/.github/workflows/release-publish.yml@refs/tags/v1.30.1 \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com "$operator_image" >"$test_dir/operator-signature.json"
bounded cosign verify \
	--certificate-identity "https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-age.yaml@$age_publisher" \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com \
	--certificate-github-workflow-repository devantler-tech/data-product-controller \
	--certificate-github-workflow-ref "refs/tags/$age_release" \
	--certificate-github-workflow-sha "$age_source" "$age_image" >"$test_dir/age-signature.json"
operator_platform=$(platform_digest "$operator_image" "$test_dir/operator-index.json")
age_platform=$(platform_digest "$age_image" "$test_dir/age-index.json")
bounded helm template cnpg "$test_dir/operator.tgz" --include-crds --namespace products \
	--set config.clusterWide=false --set config.data.WATCH_NAMESPACE=products \
	--set "image.tag=1.30.1@${operator_image##*@}" \
	--set resources.requests.cpu=100m --set resources.requests.memory=128Mi \
	--set resources.limits.cpu=1 --set resources.limits.memory=512Mi >"$test_dir/operator.yaml"
bounded kubectl --request-timeout=0 -n products apply --server-side -f "$test_dir/operator.yaml" >/dev/null
bounded kubectl --request-timeout=0 -n products rollout status deployment/cnpg-cloudnative-pg --timeout="$(remaining)s"
for model in document graph writer; do
	user="${model}_reader"
	[[ $model != writer ]] || user=catalog_writer
	printf '%s' "synthetic-postgres-$model" >"$test_dir/$model-password"
	kube create secret generic "warehouse-$model-reader" --type=kubernetes.io/basic-auth \
		--from-literal="username=$user" --from-file="password=$test_dir/$model-password" --dry-run=client -o yaml |
		yq 'with(select(.metadata.name=="warehouse-writer-reader"); .metadata.name="warehouse-writer") |
      .metadata.labels."cnpg.io/reload"="true"' | kube apply -f - >/dev/null
done
kube create configmap postgres-bootstrap --from-file="bootstrap.sql=$repo_root/tests/provider/postgres-bootstrap.sql" >/dev/null
kube apply -f "$repo_root/tests/provider/postgres.yaml" >/dev/null
wait_for 'real CNPG operator reports one settled PostgreSQL primary' database_ready
wait_for 'the independent owner verifies effective AGE preload and extension version' age_profile_ready
require_pinned_image app.kubernetes.io/name=cloudnative-pg manager "$operator_image" "$operator_platform"
require_pinned_image 'cnpg.io/cluster=warehouse,cnpg.io/podRole=instance' postgres "$age_image" "$age_platform"
kube get cluster warehouse -o json | jq '{image:.status.image,instances:.status.instances,readyInstances:.status.readyInstances}'
mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -sha256 -subj /CN=disposable-postgres-query \
	-keyout "$test_dir/tls/ca.key" -out "$test_dir/tls/ca.crt" >/dev/null 2>&1
make_certificate query 'DNS:postgres-query-sql.products.svc.cluster.local,DNS:postgres-query-document.products.svc.cluster.local,DNS:postgres-query-graph.products.svc.cluster.local'
kube create secret generic postgres-query-tls --from-file="tls.crt=$test_dir/tls/query.crt" \
	--from-file="tls.key=$test_dir/tls/query.key" --from-file="ca.crt=$test_dir/tls/ca.crt" >/dev/null
for model in sql document graph; do
	secret="warehouse-$model-reader"
	[[ $model != sql ]] || secret=warehouse-app
	kube get secret "$secret" -o jsonpath='{.data.password}' | base64 --decode >"$test_dir/stale-password"
	kube create secret generic "warehouse-stale-$model" --from-file="password=$test_dir/stale-password" >/dev/null
	export DPC_POSTGRES_MODEL=$model DPC_POSTGRES_SECRET=$secret DPC_FIXTURE_IMAGE=$fixture_image
	yq 'select(.kind=="Deployment" or .kind=="Service") |
    .metadata.name += "-"+strenv(DPC_POSTGRES_MODEL) |
    with(select(.kind=="Service"); .spec.selector.app=.metadata.name) |
    with(select(.kind=="Deployment"); .spec.selector.matchLabels.app=.metadata.name |
      .spec.template.metadata.labels.app=.metadata.name |
      .spec.template.spec.containers[].image=strenv(DPC_FIXTURE_IMAGE) |
      .spec.template.spec.containers[].env[0].value=strenv(DPC_POSTGRES_MODEL) |
      .spec.template.spec.volumes[0].secret.secretName=strenv(DPC_POSTGRES_SECRET) |
      .spec.template.spec.volumes[1].secret.secretName="warehouse-stale-"+strenv(DPC_POSTGRES_MODEL))' \
		"$repo_root/tests/provider/postgres-workloads.yaml" | kube apply -f - >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status "deployment/postgres-query-$model" --timeout="$(remaining)s"
done
yq 'select(.kind=="Pod") | .spec.containers[].image=strenv(DPC_FIXTURE_IMAGE)' "$repo_root/tests/provider/postgres-workloads.yaml" | kube apply -f - >/dev/null
kube apply -f "$repo_root/tests/provider/postgres-network-policy.yaml" >/dev/null
bounded kubectl --request-timeout=0 -n products wait --for=condition=Ready pod/postgres-writer pod/postgres-consumer --timeout="$(remaining)s"
for model in sql document graph; do
	kube exec postgres-writer -- /fixture postgres-seed "$model"
	wait_for "$model query returns independently seeded persistent records through verified HTTPS" query_model "$model"
	kube exec "deployment/postgres-query-$model" -- /fixture postgres-privileges
	query_model "$model" contract
done
echo 'PASS: all three models enforce actual read-only grants, including Cypher create, update and delete'
kube label pod postgres-consumer app=unapproved --overwrite >/dev/null
for model in sql document graph; do wait_for "$model rejects unapproved consumer by TCP isolation" query_model "$model" denied; done
kube label pod postgres-consumer app=postgres-consumer --overwrite >/dev/null
wait_for 'all authorized query paths recover' query_all

phase 'released SQL and current three-model publication' 300
install_controller ghcr.io/devantler-tech/data-product-controller 1.14.0 "${released_image##*@}"
yq 'with(select(.kind=="RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' "$repo_root/docs/examples/sql-provider-observer-rbac.yaml" | kube apply -f - >/dev/null
yq '.spec.outputs[0].url="https://postgres-query-sql.products.svc.cluster.local:8443/api/rows" |
  .spec.outputs[0].contractUrl="https://postgres-query-sql.products.svc.cluster.local:8443/openapi.json"' "$repo_root/docs/examples/sql-provider-product.yaml" | kube apply -f - >/dev/null
wait_for 'signed released controller publishes the actual native SQL source' product_ready True SourceReady
query_model sql
install_controller dpc-provider "$cluster_name" ''
kube delete dataproduct warehouse-product --wait=false >/dev/null
wait_for 'released descriptor is removed before matrix publication' registry_empty
yq 'with(select(.kind=="Role"); .rules[1].resourceNames=["warehouse-app","warehouse-document-reader","warehouse-graph-reader"]) |
  with(select(.kind=="RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' "$repo_root/docs/examples/sql-provider-observer-rbac.yaml" | kube apply -f - >/dev/null
bind_hybrid_publications
for model in sql document graph; do
	export DPC_POSTGRES_MODEL=$model
	yq '.metadata.name="postgres-"+strenv(DPC_POSTGRES_MODEL)+"-product" |
    .spec.id="urn:example:postgres-"+strenv(DPC_POSTGRES_MODEL) |
    .spec.name="PostgreSQL "+strenv(DPC_POSTGRES_MODEL) |
    .spec.source.engine.type=strenv(DPC_POSTGRES_MODEL) |
    .spec.outputs[0].url="https://postgres-query-"+strenv(DPC_POSTGRES_MODEL)+".products.svc.cluster.local:8443" |
    .spec.outputs[0].contractUrl=.spec.outputs[0].url+"/openapi.json" |
    .spec.outputs[0].url += (if strenv(DPC_POSTGRES_MODEL)=="sql" then "/api/rows" elif strenv(DPC_POSTGRES_MODEL)=="document" then "/api/documents" else "/api/lineage" end) |
    with(select(strenv(DPC_POSTGRES_MODEL)!="sql"); .spec.source.adapter="cnpg-hybrid/v1" |
      .spec.source.engine.provider="cnpg-hybrid" | .spec.source.connectionSecretRef.name="warehouse-"+strenv(DPC_POSTGRES_MODEL)+"-reader")' \
		"$repo_root/docs/examples/sql-provider-product.yaml" | kube apply -f - >/dev/null
done
wait_for 'current controller publishes all three real source and registry profiles' matrix_ready True SourceReady
query_all
[[ $(audit_reads) -gt 0 ]]
kube patch cluster warehouse --type=merge -p '{"spec":{"resources":{"limits":{"cpu":"900m"}}}}' >/dev/null
wait_for 'source spec change settles independently of publication metadata' database_ready
for model in document graph; do
	wait_for "$model rejects a publication from the previous source generation" model_unavailable "$model" ConnectionPublicationUnsupported
done
query_all
bind_hybrid_publications
wait_for 'verified current source generation restores all publications' matrix_ready True SourceReady

phase 'actual PostgreSQL hibernation and retained recovery' 600
before_pause=$(retained_identities)
kube annotate cluster warehouse cnpg.io/hibernation=on --overwrite >/dev/null
wait_for 'actual hibernation withdraws all current product readiness' matrix_ready False
for model in sql document graph; do wait_for "$model reports the actual database outage" query_model "$model" outage; done
kube annotate cluster warehouse cnpg.io/hibernation=off --overwrite >/dev/null
wait_for 'same PostgreSQL storage recovers' database_ready
wait_for 'resumed source retains the effective AGE runtime profile' age_profile_ready
wait_for 'all persisted SQL, JSONB and AGE records recover' query_all
wait_for 'all current metadata publications recover' matrix_ready True SourceReady
[[ $before_pause == "$(retained_identities)" ]]
for model in sql document graph; do
	secret="warehouse-$model-reader"
	[[ $model != sql ]] || secret=warehouse-app
	secret_uid=$(kube get secret "$secret" -o jsonpath='{.metadata.uid}')
	pod_uid=$(kube get pod -l "app=postgres-query-$model" -o jsonpath='{.items[0].metadata.uid}')
	printf '%s' "synthetic-postgres-$model-replacement" >"$test_dir/replacement"
	password=$(base64 <"$test_dir/replacement" | tr -d '\n')
	kube patch secret "$secret" --type=merge -p "$(jq -nc --arg password "$password" '{data:{password:$password},metadata:{labels:{"cnpg.io/reload":"true"}}}')" >/dev/null
	wait_for "$model stale password is rejected with code 28P01" kube exec "deployment/postgres-query-$model" -- /fixture postgres-stale-password
	wait_for "$model unchanged query workload reads its rotated projection" query_model "$model"
	[[ $(kube get secret "$secret" -o jsonpath='{.metadata.uid}') == "$secret_uid" ]]
	[[ $(kube get pod -l "app=postgres-query-$model" -o jsonpath='{.items[0].metadata.uid}') == "$pod_uid" ]]
done
wait_for 'rotation preserves all current publications' matrix_ready True SourceReady

phase 'independent source recreation and generation rebinding' 300
before_recreation=$(retained_identities)
old_uid=$(kube get cluster warehouse -o jsonpath='{.metadata.uid}')
bounded kubectl --request-timeout=0 -n products delete cluster warehouse --cascade=orphan --timeout="$(remaining)s"
kube apply -f "$repo_root/tests/provider/postgres.yaml" >/dev/null
new_uid=$(kube get cluster warehouse -o jsonpath='{.metadata.uid}')
[[ $new_uid != "$old_uid" ]]
wait_for 'recreated independent source adopts retained storage' database_ready
wait_for 'recreated source retains the effective AGE runtime profile' age_profile_ready
wait_for 'recreated source serves all retained records before republishing credentials' query_all
[[ $(jq -c 'map(select(.kind!="Cluster"))' <<<"$before_recreation") == "$(retained_identities | jq -c 'map(select(.kind!="Cluster"))')" ]]
publication_anchor_uid=$(kube get configmap postgres-bootstrap -o jsonpath='{.metadata.uid}')
[[ -n $publication_anchor_uid ]]
for model in document graph; do
	# Retain the Secret under an independent owner while testing the absence of
	# a current Cluster owner, without racing garbage collection of a stale UID.
	kube patch secret "warehouse-$model-reader" --type=merge -p "$(jq -nc --arg anchor "$publication_anchor_uid" '{metadata:{ownerReferences:[
      {apiVersion:"v1",kind:"ConfigMap",name:"postgres-bootstrap",uid:$anchor}]}}')" >/dev/null
done
# Every descriptor rejects a publication without a current source owner while queries keep working.
for model in sql document graph; do
	wait_for "$model rejects a publication without a current source owner" model_unavailable "$model" ConnectionOwnerMismatch
done
bind_sql_publication
bind_hybrid_publications
wait_for 'fresh source UID restores all current publications' matrix_ready True SourceReady
wait_for 'recreated source serves every retained model record' query_all
[[ $(jq -c 'map(select(.kind!="Cluster"))' <<<"$before_recreation") == "$(retained_identities | jq -c 'map(select(.kind!="Cluster"))')" ]]

phase 'independent gates and descriptor deletion retain the data plane' 240
for gate in PROVISIONED_SOURCES_ENABLED ENGINE_PROVIDERS_ENABLED; do
	reason=SourceFeatureDisabled
	[[ $gate != ENGINE_PROVIDERS_ENABLED ]] || reason=EngineProviderFeatureDisabled
	capture_controller_logs
	kube set env deployment/dpc "$gate=false" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'disabled gate withdraws the complete current matrix' matrix_ready False "$reason"
	before=$(audit_reads)
	bounded sleep 35
	[[ $before == "$(audit_reads)" ]]
	query_all
	capture_controller_logs
	kube set env deployment/dpc "$gate=true" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'restored gate republishes every current source' matrix_ready True SourceReady
done
before_delete=$(retained_identities)
kube delete dataproduct postgres-sql-product postgres-document-product postgres-graph-product --wait=false >/dev/null
wait_for 'all deleted descriptors leave the registry' registry_empty
wait_for 'descriptor deletion retains every independent query' query_all
[[ $before_delete == "$(retained_identities)" ]]
capture_controller_logs
for marker in synthetic-postgres persistent-row persistent-jsonb private-excluded-record '"id":"middle"' '"id":"target"'; do
	if grep -Fq "$marker" "$test_dir/controller.log" "$test_dir/products-seen.jsonl" "$test_dir/registry-seen.jsonl"; then
		echo 'control-plane projection contains data-plane fixture content' >&2
		exit 1
	fi
done
require_read_only_source_audit '[{"group":"postgresql.cnpg.io","resource":"clusters","name":"warehouse"},{"group":"","resource":"secrets","name":"warehouse-app"},{"group":"","resource":"secrets","name":"warehouse-document-reader"},{"group":"","resource":"secrets","name":"warehouse-graph-reader"}]'
echo 'PASS: real native SQL, hybrid JSONB and hybrid AGE query, grant, rotation and retained lifecycle acceptance'
bounded docker stats --no-stream --format 'Owned cluster CPU={{.CPUPerc}} memory={{.MemUsage}}' "$control_node"
