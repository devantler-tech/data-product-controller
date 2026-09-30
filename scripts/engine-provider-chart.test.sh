#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
for enabled in false true; do
	rendered=$(helm template controller "$chart" --namespace data-product-system --set engineProviders.enabled="$enabled")
	flag=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "ENGINE_PROVIDERS_ENABLED") | .value' -)
	[ "$flag" = "$enabled" ] || {
		printf '%s\n' "engine-provider flag must be $enabled"
		exit 1
	}
done
default_render=$(helm template controller "$chart")
printf '%s' "$default_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "ENGINE_PROVIDERS_ENABLED") | .value' - | grep -Fx false >/dev/null
if helm template controller "$chart" --set-string engineProviders.enabled=invalid >/dev/null 2>&1; then
	printf '%s\n' 'invalid engine-provider flag accepted'
	exit 1
fi
printf '%s\n' 'engine-provider chart behavior tests passed'
