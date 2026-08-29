#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
generated_crd="$repo_root/config/crd/bases/data.devantler.tech_dataproducts.yaml"
chart_crd="$chart/templates/data.devantler.tech_dataproducts.yaml"

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

default_render=$(helm template data-product-controller "$chart")
cmp "$generated_crd" "$chart_crd" || fail "generated CRD copies differ"
assert_contains "$default_render" 'value: "false"'
assert_not_contains "$default_render" 'kind: HTTPRoute'
assert_not_contains "$default_render" 'name: harbour-observations'
assert_contains "$default_render" 'kind: CustomResourceDefinition'
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
