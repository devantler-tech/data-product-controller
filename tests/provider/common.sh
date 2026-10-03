#!/usr/bin/env bash
# Owned mechanics for disposable real-provider acceptance; no production state.
: "${repo_root:?}" "${provider_name:?}" "${product_name:?}" "${product_id:?}" "${consumer_pod:?}" "${source_api_group:?}" "${source_resource:?}"
test_dir=$(mktemp -d)
cluster_name="dpc-${provider_name}-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-$RANDOM"
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
			kubectl --request-timeout=10s -n kube-system get pods -o wide || true
			kubectl --request-timeout=10s -n kube-system get events --sort-by=.metadata.creationTimestamp || true
			kubectl --request-timeout=10s -n kube-system logs -l k8s-app=cilium --all-containers=true --tail=100 || true
			kubectl --request-timeout=10s -n products get pods,pvc -o wide || true
			kubectl --request-timeout=10s -n products get "$source_resource" -o wide || true
			kubectl --request-timeout=10s -n products get dataproducts -o wide || true
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
	printf 'Provider acceptance finished in %ss (exit %s)\n' "$((SECONDS - started_at))" "$result"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

forward_registry() {
	if [[ -n ${registry_forward_pid:-} ]]; then
		kill "$registry_forward_pid" 2>/dev/null || true
		wait "$registry_forward_pid" 2>/dev/null || true
	fi
	registry_port=''
	wait_for 'current controller revision has one Ready registry pod' registry_target_ready
	start_registry_forward
}

capture_controller_logs() {
	# Read full logs from all current replicas before replacement. Collection
	# failure means incomplete leak-check evidence and fails acceptance.
	kube logs -l app.kubernetes.io/instance=dpc --all-containers=true --prefix=true --tail=-1 >>"$test_dir/controller.log"
}
install_controller() {
	if [[ ${controller_installed:-false} == true ]]; then
		capture_controller_logs
	fi
	bounded helm template dpc "$repo_root/charts/data-product-controller" --include-crds --namespace products \
		--set "image.repository=$1" --set "image.tag=$2" --set "image.digest=$3" --set image.pullPolicy=Never \
		--set demoProduct.enabled=false --set provisionedSources.enabled=true --set engineProviders.enabled=true >"$test_dir/controller.yaml"
	bounded kubectl --request-timeout=0 -n products apply -f "$test_dir/controller.yaml" >/dev/null
	bounded kubectl --request-timeout=0 -n products rollout status deployment/dpc --timeout="$(remaining)s"
	controller_installed=true
	forward_registry
}

product_ready() {
	local status=$1 reason=${2:-}
	kube get dataproduct "$product_name" -o json >"$test_dir/product.json" || return 1
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
		--arg id "$product_id" '.products | length == 1 and .[0].id == $id and .[0].ready == $ready' "$test_dir/registry.json" >/dev/null
}

registry_empty() {
	kill -0 "$registry_forward_pid" 2>/dev/null || return 1
	curl --fail --silent --max-time 5 "http://127.0.0.1:$registry_port/api/v1/products" >"$test_dir/registry.json" || return 1
	jq -e '.products | type == "array" and length == 0' "$test_dir/registry.json" >/dev/null
}

audit_reads() {
	docker exec "$control_node" cat /audit/log.json | jq -s '[.[] | select(.stage == "ResponseComplete" and .verb == "get")] | length'
}

