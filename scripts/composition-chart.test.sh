#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
for enabled in false true; do
	if [ "$enabled" = false ]; then
		rendered=$(helm template controller "$chart" --namespace data-product-system)
	else
		rendered=$(helm template controller "$chart" --namespace data-product-system --set composition.enabled=true)
	fi
	flag=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "COMPOSITION_ENABLED") | .value' -)
	[ "$flag" = "$enabled" ] || { printf '%s\n' "composition flag must be $enabled"; exit 1; }
done
if helm template controller "$chart" --set-string composition.enabled=invalid >/dev/null 2>&1; then
	printf '%s\n' 'invalid composition flag accepted'
	exit 1
fi
printf '%s\n' 'composition chart behavior tests passed'
