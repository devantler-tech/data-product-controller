#!/usr/bin/env bash
set -euo pipefail

# Do not source provider/common.sh: it owns a cluster lifecycle and exit traps.
umask 077
script_dir=$(cd "$(dirname "$0")" && pwd)
products=() deployments=() containers=() conditions=() runtime_digests=()
kubeconfig='' context='' namespace='' image='' evidence_dir='' timeout_seconds=''
active_read='' watchdog='' evidence_created=0 started_at=$SECONDS

summary() {
	printf '{"complete":false,"products":%s,"deployments":%s,"pods":0,"failure":"%s"}\n' \
		"${#products[@]}" "${#deployments[@]}" "$1"
}
fail() {
	if [[ $evidence_created == 1 ]]; then summary "$1" >"$evidence_dir/summary.json"; fi
	summary "$1"
	exit "${2:-1}"
}
cleanup() {
	if [[ -n $active_read ]]; then
		kill -KILL -- "-$active_read" 2>/dev/null || true
		wait "$active_read" 2>/dev/null || true
	fi
	if [[ -n $watchdog ]]; then
		kill -KILL -- "-$watchdog" 2>/dev/null || true
		wait "$watchdog" 2>/dev/null || true
	fi
}
trap cleanup EXIT
trap 'fail interrupted' HUP INT TERM

while [[ $# -gt 0 ]]; do
	[[ $# -ge 2 ]] || fail invalid_arguments 2
	case "$1" in
	--kubeconfig) kubeconfig=$2 ;;
	--context) context=$2 ;;
	--namespace) namespace=$2 ;;
	--product) products+=("$2") ;;
	--deployment)
		[[ $2 == *:* && ${2#*:} != *:* ]] || fail invalid_arguments 2
		deployments+=("${2%%:*}")
		containers+=("${2#*:}")
		;;
	--condition) conditions+=("$2") ;;
	--image) image=$2 ;;
	--runtime-digest) runtime_digests+=("$2") ;;
	--timeout) timeout_seconds=$2 ;;
	--evidence-dir) evidence_dir=$2 ;;
	*) fail invalid_arguments 2 ;;
	esac
	shift 2
done

