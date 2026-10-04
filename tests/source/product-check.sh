#!/usr/bin/env bash
# Exercise the packaged offline command, including both feature states.
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
image=${1:?provide the built or verified released application image}
[[ $# == 1 && "$image" =~ ^[a-zA-Z0-9][a-zA-Z0-9./_:@-]+$ ]] || exit 1
for command in docker jq timeout; do
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
echo 'PASS: packaged publisher preflight is default-off, offline, schema-aware and never claims observed readiness'
