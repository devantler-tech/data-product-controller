#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"

fail() {
	printf '%s\n' "connector chart test failed: $1" >&2
	exit 1
}

for enabled in false true; do
	if [ "$enabled" = false ]; then
		rendered=$(helm template data-product-controller "$chart" --namespace data-product-system)
	else
		rendered=$(helm template data-product-controller "$chart" --namespace data-product-system --set connectorReadiness.enabled=true)
	fi
	flag=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "CONNECTOR_READINESS_ENABLED") | .value' -)
	[ "$flag" = "$enabled" ] || fail "connector readiness flag must be $enabled"
	grants=$(printf '%s' "$rendered" | yq ea '[select(.kind == "Role" or .kind == "ClusterRole") | .rules[] | select(.resources[] | test("^(deployments|secrets|\\*)$"))] | length' -)
	[ "$grants" = 0 ] || fail 'release enablement must not grant workload or Secret reads'
done

if helm template data-product-controller "$chart" --set-string connectorReadiness.enabled=invalid >/dev/null 2>&1; then
	fail 'invalid connector flag accepted'
fi

rbac="$repo_root/docs/examples/connector-observer-rbac.yaml"
grant=$(yq ea 'select(.kind == "Role") | .rules' "$rbac" -o=json -I=0)
[ "$grant" = '[{"apiGroups":["apps"],"resources":["deployments"],"resourceNames":["export-http-source"],"verbs":["get"]}]' ] || fail 'example must grant only a named Deployment get'
role_namespace=$(yq ea 'select(.kind == "Role") | .metadata.namespace' "$rbac")
binding_namespace=$(yq ea 'select(.kind == "RoleBinding") | .metadata.namespace' "$rbac")
product_namespace=$(yq '.metadata.namespace' "$repo_root/docs/examples/http-source-product.yaml")
[ "$role_namespace" = "$product_namespace" ] && [ "$binding_namespace" = "$product_namespace" ] || fail 'observer access must be scoped to the product namespace'

printf '%s\n' 'connector chart behavior tests passed'
