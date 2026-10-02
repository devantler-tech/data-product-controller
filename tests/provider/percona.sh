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
	echo 'Percona UBI10 acceptance requires an amd64 x86-64-v3 runner' >&2
	exit 1
}
for capability in avx2 bmi1 bmi2 fma; do
	grep -qw "$capability" /proc/cpuinfo || {
		echo "runner lacks required CPU capability: $capability" >&2
		exit 1
	}
done
echo 'PASS: runner supports the documented Percona x86-64-v3 CPU profile'

test_dir=$(mktemp -d)
cluster_name="dpc-percona-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-$RANDOM"
export KUBECONFIG="$test_dir/kubeconfig"
cluster_config="$test_dir/cluster/ksail.yaml"
cluster_started=false
started_at=$SECONDS
# 33 minutes of assertions plus 3 minutes reserved for cleanup, under the 40-minute job.
work_deadline=$((started_at + 1980))
phase_deadline=$work_deadline
source "$repo_root/tests/provider/budget.sh"

cleanup() {
	local result=$? node
	trap - EXIT INT TERM
	phase_deadline=$((SECONDS + 180))
	if [[ -n ${registry_forward_pid:-} ]]; then
		kill "$registry_forward_pid" 2>/dev/null || true
		wait "$registry_forward_pid" 2>/dev/null || true
	fi
	if [[ $cluster_started == true ]]; then
		if [[ $result != 0 ]]; then
			# Setup can fail before the product namespace or CRDs exist. Include
			# network-plugin readiness so a cluster timeout has a concrete cause.
			kubectl --request-timeout=10s -n kube-system get pods -o wide || true
			kubectl --request-timeout=10s -n kube-system get events --sort-by=.metadata.creationTimestamp || true
			kubectl --request-timeout=10s -n kube-system logs -l k8s-app=cilium --all-containers=true --tail=100 || true
			kubectl --request-timeout=10s -n products get pods,pvc -o wide || true
			kubectl --request-timeout=10s -n products get perconaservermongodbs -o wide || true
			kubectl --request-timeout=10s -n products get dataproducts -o wide || true
			kubectl --request-timeout=10s -n products get dataproduct document-product -o json |
				jq '{generation:.metadata.generation,conditions:.status.conditions}' || true
			[[ ! -f "$test_dir/forward.log" ]] || cat "$test_dir/forward.log" >&2
			kubectl --request-timeout=10s -n products get events --sort-by=.metadata.creationTimestamp || true
			# Do not dump Secrets, database records or system-user logs.
		fi
		timeout --signal=TERM --kill-after=5s 150s ksail cluster delete --name "$cluster_name" --provider Docker \
			--kubeconfig "$KUBECONFIG" --config "$cluster_config" --force --delete-storage || result=1
		for node in $(docker ps --all --filter "label=io.x-k8s.kind.cluster=$cluster_name" --format '{{.Names}}'); do
			bounded docker rm --force "$node" >/dev/null || result=1
		done
		[[ -z $(docker ps --all --filter "label=io.x-k8s.kind.cluster=$cluster_name" --format '{{.Names}}') ]] || result=1
	fi
	rm -rf "$test_dir"
	printf 'Percona acceptance finished in %ss (exit %s)\n' "$((SECONDS - started_at))" "$result"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

kube() { kubectl --request-timeout=15s -n products "$@"; }
query() { kube exec document-consumer -- /fixture probe "$@"; }
forward_registry() {
	if [[ -n ${registry_forward_pid:-} ]]; then
		kill "$registry_forward_pid" 2>/dev/null || true
		wait "$registry_forward_pid" 2>/dev/null || true
	fi
	registry_port=''
	wait_for 'current controller revision has one Ready registry pod' registry_target_ready
	kubectl --request-timeout=0 -n products port-forward --address=127.0.0.1 "pod/$registry_pod" ":$registry_target_port" >"$test_dir/forward.log" 2>&1 &
	registry_forward_pid=$!
	wait_for 'owned registry port-forward starts on an allocated loopback port' registry_forward_ready || {
		cat "$test_dir/forward.log" >&2
		return 1
	}
}
capture_controller_logs() {
	# Selector-based logs otherwise default to only ten lines. Collect every
	# current replica before replacement; failed collection is incomplete evidence.
	kube logs -l app.kubernetes.io/instance=dpc --all-containers=true --prefix=true --tail=-1 >>"$test_dir/controller.log"
}
install_controller() {
	if [[ ${controller_installed:-false} == true ]]; then
		capture_controller_logs
	fi
	bounded helm template dpc "$repo_root/charts/data-product-controller" --include-crds --namespace products \
		--set "image.repository=$1" --set "image.tag=$2" --set "image.digest=$3" --set image.pullPolicy=Never \
		--set demoProduct.enabled=false --set provisionedSources.enabled=true --set engineProviders.enabled=true |
		bounded kubectl --request-timeout=0 -n products apply -f - >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	controller_installed=true
	forward_registry
}
database_ready() {
	kube get perconaservermongodb documents -o json | jq -e '
    .spec.pause == false and .status.state == "ready" and .status.size == 1 and .status.ready == 1' >/dev/null
}
product_ready() {
	local status=$1 reason=${2:-}
	kube get dataproduct document-product -o json >"$test_dir/product.json" || return 1
	cat "$test_dir/product.json" >>"$test_dir/products-seen.jsonl"
	jq -e --arg status "$status" --arg reason "$reason" '
    . as $p | [.status.conditions[]? | select(.type == "Ready" or .type == "SourceReady") |
    select(.status == $status and .observedGeneration == $p.metadata.generation)] | length == 2' "$test_dir/product.json" >/dev/null || return 1
	if [[ -n $reason ]]; then
		jq -e --arg reason "$reason" \
			'any(.status.conditions[]?; .type == "SourceReady" and .reason == $reason)' "$test_dir/product.json" >/dev/null || return 1
	fi
	# Registry readback uses a runner port-forward; it never receives database credentials.
	kill -0 "$registry_forward_pid" 2>/dev/null || return 1
	curl --fail --silent --max-time 5 "http://127.0.0.1:$registry_port/api/v1/products" >"$test_dir/registry.json" || return 1
	cat "$test_dir/registry.json" >>"$test_dir/registry-seen.jsonl"
	jq -e --argjson ready "$(if [[ $status == True ]]; then echo true; else echo false; fi)" \
		'.products | length == 1 and .[0].id == "urn:example:documents" and .[0].ready == $ready' "$test_dir/registry.json" >/dev/null
}
registry_empty() {
	kill -0 "$registry_forward_pid" 2>/dev/null || return 1
	curl --fail --silent --max-time 5 "http://127.0.0.1:$registry_port/api/v1/products" >"$test_dir/registry.json" || return 1
	jq -e '.products | type == "array" and length == 0' "$test_dir/registry.json" >/dev/null
}
bind_publication() {
	local uid=$1
	kube patch secret documents-reader --type=merge -p \
		"$(jq -nc --arg uid "$uid" '{metadata:{ownerReferences:[{apiVersion:"psmdb.percona.com/v1",kind:"PerconaServerMongoDB",name:"documents",uid:$uid}]}}')" >/dev/null
}
retained_identities() {
	kube get perconaservermongodb,secret,pvc -o json |
		jq -ceS '[.items[] | select(.kind == "PersistentVolumeClaim" or
      (.kind == "PerconaServerMongoDB" and .metadata.name == "documents") or
      (.kind == "Secret" and .metadata.name == "documents-reader")) |
      {kind, name:.metadata.name, uid:.metadata.uid, deleting:.metadata.deletionTimestamp}] |
      if length >= 3 and all(.[]; .uid != null and .deleting == null) then sort_by(.kind,.name) else error("retention incomplete") end'
}
audit_reads() {
	docker exec "$control_node" cat /audit/log.json | jq -s '[.[] | select(.stage == "ResponseComplete" and .verb == "get")] | length'
}
disabled_without_reads() {
	local reason=$1 before after
	product_ready False "$reason" || return 1
	before=$(audit_reads) || return 1
	bounded sleep 35
	after=$(audit_reads) || return 1
	[[ $before == "$after" ]] || {
		echo 'disabled provider still reads external source or Secret' >&2
		return 1
	}
	echo 'PASS: disabled gate has zero source or Secret reads across a polling interval'
}

