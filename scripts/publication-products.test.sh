#!/usr/bin/env bash
set -euo pipefail
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
filter=$repo_root/tests/source/publication-products.sh
helm template publication "$repo_root/charts/data-product-controller" --namespace products \
	--set route.enabled=true --set route.host=products.example.test \
	--set demoProduct.ksailReleases.enabled=true >"$work/rendered.yaml"
"$filter" <"$work/rendered.yaml" >"$work/filtered.yaml"
yq ea -o=json -I=0 'select(.kind == "DataProduct")' "$work/rendered.yaml" | jq -s 'sort_by(.metadata.name)' >"$work/expected.json"
yq ea -o=json -I=0 '.' "$work/filtered.yaml" | jq -s 'sort_by(.metadata.name)' >"$work/actual.json"
cmp "$work/expected.json" "$work/actual.json"
reject() {
	if "$filter" <"$work/invalid.yaml" >"$work/output.yaml" 2>"$work/error.log"; then
		echo "publication filter accepted $1" >&2
		exit 1
	fi
	[[ ! -s "$work/output.yaml" ]] || {
		echo 'failed filter emitted partial products' >&2
		exit 1
	}
}
: >"$work/invalid.yaml"
reject 'an empty stream'
printf '%s\n' 'kind: [' >"$work/invalid.yaml"
reject 'malformed YAML'
yq ea 'select(.metadata.name == "harbour-observations" and .kind == "DataProduct")' "$work/rendered.yaml" >"$work/invalid.yaml"
reject 'a missing product'
cat "$work/filtered.yaml" >"$work/invalid.yaml"
printf '\n---\n' >>"$work/invalid.yaml"
cat "$work/filtered.yaml" >>"$work/invalid.yaml"
reject 'duplicate products'
yq 'with(select(.kind == "DataProduct"); .metadata.namespace = "another")' "$work/filtered.yaml" >"$work/invalid.yaml"
reject 'a foreign namespace'
yq 'with(select(.kind == "DataProduct"); .apiVersion = "another/v1")' "$work/filtered.yaml" >"$work/invalid.yaml"
reject 'a foreign API version'
cat "$work/filtered.yaml" >"$work/invalid.yaml"
printf '\n---\napiVersion: data.devantler.tech/v1alpha1\nkind: DataProduct\nmetadata:\n  name: unexpected\n  namespace: products\n' >>"$work/invalid.yaml"
reject 'an unexpected product'
cat "$work/filtered.yaml" >"$work/invalid.yaml"
for ((n = 0; n < 256; n++)); do printf '\n---\nkind: ConfigMap\n' >>"$work/invalid.yaml"; done
reject 'more than 256 rendered documents'
grep -F 'too many rendered documents' "$work/error.log" >/dev/null
cat "$work/filtered.yaml" >"$work/invalid.yaml"
dd if=/dev/zero bs=1048576 count=2 2>/dev/null | tr '\000' ' ' >>"$work/invalid.yaml"
reject 'more than two MiB'
grep -F 'render exceeds two MiB' "$work/error.log" >/dev/null
printf '%s\n' 'Product-only Helm post-renderer preserves original fields and rejects invalid selections'
