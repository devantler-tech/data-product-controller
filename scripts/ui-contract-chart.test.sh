#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
for enabled in false true; do
	if [ "$enabled" = false ]; then
		rendered=$(helm template controller "$chart" --namespace data-product-system --set route.enabled=true --set route.host=catalog.example)
	else
		rendered=$(helm template controller "$chart" --namespace data-product-system --set route.enabled=true --set route.host=catalog.example --set uiContract.enabled=true)
	fi
	for container in controller product; do
		flag=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment") | .spec.template.spec.containers[] | select(.name == "'"$container"'") | .env[] | select(.name == "UI_CONTRACT_ENABLED") | .value' -)
		[ "$flag" = "$enabled" ] || {
			printf '%s\n' "$container UI contract flag must be $enabled"
			exit 1
		}
	done
	version=$(printf '%s' "$rendered" | yq ea 'select(.kind == "DataProduct") | .spec.ui.contract.apiVersion' -)
	publisher_origins=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment") | .spec.template.spec.containers[] | select(.name == "product") | .env[] | select(.name == "UI_HOST_ORIGINS") | .value' -)
	if [ "$enabled" = true ]; then
		[ "$version" = data-product-ui/v1 ] || exit 1
		origins=$(printf '%s' "$rendered" | yq ea 'select(.kind == "DataProduct") | .spec.ui.contract.hostOrigins[]' -)
		[ "$origins" = https://catalog.example ] || exit 1
		[ "$publisher_origins" = https://catalog.example ] || exit 1
	else
		[ "$version" = null ] || exit 1
		[ -z "$publisher_origins" ] || exit 1
	fi
done
if helm template controller "$chart" --set-string uiContract.enabled=invalid >/dev/null 2>&1; then
	printf '%s\n' 'invalid UI contract flag accepted'
	exit 1
fi
printf '%s\n' 'UI contract chart behavior tests passed'