disabled_without_reads() {
	local reason=$1 before after
	product_ready False "$reason" || return 1
	before=$(audit_reads) || return 1
	((before > 0)) || return 1
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

require_pinned_image() {
	local selector=$1 container=$2 image=$3 digest=$4
	kube get pods -l "$selector" -o json | jq -e --arg container "$container" \
		--arg image "$image" --arg index "${image##*@}" --arg digest "$digest" '
    .items | length == 1 and all(.[];
      ([.spec.containers[] | select(.name == $container and .image == $image)] | length == 1) and
      ([.status.containerStatuses[] | select(.name == $container and .ready == true and .state.running != null) |
        select(.imageID | endswith($index) or endswith($digest))] | length == 1))' >/dev/null
}

make_certificate() {
	local name=$1 san=$2 organization=${3:-disposable-provider}
	openssl req -newkey rsa:2048 -nodes -subj "/CN=$name/O=$organization" \
		-keyout "$test_dir/tls/$name.key" -out "$test_dir/tls/$name.csr" >/dev/null 2>&1
	printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth,clientAuth\n' "$san" >"$test_dir/tls/$name.ext"
	openssl x509 -req -days 1 -sha256 -CA "$test_dir/tls/ca.crt" -CAkey "$test_dir/tls/ca.key" -CAcreateserial \
		-in "$test_dir/tls/$name.csr" -extfile "$test_dir/tls/$name.ext" -out "$test_dir/tls/$name.crt" >/dev/null 2>&1
}

kube() { kubectl --request-timeout=15s -n products "$@"; }
query() { kube exec "$consumer_pod" -- /fixture probe "$@"; }

start_cluster() {
	phase setup 360
	mkdir "$test_dir/audit"
	cat >"$test_dir/audit/policy.yaml" <<YAML
apiVersion: audit.k8s.io/v1
kind: Policy
omitStages: [RequestReceived]
rules:
  - level: Metadata
    users: [system:serviceaccount:products:dpc]
    resources:
      - group: $source_api_group
        resources: [$source_resource, $source_resource/*]
      - group: ""
        resources: [secrets, secrets/*]
  - level: None
YAML
	bounded ksail project init --name "$cluster_name" --distribution Vanilla --provider Docker \
		--cni Cilium --gitops-engine None --policy-engine None --load-balancer Disabled \
		--metrics-server Disabled --mirror-registry '' --local-registry localhost:5055 \
		--kubeconfig "$KUBECONFIG" --output "$test_dir/cluster" --no-devcontainer
	if [[ -d $test_dir/cluster/kind/mirrors ]]; then
		echo 'unexpected public mirror configuration in the direct-pull acceptance profile' >&2
		exit 1
	fi
	export DPC_TEST_AUDIT_DIR="$test_dir/audit"
	yq -i '.nodes = [.nodes[0]] |
  .nodes[].image = "kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a" |
  .containerdConfigPatches += ["[plugins.\"io.containerd.cri.v1.images\".registry]\n  config_path = \"/etc/containerd/certs.d\""] |
  .nodes[0].extraMounts += [{"hostPath":strenv(DPC_TEST_AUDIT_DIR),"containerPath":"/audit"}] |
  .nodes[0].kubeadmConfigPatches += ["kind: ClusterConfiguration\napiServer:\n  extraArgs:\n    audit-policy-file: /audit/policy.yaml\n    audit-log-path: /audit/log.json\n  extraVolumes:\n    - name: audit\n      hostPath: /audit\n      mountPath: /audit\n      readOnly: false\n      pathType: Directory"]' "$test_dir/cluster/kind.yaml"
	cluster_started=true
	# Keep Kind's declared nodes instead of applying KSail's default node profile.
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
	require_audit_server
	kubectl --request-timeout=15s create namespace products >/dev/null
	# Only this run's Kind node resolves the disposable registry through its Docker network.
	registry_dir=/etc/containerd/certs.d/localhost_5055_
	bounded docker exec "$control_node" mkdir -p "$registry_dir"
	printf 'server = "http://%s-local-registry:5000"\n[host."http://%s-local-registry:5000"]\n  capabilities = ["pull", "resolve"]\n' "$cluster_name" "$cluster_name" |
		bounded docker exec -i "$control_node" cp /dev/stdin "$registry_dir/hosts.toml"
	bounded docker build --tag "dpc-provider:$cluster_name" "$repo_root"
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
	bounded docker save "dpc-provider:$cluster_name" >"$test_dir/images.tar"
	# Kind's containerd may have a private /tmp mount. Stream the archive into the
	# runtime's stdin instead of assuming a copied node path exists in that namespace.
	bounded docker exec -i "$control_node" ctr --namespace k8s.io images import - <"$test_dir/images.tar"
	for image in "dpc-provider:$cluster_name" "$fixture_image"; do
		bounded docker exec "$control_node" crictl inspecti "$image" >/dev/null
	done
	bounded docker exec "$control_node" crictl pull "$released_image"
	controller_image="dpc-provider:$cluster_name"
	printf 'Controller source: %s\n' "$(git -C "$repo_root" rev-parse HEAD)"
	docker image inspect "$controller_image" "$fixture_image" --format '{{.Id}}'

}