platform_digest() {
	local image=$1 file=$2
	bounded docker buildx imagetools inspect --raw "$image" >"$file" || return 1
	jq -er --arg immutable "${image##*@}" '
    if .manifests then
      [.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest] |
      if length == 1 then .[0] else error("expected exactly one amd64 image") end
    else $immutable end' "$file"
}

phase setup 360
mkdir "$test_dir/audit"
cat >"$test_dir/audit/policy.yaml" <<'YAML'
apiVersion: audit.k8s.io/v1
kind: Policy
omitStages: [RequestReceived]
rules:
  - level: Metadata
    users: [system:serviceaccount:products:dpc]
    resources:
      - group: psmdb.percona.com
        resources: [perconaservermongodbs]
      - group: ""
        resources: [secrets]
  - level: None
YAML
bounded ksail project init --name "$cluster_name" --distribution Vanilla --provider Docker \
	--cni Cilium --gitops-engine None --policy-engine None --load-balancer Disabled \
	--metrics-server Disabled --mirror-registry '' --local-registry localhost:5055 \
	--kubeconfig "$KUBECONFIG" --output "$test_dir/cluster" --no-devcontainer
# Initialization also defaults to public mirrors unless explicitly disabled.
# Reject generated mirror mounts before booting a node with inactive endpoints.
if [[ -d $test_dir/cluster/kind/mirrors ]]; then
	echo 'unexpected public mirror configuration in the direct-pull acceptance profile' >&2
	exit 1
