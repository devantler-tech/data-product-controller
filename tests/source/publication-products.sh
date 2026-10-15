#!/usr/bin/env bash
# Helm post-renderer: preserve exactly the two original published products, without workloads or CRDs.
set -euo pipefail
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
head -c 2097153 >"$work/input.yaml"
[[ $(wc -c <"$work/input.yaml") -le 2097152 ]] || {
	echo 'publication fixture render exceeds two MiB' >&2
	exit 1
}
yq ea -o=json -I=0 '.' "$work/input.yaml" >"$work/documents.json"
jq -se '
  if length > 256 then error("too many rendered documents") else . end |
  map(select(.kind == "DataProduct")) |
  if length == 2 and
    (map(.metadata.name) | sort == ["harbour-observations", "ksail-releases"]) and
    all(.[]; .apiVersion == "data.devantler.tech/v1alpha1" and .metadata.namespace == "products")
  then .[] else error("expected exactly the two original namespaced products") end
' "$work/documents.json" >"$work/products.json"
yq -p=json -o=yaml '.' "$work/products.json"
