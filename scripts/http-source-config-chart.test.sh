#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
render_source() {
	helm template source-config "$chart" --namespace products --set image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --set httpSource.enabled=true --set httpSource.sourceCIDR=192.0.2.10/32 --set httpSource.consumerPodLabels.app=reader --set-string httpSource.secretName="$1"
}
for name in a..b a.-b a-.b .source source. Source; do
	if render_source "$name" >/dev/null 2>&1; then
		printf '%s\n' "invalid source Secret name rendered: $name" >&2
		exit 1
	fi
done
for name in existing-export warehouse.daily aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; do
	render_source "$name" >/dev/null
done
for name in 123 null yes; do
	rendered=$(render_source "$name")
	tag=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment") | .spec.template.spec.volumes[]? | select(.name == "source-config") | .secret.secretName | tag' -)
	[ "$tag" = '!!str' ] || { printf '%s\n' "valid Secret name lost its string type: $name ($tag)" >&2; exit 1; }
done
helm template source-config "$chart" >/dev/null
printf '%s\n' 'HTTP source Secret name schema checks passed'