fi
export DPC_TEST_AUDIT_DIR="$test_dir/audit"
yq -i '.nodes = [.nodes[0]] |
  .nodes[].image = "kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a" |
  .containerdConfigPatches += ["[plugins.\"io.containerd.cri.v1.images\".registry]\n  config_path = \"/etc/containerd/certs.d\""] |
  .nodes[0].extraMounts += [{"hostPath":strenv(DPC_TEST_AUDIT_DIR),"containerPath":"/audit"}] |
  .nodes[0].kubeadmConfigPatches += ["apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\napiServer:\n  extraArgs:\n    - name: audit-policy-file\n      value: /audit/policy.yaml\n    - name: audit-log-path\n      value: /audit/log.json\n  extraVolumes:\n    - name: audit\n      hostPath: /audit\n      mountPath: /audit\n      readOnly: false\n      pathType: Directory"]' "$test_dir/cluster/kind.yaml"
cluster_started=true
# Zero CLI node-count overrides preserve Kind's declared image, mounts and
# kubeadm patches instead of replacing the nodes with KSail's default profile.
# Public dependencies use direct registry pulls. Disposable pull-through caches
# add serialized cold-start latency before Cilium can register its CRDs; only
# the owned local fixture registry is needed by this acceptance profile.
bounded ksail cluster create --config "$cluster_config" --distribution-config "$test_dir/cluster/kind.yaml" \
	--control-planes 0 --workers 0 --mirror-registry ''
control_node=$(docker ps --filter "label=io.x-k8s.kind.cluster=$cluster_name" --filter label=io.x-k8s.kind.role=control-plane --format '{{.Names}}')
[[ -n $control_node && $control_node != *$'\n'* ]] || {
	echo 'expected one owned control-plane node' >&2
	exit 1
}
[[ $(docker inspect "$control_node" --format '{{.Config.Image}}') == kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a ]]
kubectl --request-timeout=15s version -o json | jq -e '.serverVersion.gitVersion == "v1.34.0"' >/dev/null
echo 'PASS: running node image and Kubernetes API match the pinned 1.34.0 profile'
kubectl --request-timeout=15s create namespace products >/dev/null
# Only this run's Kind node resolves the disposable registry through its Docker network.
registry_dir=/etc/containerd/certs.d/localhost_5055_
bounded docker exec "$control_node" mkdir -p "$registry_dir"
printf 'server = "http://%s-local-registry:5000"\n[host."http://%s-local-registry:5000"]\n  capabilities = ["pull", "resolve"]\n' "$cluster_name" "$cluster_name" |
	bounded docker exec -i "$control_node" cp /dev/stdin "$registry_dir/hosts.toml"