dns_name() { [[ ${#1} -le 253 && $1 =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && $1 != *..* && $1 != *.-* && $1 != *-.* ]]; }
dns_label() { [[ ${#1} -le 63 && $1 =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; }
unique() {
	local seen='|' value
	for value in "$@"; do
		[[ $seen != *"|$value|"* ]] || return 1
		seen+="$value|"
	done
}
[[ -n $kubeconfig && -r $kubeconfig && -f $kubeconfig && -n $context && ${#context} -le 253 ]] || fail invalid_arguments 2
dns_label "$namespace" || fail invalid_arguments 2
[[ ${#products[@]} -gt 0 && ${#products[@]} -le 64 && ${#deployments[@]} -gt 0 && ${#deployments[@]} -le 64 && ${#conditions[@]} -gt 0 ]] || fail invalid_arguments 2
for value in "${products[@]}" "${deployments[@]}"; do dns_name "$value" || fail invalid_arguments 2; done
for value in "${containers[@]}"; do dns_label "$value" || fail invalid_arguments 2; done
for value in "${conditions[@]}"; do
	case "$value" in Ready | SourceReady | ConnectorReady | ContractsReady | CompositionReady) ;; *) fail invalid_arguments 2 ;; esac
done
unique "${products[@]}" || fail invalid_arguments 2
unique "${deployments[@]}" || fail invalid_arguments 2
unique "${conditions[@]}" || fail invalid_arguments 2
[[ " ${conditions[*]} " == *' Ready '* ]] || fail invalid_arguments 2
[[ $image =~ ^[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:[a-f0-9]{64}$ && ${#image} -le 512 ]] || fail invalid_arguments 2
[[ ${#runtime_digests[@]} -gt 0 && ${#runtime_digests[@]} -le 8 ]] || fail invalid_arguments 2
for value in "${runtime_digests[@]}"; do [[ $value =~ ^sha256:[a-f0-9]{64}$ ]] || fail invalid_arguments 2; done
unique "${runtime_digests[@]}" || fail invalid_arguments 2
[[ $timeout_seconds =~ ^[1-9][0-9]{0,2}$ && $timeout_seconds -le 600 && $evidence_dir == /* && ! -e $evidence_dir ]] || fail invalid_arguments 2
command -v kubectl >/dev/null || fail missing_dependency 2
command -v jq >/dev/null || fail missing_dependency 2
mkdir -m 700 -- "$evidence_dir" 2>/dev/null || fail evidence_unavailable
evidence_created=1

# Bash job control gives the read pipeline its own process group. The watchdog
# bounds the entire call, including credential plugins, rather than just HTTP.
set -m
deadline=$((started_at + timeout_seconds))
remaining() {
	left=$((deadline - SECONDS))
	[[ $left -gt 0 ]] || fail deadline_exceeded
}
bounded() {
	local status=0
	remaining
	"$@" &
	active_read=$!
	(
		sleep "$left"
		kill -KILL -- "-$active_read" 2>/dev/null || true
	) &
	watchdog=$!
	wait "$active_read" 2>/dev/null || status=$?
	active_read=''
	kill -KILL -- "-$watchdog" 2>/dev/null || true
	wait "$watchdog" 2>/dev/null || true
	watchdog=''
	remaining
	return "$status"
}
project_read() {
	local mode=$1 destination=$2
	shift 2
	kubectl "$@" 2>/dev/null | jq -se --arg mode "$mode" -f "$script_dir/observe-rollout.jq" >"$destination" 2>/dev/null
}
read_metadata() {
	local mode=$1 destination=$2 resource=$3 name=${4:-} request_seconds
	remaining
	request_seconds=$left
	[[ $request_seconds -le 5 ]] || request_seconds=5
	local args=(--kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" --request-timeout "${request_seconds}s" get "$resource")
	[[ -z $name ]] || args+=("$name")
	args+=(-o json)
	bounded project_read "$mode" "$destination" "${args[@]}" || fail read_incomplete
	[[ -s $destination ]] || fail read_incomplete
}

product_names=$(printf '%s\n' "${products[@]}" | jq -Rsc 'split("\n")[:-1]')
required_conditions=$(printf '%s\n' "${conditions[@]}" | jq -Rsc 'split("\n")[:-1]')
accepted_digests=$(printf '%s\n' "${image##*@}" "${runtime_digests[@]}" | jq -Rsc 'split("\n")[:-1] | unique')
deployment_identities='[]'
for ((i = 0; i < ${#deployments[@]}; i++)); do
	deployment_identities=$(jq -cn --argjson current "$deployment_identities" --arg name "${deployments[$i]}" --arg container "${containers[$i]}" '$current + [{name:$name,container:$container}]')
done
# Evaluate the retained initial and final metadata without reading private payloads.
check_json() {
	local mode=$1
	jq -en --arg mode "$mode" --arg namespace "$namespace" --arg image "$image" \
		--argjson product_names "$product_names" --argjson deployment_identities "$deployment_identities" \
		--argjson required_conditions "$required_conditions" --argjson accepted_digests "$accepted_digests" \
		--slurpfile products "$evidence_dir/products.json" --slurpfile deployments "$evidence_dir/deployments.json" \
		--slurpfile replicasets "$evidence_dir/replicasets.json" --slurpfile pods "$evidence_dir/pods.json" \
		--slurpfile final_products "$evidence_dir/final-products.json" --slurpfile final_deployments "$evidence_dir/final-deployments.json" \
		--slurpfile final_replicasets "$evidence_dir/final-replicasets.json" --slurpfile final_pods "$evidence_dir/final-pods.json" \
		-f "$script_dir/observe-rollout.jq" >"$evidence_dir/summary.json" 2>/dev/null
}
check() { bounded check_json "$1"; }
json_array() {
	local destination=$1
	shift
	jq -s '.' "$@" >"$destination" 2>/dev/null
}
printf '[]\n' >"$evidence_dir/final-products.json"
printf '[]\n' >"$evidence_dir/final-deployments.json"
printf '{"items":[]}\n' >"$evidence_dir/final-replicasets.json"
printf '{"items":[]}\n' >"$evidence_dir/final-pods.json"
while :; do
	product_files=() deployment_files=()
	for ((i = 0; i < ${#products[@]}; i++)); do
		file="$evidence_dir/product-$i.json"
		product_files+=("$file")
		read_metadata product "$file" dataproducts.data.devantler.tech "${products[$i]}"
	done
	for ((i = 0; i < ${#deployments[@]}; i++)); do
		file="$evidence_dir/deployment-$i.json"
		deployment_files+=("$file")
		read_metadata deployment "$file" deployments.apps "${deployments[$i]}"
	done
	read_metadata replicasets "$evidence_dir/replicasets.json" replicasets.apps
	read_metadata pods "$evidence_dir/pods.json" pods
	bounded json_array "$evidence_dir/products.json" "${product_files[@]}" || fail read_incomplete
	bounded json_array "$evidence_dir/deployments.json" "${deployment_files[@]}" || fail read_incomplete
	if check snapshot; then
		product_files=() deployment_files=()
		for ((i = 0; i < ${#products[@]}; i++)); do
			file="$evidence_dir/product-$i-final.json"
			product_files+=("$file")
			read_metadata product "$file" dataproducts.data.devantler.tech "${products[$i]}"
		done
		for ((i = 0; i < ${#deployments[@]}; i++)); do
			file="$evidence_dir/deployment-$i-final.json"
			deployment_files+=("$file")
			read_metadata deployment "$file" deployments.apps "${deployments[$i]}"
		done
		read_metadata replicasets "$evidence_dir/final-replicasets.json" replicasets.apps
		read_metadata pods "$evidence_dir/final-pods.json" pods
		bounded json_array "$evidence_dir/final-products.json" "${product_files[@]}" || fail read_incomplete
		bounded json_array "$evidence_dir/final-deployments.json" "${deployment_files[@]}" || fail read_incomplete
		check final || fail rollout_changed
		remaining
		cat "$evidence_dir/summary.json"
		exit 0
	fi
	remaining
	sleep 0.2
done
