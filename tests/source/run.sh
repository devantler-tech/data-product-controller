#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
for command in ksail docker kubectl helm jq yq openssl; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done

test_dir=$(mktemp -d)
cluster_name="dpc-e2e-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-$RANDOM"
export KUBECONFIG="$test_dir/kubeconfig"
cluster_config="$test_dir/cluster/ksail.yaml"
cluster_started=false
source_container="$cluster_name-source"
source_started=false
started_at=$SECONDS

cleanup() {
	result=$?
	trap - EXIT INT TERM
	if [[ "$source_started" == true ]]; then
		docker rm --force "$source_container" >/dev/null || result=1
	fi
	if [[ "$cluster_started" == true ]]; then
		if [[ "$result" != 0 ]]; then
			# Selected diagnostics contain only synthetic workloads and public status.
			kubectl --request-timeout=10s -n products get pods,deployments,dataproducts -o wide || true
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

wait_for() {
	local description=$1 timeout=$2
	shift 2
	local deadline=$((SECONDS + timeout))
	until "$@" >"$test_dir/wait.log" 2>&1; do
		if ((SECONDS >= deadline)); then
			echo "timed out: $description" >&2
			cat "$test_dir/wait.log" >&2
			return 1
		fi
		sleep 2
	done
	echo "PASS: $description"
}

kube() { kubectl --request-timeout=15s -n products "$@"; }
probe() { kube exec consumer -- /fixture probe --timeout 10s "$@"; }
registry_ready() { probe --url http://dpc/api/v1/products --contains "\"ready\":$1"; }
conditions() {
	local status=$1 reason=${2:-}
	kube get dataproduct existing-export -o json | jq -e --arg status "$status" --arg reason "$reason" '
    . as $product |
    [.status.conditions[]? | select(.type == "Ready" or .type == "ConnectorReady") |
      select(.status == $status and .observedGeneration == $product.metadata.generation and
        ($reason == "" or .reason == $reason))] | length == 2' >/dev/null
}
readiness() {
	conditions "$1" "${3:-}" && registry_ready "$2"
}
source_secret() {
	jq -n --arg token "$1" '{endpointURL:"https://source.products.svc.cluster.local/export",bearerToken:$token}' \
		>"$test_dir/config.json"
	kube create secret generic existing-export --from-file=config.json="$test_dir/config.json" \
		--dry-run=client -o yaml | kube apply -f - >/dev/null
}
install_chart() {
	helm template dpc "$repo_root/charts/data-product-controller" --include-crds \
		--namespace products --values "$test_dir/values.yaml" "$@" |
		"$repo_root/tests/source/trust-test-ca.sh" | kube apply -f - >/dev/null
}
source_pod() {
	kube get pods -l app.kubernetes.io/component=http-source -o json |
		jq -er '.items | map(select(.metadata.deletionTimestamp == null)) | if length == 1 then .[0].metadata.uid else error("expected one connector Pod") end'
}
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
ksail cluster create --config "$cluster_config" --ttl 30m
kubectl --request-timeout=0 -n kube-system rollout status daemonset/cilium --timeout=180s
kubectl --request-timeout=15s create namespace products

# No GHCR write or release credentials: both images exist only in this cluster's registry.
docker build --tag localhost:5055/data-product-controller:e2e "$repo_root"
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
yq '.spec.containers[0].image = strenv(DPC_FIXTURE_IMAGE)' \
	"$repo_root/tests/source/consumer.yaml" | kube apply -f -
yq '.spec.containers[0].image = strenv(DPC_FIXTURE_IMAGE) | .metadata.name = "outsider" | .metadata.labels.app = "outsider"' \
	"$repo_root/tests/source/consumer.yaml" | kube apply -f -
kube --request-timeout=0 wait pod/consumer pod/outsider --for=condition=Ready --timeout=180s
source_secret fixture-token-a
jq -n --arg digest "$product_digest" --arg cidr "$DPC_SOURCE_IP/32" '{
  image:{repository:"localhost:5055/data-product-controller",digest:$digest,pullPolicy:"IfNotPresent"},
  demoProduct:{enabled:false},
  connectorReadiness:{enabled:false},
  httpSource:{enabled:false,secretName:"existing-export",sourceCIDR:$cidr,
    consumerPodLabels:{app:"source-consumer"},monitorPodLabels:{app:"source-consumer"}}
}' >"$test_dir/values.yaml"

install_chart
kube get deployments -l app.kubernetes.io/component=http-source -o json | jq -e '.items | length == 0' >/dev/null
echo 'PASS: HTTP source workload absent by default'
install_chart --set httpSource.enabled=true
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
kube --request-timeout=0 rollout status deployment/dpc-http-source --timeout=240s
kube --request-timeout=0 wait crd/dataproducts.data.devantler.tech --for=condition=Established --timeout=60s
yq '.spec.connector.resourceRef.name = "dpc-http-source"' "$repo_root/docs/examples/http-source-product.yaml" | kube apply -f -
wait_for 'observation disabled in conditions and registry' 180 readiness False false ConnectorFeatureDisabled

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
connector_uid=$(source_pod)
wait_for 'unauthorized consumer is denied by NetworkPolicy' 90 kube exec outsider -- /fixture probe \
	--url http://dpc-http-source/api/data --want-error --timeout 5s
kube label pod outsider app=source-consumer --overwrite
wait_for 'the same consumer succeeds with the authorized label' 90 kube exec outsider -- /fixture probe \
	--url http://dpc-http-source/api/data --contains '"fixture":"source"' --timeout 5s
kube label pod outsider app=outsider --overwrite
wait_for 'removing the label restores denial for the same consumer' 90 kube exec outsider -- /fixture probe \
	--url http://dpc-http-source/api/data --want-error --timeout 5s
probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'

docker exec "$source_container" /fixture control down
wait_for 'source outage reaches product and registry readiness' 240 readiness False false
probe --url http://dpc-http-source-metrics:8081/metrics --contains 'http_source_ready 0'
docker exec "$source_container" /fixture control healthy
wait_for 'source recovery reaches product and registry readiness' 240 readiness True true

kube delete role export-connector-observer
wait_for 'revoked observation access is reflected without a restart' 180 readiness False false ConnectorAccessDenied
kube apply -f "$test_dir/observer-rbac.yaml"
wait_for 'observation access recovery' 180 readiness True true

docker exec "$source_container" /fixture control rotated
wait_for 'rotated upstream credential rejects the old projected token' 240 readiness False false
source_secret fixture-token-b
wait_for 'projected Secret rotation recovers the source' 300 readiness True true
probe --url http://dpc-http-source/api/data --contains '"fixture":"source"'
[[ "$(source_pod)" == "$connector_uid" ]] || {
	echo 'credential rotation replaced the connector Pod' >&2
	exit 1
}
echo 'PASS: credential rotation retained the connector Pod'

# RollingUpdate may retain the old ready replica. Inspect the disabled replica
# directly so an old serving Pod cannot falsely prove the flag's behavior.
kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=false
wait_for 'disabled export replica refuses data and readiness' 180 disabled_export
wait_for 'incomplete disabled rollout makes the product unavailable' 180 readiness False false
kube set env deployment/dpc-http-source HTTP_SOURCE_ENABLED=true
kube --request-timeout=0 rollout status deployment/dpc-http-source --timeout=240s
wait_for 're-enabled export recovers the complete rollout' 240 readiness True true

kube delete dataproduct existing-export
wait_for 'deleted product disappears from the registry' 120 probe --url http://dpc/api/v1/products --contains '"products":[]'
kube get deployment/dpc-http-source secret/existing-export -o name
docker exec "$source_container" /fixture probe --url http://127.0.0.1:9000/healthz --timeout 3s
echo 'PASS: product deletion retained independently owned workloads and credentials'