bounded docker build --tag "dpc-percona:$cluster_name" "$repo_root"
bounded docker build --file "$repo_root/tests/provider/fixture/Dockerfile" --tag "localhost:5055/provider-fixture:$cluster_name" "$repo_root"
bounded docker push "localhost:5055/provider-fixture:$cluster_name"
fixture_image=$(docker image inspect "localhost:5055/provider-fixture:$cluster_name" --format '{{index .RepoDigests 0}}')
[[ $fixture_image =~ ^localhost:5055/provider-fixture@sha256:[a-f0-9]{64}$ ]] || {
	echo 'missing pushed fixture digest' >&2
	exit 1
}
bounded docker exec "$control_node" crictl pull "$fixture_image"
released_image=ghcr.io/devantler-tech/data-product-controller@sha256:683df7a8c7701ba9b5e31d7801769c84ea221a58560bfbc667462c0c2a5368a8
bounded cosign verify \
	--certificate-identity https://github.com/devantler-tech/actions/.github/workflows/publish-app.yaml@df7fd4f83edade31c121a9de563d9a7b9b1f900d \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com \
	--certificate-github-workflow-repository devantler-tech/data-product-controller \
	--certificate-github-workflow-ref refs/tags/v1.14.0 \
	--certificate-github-workflow-sha a199b4c2e8ac4dde2f34c0a8faeeae6940a592d8 "$released_image" >"$test_dir/released-signature.json"
