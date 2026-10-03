#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
mkdir "$test_dir/bin"
touch "$test_dir/kubeconfig"
index=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
runtime=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
export OBSERVER_FIXTURES="$test_dir" OBSERVER_IMAGE="example.invalid/dpc@$index" OBSERVER_RUNTIME="$runtime"

# Kubernetes is the external boundary. These complete synthetic API objects also
# carry fields that must never survive the observer's metadata projection.
cat >"$test_dir/objects.json" <<'JSON'
{
  "product": {"apiVersion":"data.devantler.tech/v1alpha1","kind":"DataProduct","metadata":{"name":"export","namespace":"products","uid":"product-uid","generation":4},"spec":{"private":"DO_NOT_RETAIN"},"status":{"conditions":[{"type":"Ready","status":"True","observedGeneration":4,"message":"DO_NOT_RETAIN"},{"type":"ConnectorReady","status":"True","observedGeneration":4}] }},
  "deployment": {"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"dpc","namespace":"products","uid":"deployment-uid","generation":7,"annotations":{"deployment.kubernetes.io/revision":"3","private":"DO_NOT_RETAIN"}},"spec":{"replicas":2,"selector":{"matchLabels":{"app":"dpc"}},"template":{"spec":{"containers":[{"name":"controller","image":"IMAGE","env":[{"name":"PRIVATE","value":"DO_NOT_RETAIN"}]}]}}},"status":{"observedGeneration":7,"replicas":2,"updatedReplicas":2,"readyReplicas":2,"availableReplicas":2}},
  "replicasets": {"apiVersion":"apps/v1","kind":"ReplicaSetList","items":[{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"dpc-current","namespace":"products","uid":"rs-current","generation":3,"annotations":{"deployment.kubernetes.io/revision":"3"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"dpc","uid":"deployment-uid","controller":true}]},"spec":{"replicas":2,"template":{"spec":{"containers":[{"name":"controller","image":"IMAGE"}]}}},"status":{"observedGeneration":3,"replicas":2,"readyReplicas":2,"availableReplicas":2}},{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"dpc-old","namespace":"products","uid":"rs-old","generation":2,"annotations":{"deployment.kubernetes.io/revision":"2"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"dpc","uid":"deployment-uid","controller":true}]},"spec":{"replicas":0},"status":{"observedGeneration":2,"replicas":0}}]},
  "pods": {"apiVersion":"v1","kind":"PodList","items":[{"apiVersion":"v1","kind":"Pod","metadata":{"name":"dpc-one","namespace":"products","uid":"pod-one","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"dpc-current","uid":"rs-current","controller":true}]},"spec":{"containers":[{"name":"controller","image":"IMAGE","env":[{"name":"PRIVATE","value":"DO_NOT_RETAIN"}]}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"controller","ready":true,"image":"IMAGE","imageID":"containerd://RUNTIME","state":{"running":{"startedAt":"2026-01-01T00:00:00Z"}}}]}},{"apiVersion":"v1","kind":"Pod","metadata":{"name":"dpc-two","namespace":"products","uid":"pod-two","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"dpc-current","uid":"rs-current","controller":true}]},"spec":{"containers":[{"name":"controller","image":"IMAGE"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"controller","ready":true,"image":"IMAGE","imageID":"containerd://RUNTIME","state":{"running":{}}}]}}]}
}
JSON
# kubectl's printer wraps namespaced inventories in a generic v1 List.
jq --arg image "$OBSERVER_IMAGE" --arg runtime "$runtime" '.replicasets.apiVersion="v1" | .replicasets.kind="List" | .pods.kind="List" |
  walk(if type == "string" then if . == "IMAGE" then $image elif . == "containerd://RUNTIME" then "containerd://" + $runtime else . end else . end)' "$test_dir/objects.json" >"$test_dir/base.json"

cat >"$test_dir/bin/kubectl" <<'BASH'
#!/usr/bin/env bash
set -euo pipefail
[[ $# -ge 11 && $1 == --kubeconfig && $2 == "$OBSERVER_FIXTURES/kubeconfig" && $3 == --context && $4 == fixture && $5 == --namespace && $6 == products && $7 == --request-timeout && $8 =~ ^[1-9][0-9]*s$ && $9 == get ]] || exit 90
shift 9
resource=$1
shift
case "$resource" in
  dataproducts.data.devantler.tech) [[ $1 == export ]]; key=product; shift ;;
  deployments.apps) [[ $1 == dpc ]]; key=deployment; shift ;;
  replicasets.apps) key=replicasets ;;
  pods) key=pods ;;
  *) exit 91 ;;
