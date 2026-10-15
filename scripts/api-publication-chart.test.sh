#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail() {
	printf 'API publication chart test: %s\n' "$1" >&2
	exit 1
}
go -C "$repo_root" build -o "$work/product-check" ./cmd/product-check
for profile in shared independent; do
	for contract in false true; do
		for appearance in false true; do
			helm template publication "$repo_root/charts/data-product-controller" --namespace products \
				--set route.enabled=true --set route.host=products.example.test \
				--set demoProduct.ksailReleases.enabled=true \
				--set uiContract.enabled="$contract" --set uiAppearance.enabled="$appearance" \
				--set-string demoProduct.publicBaseURL="$(if [ "$profile" = independent ]; then printf https://reference.example.test; fi)" \
				>"$work/rendered.yaml"
			yq ea -o=json -I=0 'select(.kind == "DataProduct")' "$work/rendered.yaml" >"$work/products.json"
			jq -se 'length == 2 and all(.[]; .apiVersion == "data.devantler.tech/v1alpha1" and
        .metadata.namespace == "products" and (.spec | has("ui") | not) and
        (.spec.outputs | length == 1) and .spec.outputs[0].protocol == "OpenAPI") and
        ([.[].metadata.name] | sort == ["harbour-observations", "ksail-releases"])' \
				"$work/products.json" >/dev/null || fail "$profile/$contract/$appearance must publish two API-only products"
			yq -p=json -o=yaml '.' "$work/products.json" >"$work/products.yaml"
			PUBLISHER_PREFLIGHT_ENABLED=true "$work/product-check" --file "$work/products.yaml" \
				--report-version v2 --format json >"$work/report.json"
			jq -e '.valid and .complete and .products == 2 and (.descriptors | length == 2) and
        all(.descriptors[]; has("ui") | not) and
        (.requiredFeatures | index("ui-contract") == null and index("ui-appearance") == null)' \
				"$work/report.json" >/dev/null || fail "$profile/$contract/$appearance must produce complete UI-free previews"
		done
	done
done
printf '%s\n' 'API-only chart publication and offline preflight passed'
