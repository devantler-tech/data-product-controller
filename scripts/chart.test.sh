#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
generated_crd="$repo_root/config/crd/bases/data.devantler.tech_dataproducts.yaml"
chart_crd="$chart/crds/data.devantler.tech_dataproducts.yaml"

fail() {
	printf '%s\n' "chart test failed: $1" >&2
	exit 1
}

command -v yq >/dev/null 2>&1 || fail 'yq is required to validate rendered RBAC'

assert_contains() {
	haystack=$1
	needle=$2
	printf '%s' "$haystack" | grep -F -- "$needle" >/dev/null || fail "missing $needle"
}

assert_not_contains() {
	haystack=$1
	needle=$2
	if printf '%s' "$haystack" | grep -F -- "$needle" >/dev/null; then
		fail "unexpected $needle"
	fi
}

default_render=$(helm template data-product-controller "$chart" --namespace data-product-system)
chart_crds=$(helm show crds "$chart")
assert_contains "$chart_crds" 'kind: CustomResourceDefinition'
assert_contains "$chart_crds" 'name: dataproducts.data.devantler.tech'
cmp "$generated_crd" "$chart_crd" || fail "generated CRD copies differ"
assert_contains "$default_render" 'value: "false"'
source_flag=$(printf '%s' "$default_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "PROVISIONED_SOURCES_ENABLED") | .value' -)
[ "$source_flag" = 'false' ] || fail 'provisioned sources must default off'
source_render=$(helm template data-product-controller "$chart" --namespace data-product-system --set provisionedSources.enabled=true)
source_flag=$(printf '%s' "$source_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "PROVISIONED_SOURCES_ENABLED") | .value' -)
[ "$source_flag" = 'true' ] || fail 'provisioned sources must be explicitly enableable'
assert_not_contains "$default_render" 'kind: HTTPRoute'
assert_not_contains "$default_render" 'name: harbour-observations'
assert_not_contains "$default_render" 'kind: CustomResourceDefinition'
assert_contains "$default_render" 'resources: [leases]'
assert_contains "$default_render" 'verbs: [get, list, watch, create, update, patch, delete]'
cluster_lease_rules=$(
	printf '%s' "$default_render" |
		yq ea '[select(.kind == "ClusterRole") | .rules[] | select(.resources[] == "leases")] | length' -
)
[ "$cluster_lease_rules" = '0' ] || fail 'ClusterRole must not grant cross-namespace Lease access'
namespace_lease_rules=$(
	printf '%s' "$default_render" |
		yq ea '[select(.kind == "Role") | .rules[] | select(.resources[] == "leases")] | length' -
)
[ "$namespace_lease_rules" = '1' ] || fail 'Role must grant Lease access in the release namespace'
assert_contains "$default_render" 'kind: RoleBinding'

controller_uid=$(
	printf '%s' "$default_render" |
		yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.securityContext.runAsUser' -
)
[ "$controller_uid" = '65532' ] || fail 'controller must run with the nonroot image UID'
controller_gid=$(
	printf '%s' "$default_render" |
		yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.securityContext.runAsGroup' -
)
[ "$controller_gid" = '65532' ] || fail 'controller must run with the nonroot image GID'
controller_cpu_limit=$(
	printf '%s' "$default_render" |
		yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].resources.limits.cpu' -
)
[ "$controller_cpu_limit" = '100m' ] || fail 'controller must have a CPU limit'
demo_cpu_limit=$(
	printf '%s' "$default_render" |
		yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "product") | .spec.template.spec.containers[0].resources.limits.cpu' -
)
[ "$demo_cpu_limit" = '50m' ] || fail 'demo product must have a CPU limit'
demo_token_mount=$(
	printf '%s' "$default_render" |
		yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "product") | .spec.template.spec.automountServiceAccountToken' -
)
[ "$demo_token_mount" = 'false' ] || fail 'demo product must not mount a Kubernetes API token'
network_policy_count=$(printf '%s' "$default_render" | yq ea '[select(.kind == "NetworkPolicy")] | length' -)
[ "$network_policy_count" = '2' ] || fail 'chart must isolate controller and demo product ingress'
wrong_namespace_count=$(
	printf '%s' "$default_render" |
		yq ea '[select(
      (.kind == "Deployment" or .kind == "Service" or .kind == "ServiceAccount" or
       .kind == "Role" or .kind == "RoleBinding" or .kind == "NetworkPolicy") and
      .metadata.namespace != "data-product-system"
    )] | length' -
)
[ "$wrong_namespace_count" = '0' ] || fail 'namespaced chart resources must use the release namespace'

hosted_render=$(helm template data-product-controller "$chart" \
	--set registryUI.enabled=true \
	--set route.enabled=true \
	--set route.host=data-products.example.test)
assert_contains "$hosted_render" 'value: "true"'
assert_contains "$hosted_render" 'kind: HTTPRoute'
assert_contains "$hosted_render" 'name: harbour-observations'
assert_contains "$hosted_render" 'https://data-products.example.test/products/harbour/ui'
assert_contains "$hosted_render" 'https://data-products.example.test/products/harbour/openapi.json'

printf '%s\n' 'chart behavior tests passed'