esac
[[ $# == 2 && $1 == -o && $2 == json ]] || exit 92
count_file="$OBSERVER_FIXTURES/$OBSERVER_CASE-$key.count"
count=0
[[ ! -f $count_file ]] || read -r count < "$count_file"
count=$((count + 1))
printf '%s\n' "$count" > "$count_file"
if [[ $OBSERVER_CASE == forbidden && $key == product ]]; then printf 'DO_NOT_RETAIN: forbidden\n' >&2; exit 1; fi
if [[ $OBSERVER_CASE == hung && $key == product ]]; then
  trap '' TERM
  printf '%s\n' "$$" > "$OBSERVER_FIXTURES/hung-read.pid"
  sleep 10
fi
if [[ $OBSERVER_CASE == slow_snapshot ]]; then sleep 1; fi
if [[ $OBSERVER_CASE == empty && $key == pods ]]; then exit 0; fi
filter='.'
case "$OBSERVER_CASE" in
  typed_inventory) filter='.replicasets.kind="ReplicaSetList" | .replicasets.apiVersion="apps/v1" | .pods.kind="PodList"' ;;
  wrong_inventory) filter='.replicasets.kind="DeploymentList"' ;;
  wrong_pod_item) filter='.pods.items[0].kind="Secret"' ;;
  wrong_rs_item) filter='.replicasets.items[0].apiVersion="v1"' ;;
  stale_product) filter='.product.status.conditions[0].observedGeneration=3' ;;
  duplicate_condition) filter='.product.status.conditions += [.product.status.conditions[0]]' ;;
  missing_condition) filter='.product.status.conditions |= map(select(.type != "ConnectorReady"))' ;;
  zero_replicas) filter='.deployment.spec.replicas=0 | .deployment.status.replicas=0 | .deployment.status.updatedReplicas=0 | .deployment.status.readyReplicas=0 | .deployment.status.availableReplicas=0' ;;
  stale_deployment) filter='.deployment.status.observedGeneration=6' ;;
  partial_replicas) filter='.deployment.status.readyReplicas=1' ;;
  missing_runtime) filter='.pods.items[0].status.containerStatuses[0].imageID=""' ;;
  wrong_runtime) filter='.pods.items[0].status.containerStatuses[0].imageID="containerd://sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"' ;;
  garbage_runtime) filter='.pods.items[0].status.containerStatuses[0].imageID="garbage-" + .pods.items[0].status.containerStatuses[0].imageID' ;;
  wrong_container) filter='.pods.items[0].status.containerStatuses[0].name="other"' ;;
  wrong_image) filter='.pods.items[0].spec.containers[0].image="example.invalid/dpc:mutable"' ;;
  unready_pod) filter='.pods.items[0].status.conditions[0].status="False"' ;;
  duplicate_pod_ready) filter='.pods.items[0].status.conditions += [.pods.items[0].status.conditions[0]]' ;;
  terminating_pod) filter='.pods.items[0].metadata.deletionTimestamp="2026-01-01T00:00:00Z"' ;;
  foreign_pod) filter='.pods.items[0].metadata.ownerReferences[0].uid="foreign-rs"' ;;
  old_pods) filter='.pods.items[].metadata.ownerReferences[0] |= (.uid="rs-old" | .name="dpc-old")' ;;
  extra_old_pod) filter='.pods.items += [(.pods.items[0] | .metadata.name="old-pod" | .metadata.uid="old-pod-uid" | .metadata.ownerReferences[0].uid="rs-old" | .metadata.ownerReferences[0].name="dpc-old")]' ;;
  missing_rs) filter='.replicasets.items |= map(select(.metadata.uid != "rs-current"))' ;;
  duplicate_rs) filter='.replicasets.items += [(.replicasets.items[0] | .metadata.uid="other-rs" | .metadata.name="other-current")]' ;;
  stale_rs) filter='.replicasets.items[0].status.observedGeneration=2' ;;
  invalid_running) filter='.pods.items[0].status.containerStatuses[0].state.running="not-a-state"' ;;
  foreign_rs) filter='.replicasets.items[0].metadata.ownerReferences[0].uid="foreign-deployment"' ;;
  empty_pods) filter='.pods.items=[]' ;;
  foreign_inventory) filter='.pods.items += [(.pods.items[0] | .metadata.name="unrelated" | .metadata.uid="unrelated-uid" | .metadata.ownerReferences[0].uid="unrelated-rs")]' ;;
  index_runtime) filter='.pods.items[].status.containerStatuses[0].imageID="containerd://sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' ;;
  pullable_runtime) filter='.pods.items[].status.containerStatuses[0].imageID="docker-pullable://example.invalid/dpc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"' ;;
  reconfigured) [[ $count == 1 || $key != deployment ]] || filter='.deployment.metadata.generation=8' ;;
  recreated) [[ $count == 1 || $key != deployment ]] || filter='.deployment.metadata.uid="replacement-uid"' ;;
  revised) [[ $count == 1 || $key != deployment ]] || filter='.deployment.metadata.annotations["deployment.kubernetes.io/revision"]="4"' ;;
  product_recreated) [[ $count == 1 || $key != product ]] || filter='.product.metadata.uid="replacement-product"' ;;
  product_changed) [[ $count == 1 || $key != product ]] || filter='.product.metadata.generation=5' ;;
  product_failed) [[ $count == 1 || $key != product ]] || filter='.product.status.conditions[0].status="False"' ;;
