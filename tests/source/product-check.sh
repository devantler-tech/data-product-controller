#!/usr/bin/env bash
# Exercise the packaged offline command, including both feature states.
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
image=${1:?provide the built or verified released application image}
[[ $# == 1 && "$image" =~ ^[a-zA-Z0-9][a-zA-Z0-9./_:@-]+$ ]] || exit 1
for command in docker jq timeout go; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done
[[ $(docker image inspect "$image" --format '{{.Config.User}}') == '65532:65532' ]]

evidence=$(mktemp -d)
attempt=0
cleanup() {
	local result=$? cid
	trap - EXIT INT TERM
	for cid in "$evidence"/cid-*; do
		[[ -f "$cid" ]] || continue
		if docker inspect "$(cat "$cid")" >/dev/null 2>&1; then
			docker rm --force "$(cat "$cid")" >/dev/null || result=1
		fi
	done
	rm -rf "$evidence"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 755 "$evidence"

run_check() {
	attempt=$((attempt + 1))
	timeout --foreground --kill-after=5s 30s docker run --rm --network none \
		--cidfile "$evidence/cid-$attempt" --read-only --cap-drop ALL \
		--security-opt no-new-privileges:true --user 65532:65532 \
		--memory 256m --cpus 1 --pids-limit 64 --stop-timeout 1 \
		--entrypoint /product-check "$@"
}

for state in default false; do
	options=()
	[[ "$state" == default ]] || options+=(--env PUBLISHER_PREFLIGHT_ENABLED=false)
	if run_check "${options[@]}" "$image" --file /missing --format json \
		>"$evidence/disabled.json" 2>"$evidence/disabled.log"; then
		echo 'publisher preflight unexpectedly enabled' >&2
		exit 1
	fi
	[[ ! -s "$evidence/disabled.json" ]]
	grep -F 'publisher preflight is disabled' "$evidence/disabled.log" >/dev/null
done

run_check --env PUBLISHER_PREFLIGHT_ENABLED=true \
	--mount "type=bind,src=$repo_root/docs/examples,dst=/examples,readonly" \
	"$image" --file /examples/composition.yaml --format json >"$evidence/bundle.json"
jq -e '
  .apiVersion == "data-product-preflight/v1" and .valid and .complete and
  .products == 3 and .requiredFeatures == ["composition"] and
  (.descriptors | length) == 3 and
  all(.descriptors[]; .ready == false and .generation == 0 and
    .observedGeneration == 0 and .readiness.reason == "unobserved")
' "$evidence/bundle.json" >/dev/null

cat >"$evidence/valid.json" <<'JSON'
{"apiVersion":"data.devantler.tech/v1alpha1","kind":"DataProduct","metadata":{"name":"harbour","namespace":"products","generation":42},"spec":{"id":"urn:example:harbour","name":"Harbour","description":"Public observations","version":"v1.0.0","owner":{"name":"Example team"},"outputs":[{"name":"query","protocol":"OpenAPI","url":"https://api.example.test/observations","contractUrl":"https://api.example.test/openapi.json"}]},"status":{"conditions":[{"type":"Ready","status":"True","reason":"PRIVATE_REASON","message":"PRIVATE_MESSAGE","observedGeneration":42,"lastTransitionTime":"2026-01-01T00:00:00Z"}]}}
JSON
jq '.spec.source = {adapter:"cnpg/v1", engine:{apiVersion:"engine-provider/v1", type:"sql", provider:"native"}, resourceRef:{apiVersion:"postgresql.cnpg.io/v1", kind:"Cluster", name:"warehouse"}, connectionSecretRef:{name:"warehouse-superuser"}}' \
	"$evidence/valid.json" >"$evidence/invalid.json"
jq '.spec.inputs = [{name:"upstream", productRef:{name:"absent", output:"query"}}]' \
	"$evidence/valid.json" >"$evidence/unresolved.json"
chmod 444 "$evidence/valid.json" "$evidence/invalid.json" "$evidence/unresolved.json"

check_file() {
	local file=$1 expected=$2 result
	if run_check --env PUBLISHER_PREFLIGHT_ENABLED=true \
		--mount "type=bind,src=$evidence,dst=/input,readonly" \
		"$image" --file "/input/$file.json" --format json >"$evidence/$file-report.json"; then
		result=0
	else
		result=$?
	fi
	[[ "$result" == "$expected" ]] || {
		echo "unexpected $file exit: $result" >&2
		return 1
	}
	! grep -F 'PRIVATE_' "$evidence/$file-report.json"
}
check_file valid 0
jq -e '.valid and .complete and .descriptors[0].ready == false and
  .descriptors[0].generation == 0 and .descriptors[0].observedGeneration == 0 and
  .descriptors[0].readiness.reason == "unobserved"' "$evidence/valid-report.json" >/dev/null
check_file invalid 1
jq -e '.valid == false and .complete == false and (has("descriptors") | not) and
  any(.diagnostics[]; .code == "AdmissionInvalid")' "$evidence/invalid-report.json" >/dev/null
check_file unresolved 2
jq -e '.valid and .complete == false and (has("descriptors") | not) and
  any(.diagnostics[]; .code == "ProducerUnresolved")' "$evidence/unresolved-report.json" >/dev/null

# Selected producer and consumer files are one bounded validation scope.
jq '.metadata.name = "consumer" | .spec.id = "urn:example:consumer" |
  .spec.inputs = [{name:"upstream",productRef:{name:"harbour",output:"query"}}]' \
	"$evidence/valid.json" >"$evidence/consumer.json"
jq '.spec.inputs = [{name:"upstream",productRef:{name:"consumer",output:"query"}}]' \
	"$evidence/valid.json" >"$evidence/cycle.json"
chmod 444 "$evidence/consumer.json" "$evidence/cycle.json"
jq '.spec.inputs[0].contract = {minimumVersion:"v2.0.0",protocol:"OpenAPI"}' \
	"$evidence/consumer.json" >"$evidence/incompatible.json"
jq '.spec.inputs = [range(0;140) | {name:("input-" + tostring),
  productRef:{name:"absent",output:"query"}}]' \
	"$evidence/valid.json" >"$evidence/truncated.json"
chmod 444 "$evidence/incompatible.json" "$evidence/truncated.json"

check_bundle() {
	local name=$1 expected=$2 result
	shift 2
	if run_check --env PUBLISHER_PREFLIGHT_ENABLED=true \
		--mount "type=bind,src=$evidence,dst=/input,readonly" \
		"$image" --report-version v2 --format json "$@" >"$evidence/$name-v2.json"; then
		result=0
	else
		result=$?
	fi
	[[ "$result" == "$expected" ]]
	! grep -F 'PRIVATE_' "$evidence/$name-v2.json"
}
check_bundle selected 0 --file /input/consumer.json --file /input/valid.json
jq -e '.apiVersion == "data-product-preflight/v2" and .valid and .complete and
  .products == 2 and (.sources | length) == 2 and
  [.plan.order[].key] == ["products/harbour","products/consumer"] and
  .plan.edges[0].inputIndex == 0 and .diagnosticCounts.total == 0 and
  all(.descriptors[]; .ready == false and .generation == 0 and .observedGeneration == 0)
' "$evidence/selected-v2.json" >/dev/null
check_bundle duplicate 1 --file /input/valid.json --file /input/valid.json
jq -e '(.valid | not) and (.complete | not) and .plan == null and
  .descriptors == [] and any(.diagnostics[]; .code == "IdentityConflict")' \
	"$evidence/duplicate-v2.json" >/dev/null
check_bundle cycle 1 --file /input/cycle.json --file /input/consumer.json
jq -e '(.valid | not) and .plan == null and .descriptors == [] and
  any(.diagnostics[]; .code == "CompositionCycle" and (.witness | length) == 2)' \
	"$evidence/cycle-v2.json" >/dev/null
check_bundle unresolved 2 --file /input/consumer.json
jq -e '.valid and (.complete | not) and .plan == null and .descriptors == [] and
  any(.diagnostics[]; .code == "ProducerUnresolved" and .source == 1 and
    .document == 1 and .path == "/spec/inputs/0/productRef")' \
	"$evidence/unresolved-v2.json" >/dev/null
check_bundle missing 1 --file /input/missing.json --file /input/valid.json
[[ ! -s "$evidence/missing-v2.json" ]]
check_bundle invalid 1 --file /input/invalid.json --file /input/consumer.json
jq -e '(.valid | not) and .descriptors == [] and .plan == null and
  .diagnosticCounts.errors > 0' "$evidence/invalid-v2.json" >/dev/null
check_bundle incompatible 1 --file /input/valid.json --file /input/incompatible.json
jq -e '(.valid | not) and .descriptors == [] and .plan == null and
  any(.diagnostics[]; .code == "ContractIncompatible" and
    .path == "/spec/inputs/0/contract")' "$evidence/incompatible-v2.json" >/dev/null
check_bundle truncated 2 --file /input/truncated.json
jq -e '.valid and (.complete | not) and .descriptors == [] and .plan == null and
  (.diagnostics | length) == 128 and .diagnosticCounts.total == 140 and
  .diagnosticCounts.warnings == 140 and .diagnosticCounts.omitted == 12' \
	"$evidence/truncated-v2.json" >/dev/null

# Build the single standard-library reader without module or network dependency resolution.
GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	go build -o "$evidence/report-reader" "$repo_root/docs/examples/preflight-client/main.go"
for name in selected duplicate cycle unresolved invalid incompatible truncated; do
	"$evidence/report-reader" "$evidence/$name-v2.json" >"$evidence/$name-reader.log"
done
echo 'PASS: packaged publisher preflight is default-off, offline, schema-aware and never claims observed readiness'
