#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/khoist"
fail() {
	printf '%s\n' "KSail release chart test failed: $1" >&2
	exit 1
}
default=$(helm template products "$chart" --namespace products)
flag=$(printf '%s' "$default" | yq ea 'select(.kind=="Deployment" and .spec.template.spec.containers[0].name=="product") | .spec.template.spec.containers[0].env[] | select(.name=="KSAIL_RELEASES_ENABLED") | .value' -)
[ -z "$flag" ] || fail 'standard release interfaces must not depend on a runtime gate'
count=$(printf '%s' "$default" | yq ea '[select(.kind=="DataProduct" and .metadata.name=="ksail-releases")] | length' -)
[ "$count" = 0 ] || fail 'disabled source must not publish a product'
enabled=$(helm template products "$chart" --namespace products --set route.enabled=true --set route.host=products.example.test --set demoProduct.ksailReleases.enabled=true --set uiContract.enabled=true --set uiAppearance.enabled=true)
flag=$(printf '%s' "$enabled" | yq ea 'select(.kind=="Deployment" and .spec.template.spec.containers[0].name=="product") | .spec.template.spec.containers[0].env[] | select(.name=="KSAIL_RELEASES_ENABLED") | .value' -)
[ -z "$flag" ] || fail 'optional publication must not restore the retired runtime gate'
product=$(printf '%s' "$enabled" | yq ea -o=json -I=0 'select(.kind=="DataProduct" and .metadata.name=="ksail-releases")' -)
printf '%s' "$product" | jq -e '
  .metadata.namespace=="products" and .spec.id=="https://github.com/devantler-tech/ksail/releases"
  and .spec.outputs[0].url=="https://api.github.com/repos/devantler-tech/ksail/releases"
  and .spec.outputs[0].contractUrl=="https://products.example.test/products/harbour/ksail-releases/openapi.json"
  and .spec.ui.url=="https://products.example.test/products/harbour/ksail-releases/ui"
  and .spec.ui.contract.apiVersion=="data-product-ui/v2"
  and .spec.ui.contract.capabilities==["status","resize","appearance"]
' >/dev/null || fail 'publication does not bind the actual API, contract, UI and namespace'
separate=$(helm template products "$chart" --namespace products --set demoProduct.publicBaseURL=https://harbour.example.test --set demoProduct.ksailReleases.enabled=true)
printf '%s' "$separate" | yq ea -o=json -I=0 'select(.kind=="DataProduct" and .metadata.name=="ksail-releases")' - | jq -e '
  .spec.ui.url=="https://harbour.example.test/ksail-releases/ui"
  and .spec.outputs[0].contractUrl=="https://harbour.example.test/ksail-releases/openapi.json"
' >/dev/null || fail 'independent publication requires its actual public URL'
if helm template products "$chart" --set demoProduct.enabled=false --set demoProduct.ksailReleases.enabled=true >/dev/null 2>&1; then
	fail 'publication without its workload was accepted'
fi
if helm template products "$chart" --set demoProduct.ksailReleases.enabled=true >/dev/null 2>&1; then
	fail 'publication without a public URL was accepted'
fi
printf '%s\n' 'KSail release chart boundaries passed'