esac
jq "$filter | .$key" "$OBSERVER_FIXTURES/base.json"
if [[ $OBSERVER_CASE == extra_response && $key == pods ]]; then jq '.pods' "$OBSERVER_FIXTURES/base.json"; fi
BASH
chmod +x "$test_dir/bin/kubectl"
export PATH="$test_dir/bin:$PATH"

run_observer() {
	local scenario=$1 expected=$2
	export OBSERVER_CASE=$scenario
	local output="$test_dir/$scenario.out" status=0 invocation_timeout=1
	case "$scenario" in ready | typed_inventory | foreign_inventory | index_runtime | pullable_runtime | reconfigured | recreated | revised | product_recreated | product_changed | product_failed) invocation_timeout=5 ;; hung | slow_snapshot) invocation_timeout=2 ;; esac
	bash "$repo_root/scripts/observe-rollout.sh" \
		--kubeconfig "$test_dir/kubeconfig" --context fixture --namespace products \
		--product export --deployment dpc:controller --condition Ready --condition ConnectorReady \
		--image "$OBSERVER_IMAGE" --runtime-digest "$runtime" --timeout "$invocation_timeout" \
		--evidence-dir "$test_dir/evidence-$scenario" >"$output" 2>"$test_dir/$scenario.err" || status=$?
	if [[ $expected == success ]]; then
		[[ $status == 0 ]] || {
			printf 'FAIL %s: expected complete rollout, exit %s\n' "$scenario" "$status"
			cat "$output"
			exit 1
		}
		jq -e '.complete == true and .products == 1 and .deployments == 1 and .pods == 2' "$output" >/dev/null
		[[ -s "$test_dir/evidence-$scenario/product-0-final.json" && -s "$test_dir/evidence-$scenario/deployment-0-final.json" ]]
		if grep -Rq DO_NOT_RETAIN "$test_dir/evidence-$scenario" "$output" "$test_dir/$scenario.err"; then
			printf 'FAIL %s: private fields retained\n' "$scenario"
			exit 1
		fi
		[[ $(stat -c '%a' "$test_dir/evidence-$scenario" 2>/dev/null || stat -f '%Lp' "$test_dir/evidence-$scenario") == 700 ]]
		local evidence
		for evidence in "$test_dir/evidence-$scenario"/*; do
			[[ $(stat -c '%a' "$evidence" 2>/dev/null || stat -f '%Lp' "$evidence") == 600 ]]
		done
	else
		[[ $status != 0 ]] || {
			printf 'FAIL %s: incomplete rollout passed\n' "$scenario"
			exit 1
		}
		jq -e '.complete == false and (.failure | type == "string")' "$output" >/dev/null
		case "$scenario" in
		reconfigured | recreated | revised | product_recreated | product_changed | product_failed) jq -e '.failure == "rollout_changed"' "$output" >/dev/null ;;
		hung | slow_snapshot) jq -e '.failure == "deadline_exceeded"' "$output" >/dev/null ;;
		esac
		if grep -Rq DO_NOT_RETAIN "$test_dir/evidence-$scenario" "$output" "$test_dir/$scenario.err"; then
			printf 'FAIL %s: private failure output retained\n' "$scenario"
			exit 1
		fi
	fi
	printf 'PASS %s\n' "$scenario"
}

run_observer ready success
run_observer typed_inventory success
run_observer foreign_inventory success
run_observer index_runtime success
run_observer pullable_runtime success
for scenario in wrong_inventory wrong_pod_item wrong_rs_item extra_response invalid_running stale_product duplicate_condition missing_condition zero_replicas stale_deployment partial_replicas missing_runtime wrong_runtime garbage_runtime wrong_container wrong_image unready_pod duplicate_pod_ready terminating_pod foreign_pod old_pods extra_old_pod missing_rs duplicate_rs stale_rs foreign_rs empty_pods forbidden empty reconfigured recreated revised product_recreated product_changed product_failed; do
	run_observer "$scenario" failure
done
started=$(date +%s)
run_observer hung failure
[[ $(($(date +%s) - started)) -le 3 ]] || {
	printf 'FAIL hung: invocation deadline was not enforced\n'
	exit 1
}
read -r hung_pid <"$test_dir/hung-read.pid"
if kill -0 "$hung_pid" 2>/dev/null; then
	kill -KILL "$hung_pid" 2>/dev/null || true
	printf 'FAIL hung: Kubernetes read survived the invocation deadline\n'
	exit 1
fi
started=$(date +%s)
run_observer slow_snapshot failure
[[ $(($(date +%s) - started)) -le 3 ]] || {
	printf 'FAIL slow_snapshot: call sequence reset the deadline\n'
	exit 1
}
printf 'Rollout observer behavioral tests passed.\n'