bounded docker pull "$released_image"
[[ $(docker image inspect "$released_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}') == a199b4c2e8ac4dde2f34c0a8faeeae6940a592d8 ]]
# Import only this run's built images into its own node; no release credentials or public push.
bounded docker save "dpc-percona:$cluster_name" >"$test_dir/images.tar"
# Kind's containerd may have a private /tmp mount. Stream the archive into the
# runtime's stdin instead of assuming a copied node path exists in that namespace.
bounded docker exec -i "$control_node" ctr --namespace k8s.io images import - <"$test_dir/images.tar"
for image in "dpc-percona:$cluster_name" "$fixture_image"; do
	bounded docker exec "$control_node" crictl inspecti "$image" >/dev/null
done
bounded docker exec "$control_node" crictl pull "$released_image"
controller_image="dpc-percona:$cluster_name"
printf 'Controller source: %s\n' "$(git -C "$repo_root" rev-parse HEAD)"
docker image inspect "$controller_image" "$fixture_image" --format '{{.Id}}'

# Integrity-checked packages install their real CRDs; no fabricated status is written.
for chart in psmdb-operator psmdb-operator-crds; do
	bounded curl --fail --location --retry 3 --max-time 120 --output "$test_dir/$chart.tgz" \
		"https://github.com/percona/percona-helm-charts/releases/download/$chart-1.23.0/$chart-1.23.0.tgz"
done
printf '%s  %s\n' \
	1b3c74e100e80a17d7333bada040116c8dfca9c7340548c3f3d6925d7574b81e "$test_dir/psmdb-operator.tgz" \
	c83347339fd456ba5cfe255b5a459ffa60771eb790d74dfc566c1ca2c15fb306 "$test_dir/psmdb-operator-crds.tgz" | sha256sum --check -
bounded helm upgrade --install percona-crds "$test_dir/psmdb-operator-crds.tgz" --namespace products
bounded kubectl --request-timeout=0 wait crd/perconaservermongodbs.psmdb.percona.com --for=condition=Established --timeout="$(remaining)s"
bounded helm upgrade --install percona "$test_dir/psmdb-operator.tgz" --namespace products --skip-crds \
	--set image.tag=1.23.0@sha256:feaff989e25346716d85be9ea918593f89ad7481d30e033df21e0a764a6a484e \
	--set disableTelemetry=true --set resources.requests.cpu=100m --set resources.requests.memory=256Mi \
	--set resources.limits.cpu=500m --set resources.limits.memory=512Mi

mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -sha256 -subj /CN=disposable-provider-ca \
	-keyout "$test_dir/tls/ca.key" -out "$test_dir/tls/ca.crt" >/dev/null 2>&1
make_certificate() {
	local name=$1 san=$2 organization=${3:-disposable-provider}
	openssl req -newkey rsa:2048 -nodes -subj "/CN=$name/O=$organization" \
		-keyout "$test_dir/tls/$name.key" -out "$test_dir/tls/$name.csr" >/dev/null 2>&1
	printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth,clientAuth\n' "$san" >"$test_dir/tls/$name.ext"
	openssl x509 -req -days 1 -sha256 -CA "$test_dir/tls/ca.crt" -CAkey "$test_dir/tls/ca.key" -CAcreateserial \
		-in "$test_dir/tls/$name.csr" -extfile "$test_dir/tls/$name.ext" -out "$test_dir/tls/$name.crt" >/dev/null 2>&1
}
make_certificate documents 'DNS:documents-rs0,DNS:documents-rs0.products,DNS:documents-rs0.products.svc,DNS:documents-rs0.products.svc.cluster.local,DNS:*.documents-rs0.products.svc.cluster.local,DNS:localhost'
make_certificate document-query 'DNS:document-query.products.svc.cluster.local'
make_certificate document-client 'DNS:document-query.products.svc.cluster.local' disposable-application
for secret in documents-ssl documents-ssl-internal document-query-tls document-client-tls; do
	certificate=documents
	case $secret in
	document-query-tls) certificate=document-query ;;
	document-client-tls) certificate=document-client ;;
	esac
	kube create secret generic "$secret" --from-file=ca.crt="$test_dir/tls/ca.crt" \
		--from-file=tls.crt="$test_dir/tls/$certificate.crt" --from-file=tls.key="$test_dir/tls/$certificate.key" >/dev/null
done
printf 'synthetic-reader-original' >"$test_dir/reader-password"
printf 'synthetic-writer-password' >"$test_dir/writer-password"
kube create secret generic documents-reader --from-file=password="$test_dir/reader-password" >/dev/null
kube create secret generic documents-stale-reader --from-file=password="$test_dir/reader-password" >/dev/null
kube create secret generic documents-writer --from-file=password="$test_dir/writer-password" >/dev/null
kube apply -f "$repo_root/tests/provider/percona.yaml" >/dev/null
kube apply -f "$repo_root/tests/provider/network-policy.yaml" >/dev/null

phase 'operator and database startup' 600
server_digest=$(platform_digest 'percona/percona-server-mongodb@sha256:53f89c001997627554e6afc0feb5906209ba109f4f98c62f2ca8456c214af60c' "$test_dir/server-index.json")
operator_digest=$(platform_digest 'percona/percona-server-mongodb-operator@sha256:feaff989e25346716d85be9ea918593f89ad7481d30e033df21e0a764a6a484e' "$test_dir/operator-index.json")
bounded kubectl --request-timeout=0 -n products rollout status deployment/percona-psmdb-operator --timeout="$(remaining)s"
wait_for 'real operator reports the managed TLS replica set ready' database_ready
kube get pods -o json | jq '[.items[] | {name:.metadata.name,uid:.metadata.uid,images:[.status.containerStatuses[]? | {name,imageID}]}]'
# CRI may report either the immutable multi-platform index or its selected
# manifest. Require the exact declared pin, one running container and a node
# architecture present exactly once in the verified index.
kubectl --request-timeout=15s get node "$control_node" -o json | jq -e \
	'.status.nodeInfo.architecture == "amd64" and .status.nodeInfo.operatingSystem == "linux"' >/dev/null
for component in server operator; do
	if [[ $component == server ]]; then
		selector=app=document-database
		container=mongod
		image=percona/percona-server-mongodb:8.0.26-11@sha256:53f89c001997627554e6afc0feb5906209ba109f4f98c62f2ca8456c214af60c
		digest=$server_digest
	else
		selector=app.kubernetes.io/name=psmdb-operator
		container=psmdb-operator
		image=percona/percona-server-mongodb-operator:1.23.0@sha256:feaff989e25346716d85be9ea918593f89ad7481d30e033df21e0a764a6a484e
		digest=$operator_digest
	fi
	kube get pods -l "$selector" -o json | jq -e --arg container "$container" \
		--arg image "$image" --arg index "${image##*@}" --arg digest "$digest" '
    .items | length == 1 and all(.[];
      ([.spec.containers[] | select(.name == $container and .image == $image)] | length == 1) and
      ([.status.containerStatuses[] | select(.name == $container and .ready == true and .state.running != null) |
        select(.imageID | endswith($index) or endswith($digest))] | length == 1))' >/dev/null
done
kubectl --request-timeout=15s version -o json | jq '.serverVersion'
kube get perconaservermongodb documents -o json | jq '{uid:.metadata.uid,finalizers:.metadata.finalizers,state:.status.state,ready:.status.ready}'
export DPC_PROVIDER_FIXTURE_IMAGE="$fixture_image"
yq 'with(select(.kind == "Deployment"); .spec.template.spec.containers[0].image=strenv(DPC_PROVIDER_FIXTURE_IMAGE)) |
  with(select(.kind == "Pod"); .spec.containers[0].image=strenv(DPC_PROVIDER_FIXTURE_IMAGE))' \
	"$repo_root/tests/provider/workloads.yaml" | kube apply -f - >/dev/null
bounded kubectl --request-timeout=0 -n products rollout status deployment/document-query --timeout="$(remaining)s"
bounded kubectl --request-timeout=0 -n products wait pod/document-writer pod/document-consumer --for=condition=Ready --timeout="$(remaining)s"
bounded kubectl --request-timeout=0 -n products exec document-writer -- /fixture seed
wait_for 'reader returns the seeded record through verified HTTPS' query
query contract
bounded kubectl --request-timeout=0 -n products exec deployment/document-query -- /fixture privileges
echo 'PASS: real reader cannot insert, update, delete or manage privileges'
# The same consumer must lose and regain query access when its policy identity changes.
kube label pod document-consumer app=unapproved --overwrite >/dev/null
wait_for 'NetworkPolicy times out the unapproved consumer without an HTTP response' query denied
kube label pod document-consumer app=document-consumer --overwrite >/dev/null
wait_for 'authorized consumer regains the same query path' query

phase 'released and current controller publication' 240
install_controller ghcr.io/devantler-tech/data-product-controller 1.14.0 "${released_image##*@}"
yq 'with(select(.kind == "RoleBinding"); .subjects[0].name="dpc" | .subjects[0].namespace="products")' \
	"$repo_root/docs/examples/document-provider-observer-rbac.yaml" | kube apply -f - >/dev/null
yq '.spec.outputs[0].url="https://document-query.products.svc.cluster.local:8443/api/documents" |
  .spec.outputs[0].contractUrl="https://document-query.products.svc.cluster.local:8443/openapi.json"' \
	"$repo_root/docs/examples/document-provider-product.yaml" | kube apply -f - >/dev/null
wait_for 'missing independent publication ownership blocks readiness' product_ready False ConnectionOwnerMismatch
source_uid=$(kube get perconaservermongodb documents -o jsonpath='{.metadata.uid}')
bind_publication "$source_uid"
wait_for 'binding the current UID restores source and registry readiness' product_ready True SourceReady
query
echo 'PASS: signed released v1.14.0 controller observes the real source and application publication'
install_controller dpc-percona "$cluster_name" ''
wait_for 'current PR controller preserves real source and query readiness' product_ready True SourceReady
query
[[ $(audit_reads) -gt 0 ]] || {
	echo 'API audit observation produced no data' >&2
	exit 1
}
query_pod_uid=$(kube get pod -l app=document-query -o jsonpath='{.items[0].metadata.uid}')
secret_uid=$(kube get secret documents-reader -o jsonpath='{.metadata.uid}')

phase 'actual source pause' 120
kube patch perconaservermongodb documents --type=merge -p '{"spec":{"pause":true}}' >/dev/null
wait_for 'pause withdraws current product readiness' product_ready False
wait_for 'pause stops the real published query' query outage

phase 'resume, rotation and retained source recreation' 600
kube patch perconaservermongodb documents --type=merge -p '{"spec":{"pause":false}}' >/dev/null
wait_for 'real source resumes' database_ready
wait_for 'same persisted record remains queryable after resume' query
wait_for 'resume restores current product readiness' product_ready True SourceReady
printf 'synthetic-reader-replacement' >"$test_dir/reader-password"
kube create secret generic documents-reader --from-file=password="$test_dir/reader-password" --dry-run=client -o yaml | kube apply -f - >/dev/null
wait_for 'operator rejects the stale password with authentication code 18' kube exec deployment/document-query -- /fixture stale-password
wait_for 'unchanged workload reads the rotated projection' query
[[ $(kube get secret documents-reader -o jsonpath='{.metadata.uid}') == "$secret_uid" ]]
[[ $(kube get pod -l app=document-query -o jsonpath='{.items[0].metadata.uid}') == "$query_pod_uid" ]]
wait_for 'rotation preserves metadata publication readiness' product_ready True SourceReady

before_recreation=$(retained_identities)
# Orphan deletion preserves the independently managed PVC and publication. The operator's
# orderly-pod finalizer stops its own workloads; no PVC deletion finalizer is installed.
bounded kubectl --request-timeout=0 -n products delete perconaservermongodb documents --cascade=orphan --timeout="$(remaining)s"
kube apply -f "$repo_root/tests/provider/percona.yaml" >/dev/null
new_uid=$(kube get perconaservermongodb documents -o jsonpath='{.metadata.uid}')
[[ $new_uid != "$source_uid" ]]
wait_for 'recreated real source becomes available' database_ready
wait_for 'old publication UID is rejected' product_ready False ConnectionOwnerMismatch
bind_publication "$new_uid"
wait_for 'new publication UID restores readiness' product_ready True SourceReady
wait_for 'recreated source still returns the same retained record' query
after_recreation=$(retained_identities)
[[ "$(jq -c 'map(select(.kind != "PerconaServerMongoDB"))' <<<"$before_recreation")" == "$(jq -c 'map(select(.kind != "PerconaServerMongoDB"))' <<<"$after_recreation")" ]]

phase 'disabled gates and independent retention' 240
for gate in PROVISIONED_SOURCES_ENABLED ENGINE_PROVIDERS_ENABLED; do
	reason=SourceFeatureDisabled
	[[ $gate != ENGINE_PROVIDERS_ENABLED ]] || reason=EngineProviderFeatureDisabled
	capture_controller_logs
	kube set env deployment/dpc "$gate=false" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'disabled gate appears in current conditions and registry' product_ready False "$reason"
	disabled_without_reads "$reason"
	query
	capture_controller_logs
	kube set env deployment/dpc "$gate=true" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	forward_registry
	wait_for 'restored gate republishes the current source' product_ready True SourceReady
done
before_delete=$(retained_identities)
kube delete dataproduct document-product --wait=false >/dev/null
wait_for 'deleted descriptor is removed from the registry' registry_empty
wait_for 'deleting only the descriptor retains queryable independent data' query
[[ $before_delete == "$(retained_identities)" ]]
echo 'PASS: source, publication and PVC identities survive DataProduct deletion'

# Fixed synthetic values must not enter either control-plane projection or controller logs.
capture_controller_logs
for marker in synthetic-reader-original synthetic-reader-replacement synthetic-writer-password persistent-document; do
	if grep -Fq "$marker" "$test_dir/controller.log" "$test_dir/products-seen.jsonl" "$test_dir/registry-seen.jsonl"; then
		echo 'control-plane output contains data-plane fixture content' >&2
		exit 1
	fi
done
echo 'PASS: complete real Percona query, privilege and lifecycle acceptance'
bounded docker stats --no-stream --format 'Owned cluster CPU={{.CPUPerc}} memory={{.MemUsage}}' "$control_node"
