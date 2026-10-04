#!/usr/bin/env bash
set -euo pipefail
# Resolve only the selector before prerequisites, scratch space or runtime actions.
source "${BASH_SOURCE[0]%/*}/suite.sh"
source_suite_validate

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
for command in ksail docker kubectl helm jq yq openssl cosign timeout; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done
helm_version=$(helm version --short)
[[ $helm_version =~ ^v3\.(1[3-9]|[2-9][0-9])\. ]] || {
	echo 'source acceptance requires Helm 3.13 or newer within major version 3 for executable post-rendering and release metadata' >&2
	exit 1
}

test_dir=$(mktemp -d)
cluster_name="dpc-e2e-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-$RANDOM"
export KUBECONFIG="$test_dir/kubeconfig"
cluster_config="$test_dir/cluster/ksail.yaml"
cluster_started=false
source_container="$cluster_name-source"
source_started=false
started_at=$SECONDS
integration_deadline=$((started_at + 2700))

# cleanup removes this run's source and cluster, preserving failure status and emitting only selected fixture diagnostics.
cleanup() {
	result=$?
	trap - EXIT INT TERM
	if [[ "$source_started" == true ]]; then
		docker rm --force "$source_container" >/dev/null || result=1
	fi
	if [[ "$cluster_started" == true ]]; then
		if [[ "$result" != 0 ]]; then
			# Selected diagnostics contain only synthetic workloads and public status.
			kubectl --request-timeout=10s -n products get pods,deployments -o wide || true
			kubectl --request-timeout=10s -n products get dataproducts -o wide || true
			kubectl --request-timeout=10s -n products get events --sort-by=.metadata.creationTimestamp || true
			kubectl --request-timeout=10s -n products logs deployment/dpc --tail=80 || true
			kubectl --request-timeout=10s -n kube-system get pods -o wide || true
		fi
		ksail cluster delete --name "$cluster_name" --provider Docker --kubeconfig "$KUBECONFIG" \
			--config "$cluster_config" --force --delete-storage || result=1
	fi
	rm -rf "$test_dir"
	echo "source integration finished in $((SECONDS - started_at)) seconds (exit $result)"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# wait_for retries one assertion until its allowance or the absolute integration deadline expires.
wait_for() {
	local description=$1 timeout=$2
	shift 2
	local deadline=$((SECONDS + timeout))
	((deadline <= integration_deadline)) || deadline=$integration_deadline
	while :; do
		if ((SECONDS >= deadline)); then
			echo "timed out: $description" >&2
			[[ ! -f "$test_dir/wait.log" ]] || cat "$test_dir/wait.log" >&2
			return 1
		fi
		if "$@" >"$test_dir/wait.log" 2>&1; then
			((SECONDS < deadline)) || {
				echo "timed out: $description" >&2
				return 1
			}
			echo "PASS: $description"
			return 0
		fi
		sleep 2
	done
}

# kube scopes requests to the fixture namespace and bounds each API request.
kube() { kubectl --request-timeout=15s -n products "$@"; }
# probe makes a bounded request from the authorized consumer so assertions exercise cluster networking.
probe() { kube exec consumer -- /fixture probe --timeout 10s "$@"; }
# registry_ready selects one exact fixture descriptor instead of accepting another product's readiness.
registry_ready() { probe --url http://dpc/api/v1/products --registry-product "products/${2:-existing-export}" --registry-ready "$1"; }
# Probe every endpoint directly: a successful Service request can hide a non-serving follower.
registry_replicas_ready() {
	local addresses address
	kube get deployment/dpc -o json >"$test_dir/registry-deployment.json" || return 1
	kube get replicasets -o json >"$test_dir/registry-replicasets.json" || return 1
	addresses=$(kube get pods -l app.kubernetes.io/component=controller -o json |
		jq -er --slurpfile deployment "$test_dir/registry-deployment.json" --slurpfile replicasets "$test_dir/registry-replicasets.json" '
    $deployment[0] as $d | ($d.metadata.annotations["deployment.kubernetes.io/revision"]) as $revision |
    [$replicasets[0].items[] | select(.metadata.deletionTimestamp == null and .metadata.annotations["deployment.kubernetes.io/revision"] == $revision) |
      select(any(.metadata.ownerReferences[]?; .uid == $d.metadata.uid and .controller == true))] as $rs |
    if ($revision | type) != "string" or ($revision | test("^[1-9][0-9]*$") | not) or
      $d.status.observedGeneration != $d.metadata.generation or $d.spec.replicas != 2 or
      $d.status.replicas != 2 or $d.status.updatedReplicas != 2 or $d.status.readyReplicas != 2 or
      $d.status.availableReplicas != 2 or ($rs | length) != 1 then error("registry rollout incomplete")
    else [.items[] | select(.metadata.deletionTimestamp == null) |
      select(any(.metadata.ownerReferences[]?; .uid == $rs[0].metadata.uid and .controller == true)) |
      select(any(.status.conditions[]?; .type == "Ready" and .status == "True")) |
      select(any(.status.containerStatuses[]?; .name == "controller" and .ready == true)) |
      .status.podIP // empty] | if length == 2 then .[] else error("expected two current ready registry endpoints") end end') || return 1
	while IFS= read -r address; do
		probe --url "http://$address:8082/api/v1/products" --contains '"products":' || return 1
		probe --url "http://$address:8082/" --contains '<title>Data products</title>' || return 1
		probe --url "http://$address:8082/api/v1/ui-config" --contains '"uiContractEnabled":false,"uiAppearanceEnabled":false' || return 1
	done <<<"$addresses"
}
# conditions requires both connector and aggregate conditions to match the product's current generation.
conditions() {
	local status=$1 reason=${2:-}
	kube get dataproduct existing-export -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    [.status.conditions[]? | select(.type == "Ready" or .type == "ConnectorReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation and
        ($reason == "" or .reason == $reason))] | length == 2' >/dev/null
}
# readiness joins Kubernetes conditions with the selected registry descriptor's reported readiness.
readiness() {
	conditions "$1" "${3:-}" && registry_ready "$2"
}
# source_secret updates only the synthetic projected credential while keeping the fixture's HTTPS endpoint fixed.
source_secret() {
	jq -n --arg token "$1" '{endpointURL:"https://source.products.svc.cluster.local/export",bearerToken:$token}' \
		>"$test_dir/config.json"
	kube create secret generic existing-export --from-file=config.json="$test_dir/config.json" \
		--dry-run=client -o yaml | kube apply -f - >/dev/null
}
source "$repo_root/tests/source/helm-lifecycle.sh"
source "$repo_root/tests/source/source-lifecycle.sh"
source "$repo_root/tests/source/contract-matrix.sh"
source "$repo_root/tests/source/connector-matrix.sh"
source "$repo_root/tests/source/engine-helpers.sh"
# source_pod returns the sole nondeleting connector Pod UID, rejecting ambiguous rollout states.
source_pod() {
	kube get pods -l app.kubernetes.io/component=http-source -o json |
		jq -er '.items | map(select(.metadata.deletionTimestamp == null)) | if length == 1 then .[0].metadata.uid else error("expected one connector Pod") end'
}
# contract_readiness requires a healthy connector alongside matching current contract, aggregate and registry readiness.
contract_readiness() {
	local status=$1 reason=$2
	kube get dataproduct existing-export -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    any(.status.conditions[]?; .type == "ConnectorReady" and .status == "True" and .observedGeneration == $product.metadata.generation) and
    ([.status.conditions[]? | select(.type == "Ready" or .type == "ContractsReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation)] | length == 2) and
    any(.status.conditions[]?; .type == "ContractsReady" and .reason == $reason)' >/dev/null &&
		registry_ready "$(if [[ "$status" == True ]]; then echo true; else echo false; fi)"
}
# independent_resource_uids records the existing connector and Secret only when neither is deleting or owned by the product.
independent_resource_uids() {
	kube get deployment/dpc-http-source secret/existing-export -o json |
		jq -ceS --arg product_uid "$1" '
    if (.items | length) == 2 and all(.items[];
      .metadata.uid != null and .metadata.uid != "" and .metadata.deletionTimestamp == null and
      all(.metadata.ownerReferences[]?; .uid != $product_uid))
    then [.items[] | {key: (.kind + "/" + .metadata.name), value: .metadata.uid}] | from_entries
    else error("connector and credentials must exist independently of the product without pending deletion") end'
}
# disabled_export targets a disabled replica directly and requires refusal on both data and readiness endpoints.
disabled_export() {
	local address
	address=$(kube get pods -l app.kubernetes.io/component=http-source -o json | jq -er '
    [.items[] | select(.metadata.deletionTimestamp == null) |
      select(any(.spec.containers[].env[]?; .name == "HTTP_SOURCE_ENABLED" and .value == "false")) |
      .status.podIP // empty] | first // error("disabled Pod not running")')
	probe --url "http://$address:8080/api/data" --want-status 404 &&
		probe --url "http://$address:8081/readyz" --want-status 503
}

ksail project init --name "$cluster_name" --distribution Vanilla --provider Docker \
	--cni Cilium --gitops-engine None --policy-engine None --load-balancer Disabled \
	--metrics-server Disabled --local-registry localhost:5055 --kubeconfig "$KUBECONFIG" \
	--output "$test_dir/cluster" --no-devcontainer
cluster_started=true
ksail cluster create --config "$cluster_config" --distribution-config "$test_dir/cluster/kind.yaml"
cluster_context=$(kubectl config current-context)
[[ -n "$cluster_context" ]] || {
	echo 'owned fixture context missing' >&2
	exit 1
}
kubectl --request-timeout=0 -n kube-system rollout status daemonset/cilium --timeout=180s
[[ -z "$(kubectl --request-timeout=15s -n kube-system get daemonset kindnet --ignore-not-found -o name)" ]] || {
	echo 'unexpected default Kind CNI alongside Cilium' >&2
	exit 1
}
echo 'PASS: cluster uses the generated Cilium configuration'
kubectl --request-timeout=15s create namespace products

# A node's localhost is not the host's registry endpoint. Configure only this
# cluster's nodes using Kind's documented containerd alias for a local registry.
# https://kind.sigs.k8s.io/docs/user/local-registry/
cluster_nodes=$(docker ps --filter "label=io.x-k8s.kind.cluster=$cluster_name" --format '{{.Names}}')
[[ -n "$cluster_nodes" ]] || {
	echo 'missing owned Kind nodes' >&2
	exit 1
}
registry_dir=/etc/containerd/certs.d/localhost:5055
while IFS= read -r node; do
	docker exec "$node" mkdir -p "$registry_dir"
	printf '[host."http://%s-local-registry:5000"]\n' "$cluster_name" |
		docker exec -i "$node" cp /dev/stdin "$registry_dir/hosts.toml"
done <<<"$cluster_nodes"

# No GHCR write or release credentials: both images exist only in this cluster's registry.
docker build --tag localhost:5055/data-product-controller:e2e "$repo_root"
if source_suite_includes lifecycle; then
	bash "$repo_root/tests/source/dsp-catalog.sh" localhost:5055/data-product-controller:e2e
	bash "$repo_root/tests/source/ui-kit.sh" localhost:5055/data-product-controller:e2e
	bash "$repo_root/tests/source/product-check.sh" localhost:5055/data-product-controller:e2e
fi
docker push localhost:5055/data-product-controller:e2e
docker build --file "$repo_root/tests/source/fixture/Dockerfile" \
	--tag localhost:5055/source-fixture:e2e "$repo_root"
docker push localhost:5055/source-fixture:e2e
product_digest=$(docker image inspect localhost:5055/data-product-controller:e2e --format '{{index .RepoDigests 0}}')
product_digest=${product_digest##*@}
export DPC_FIXTURE_IMAGE
DPC_FIXTURE_IMAGE=$(docker image inspect localhost:5055/source-fixture:e2e --format '{{index .RepoDigests 0}}')
[[ "$product_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || {
	echo 'missing pushed product digest' >&2
	exit 1
}
[[ "$DPC_FIXTURE_IMAGE" =~ @sha256:[a-f0-9]{64}$ ]] || {
	echo 'missing pushed fixture digest' >&2
	exit 1
}
while IFS= read -r node; do
	docker exec "$node" crictl pull "$DPC_FIXTURE_IMAGE"
done <<<"$cluster_nodes"
echo 'PASS: node runtime pulls the immutable fixture through the local registry alias'

mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 1 \
	-subj /CN=source.products.svc.cluster.local \
	-addext subjectAltName=DNS:source.products.svc.cluster.local \
	-keyout "$test_dir/tls/tls.key" -out "$test_dir/tls/tls.crt" >/dev/null 2>&1
# These generated fixture files contain no operator credentials. The nonroot
# source container needs read access through its read-only bind mount.
chmod 0444 "$test_dir/tls/tls.key" "$test_dir/tls/tls.crt"
kube create configmap synthetic-source-ca --from-file=ca.crt="$test_dir/tls/tls.crt"
# Cilium's CIDR rules match external identities, not managed cluster Pods.
# Keep this source outside Kubernetes while retaining private Kind networking.
source_started=true
docker run --detach --name "$source_container" --network kind --read-only \
	--cap-drop ALL --cap-add NET_BIND_SERVICE --security-opt no-new-privileges:true \
	--memory 64m --cpus 1 --mount "type=bind,src=$test_dir/tls,dst=/tls,readonly" \
	"$DPC_FIXTURE_IMAGE" serve >/dev/null
wait_for 'external TLS source starts' 60 docker exec "$source_container" /fixture probe \
	--url http://127.0.0.1:9000/healthz --timeout 3s
export DPC_SOURCE_IP
DPC_SOURCE_IP=$(docker inspect --format '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$source_container")
[[ "$DPC_SOURCE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
	echo 'missing external source IPv4 address' >&2
	exit 1
}
yq 'with(select(.kind == "EndpointSlice"); .endpoints[0].addresses = [strenv(DPC_SOURCE_IP)])' \
	"$repo_root/tests/source/source.yaml" | kube apply -f -
yq 'with(select(.kind == "Pod"); .spec.containers[0].image = strenv(DPC_FIXTURE_IMAGE))' \
	"$repo_root/tests/source/consumer.yaml" | kube apply -f -
yq 'select(.kind == "Pod") | .spec.containers[0].image = strenv(DPC_FIXTURE_IMAGE) |
  .metadata.name = "outsider" | .metadata.labels.app = "outsider"' \
	"$repo_root/tests/source/consumer.yaml" | kube apply -f -
kube --request-timeout=0 wait pod/consumer pod/outsider --for=condition=Ready --timeout=180s
source_secret fixture-token-a
jq -n --arg digest "$product_digest" --arg cidr "$DPC_SOURCE_IP/32" '{
  image:{repository:"localhost:5055/data-product-controller",digest:$digest,pullPolicy:"IfNotPresent"},
  controller:{replicas:2},
  demoProduct:{enabled:false},
  connectorReadiness:{enabled:false},
  httpSource:{enabled:false,secretName:"existing-export",sourceCIDR:$cidr,
    consumerPodLabels:{app:"source-consumer"},monitorPodLabels:{app:"source-consumer"}}
}' >"$test_dir/values.yaml"

mkdir "$test_dir/candidate-chart"
helm package "$repo_root/charts/data-product-controller" --destination "$test_dir/candidate-chart" >/dev/null
candidate_chart="$test_dir/candidate-chart/data-product-controller-$(helm show chart "$repo_root/charts/data-product-controller" | yq -r '.version').tgz"
if source_suite_includes lifecycle; then
	helm_release_lifecycle

	install_chart
	kube get deployments -l app.kubernetes.io/component=http-source -o json | jq -e '.items | length == 0' >/dev/null
	echo 'PASS: HTTP source workload absent by default'
	install_chart --set httpSource.enabled=true
	kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
	kube --request-timeout=0 rollout status deployment/dpc-http-source --timeout=240s
	kube --request-timeout=0 wait crd/dataproducts.data.devantler.tech --for=condition=Established --timeout=60s
	source "$repo_root/tests/source/ui-appearance.sh"
	yq '.spec.connector.resourceRef.name = "dpc-http-source"' "$repo_root/docs/examples/http-source-product.yaml" | kube apply -f -
	wait_for 'observation disabled in conditions and registry' 180 readiness False false ConnectorFeatureDisabled
	wait_for 'both leader-elected replicas serve the default workspace, descriptors and disabled optional grants' 120 registry_replicas_ready

	install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true
	kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
	wait_for 'observation requires its exact-resource grant' 180 readiness False false ConnectorAccessDenied
	yq 'with(select(.kind == "Role"); .rules[0].resourceNames = ["dpc-http-source"]) |
	  with(select(.kind == "RoleBinding"); .subjects[0].name = "dpc" | .subjects[0].namespace = "products")' \
		"$repo_root/docs/examples/connector-observer-rbac.yaml" >"$test_dir/observer-rbac.yaml"
	kube apply -f "$test_dir/observer-rbac.yaml"
	wait_for 'healthy source reaches product and registry readiness' 240 readiness True true
	wait_for 'authorized consumer reads the real export' 120 probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
	probe --url http://dpc-http-source/openapi.json --contains '"openapi"'

	source "$repo_root/tests/source/installed-matrix.sh"
	installed_lifecycle_matrix
	source "$repo_root/tests/source/composition.sh"
	source "$repo_root/tests/source/catalog.sh"
else
	# Each provider job starts with the candidate controller and an empty inventory.
	# Keep the common CA/Secret fixture for the SQL module's HTTP flag assertion.
	install_chart
	kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
	kube --request-timeout=0 wait crd/dataproducts.data.devantler.tech --for=condition=Established --timeout=60s
fi
if source_suite_includes sql; then source "$repo_root/tests/source/engine-provider.sh"; fi
if source_suite_includes document; then source "$repo_root/tests/source/percona-provider.sh"; fi
if source_suite_includes graph; then source "$repo_root/tests/source/arango-provider.sh"; fi
