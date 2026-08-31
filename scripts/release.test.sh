#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
workflow="$repo_root/.github/workflows/cd.yaml"
deployment="$repo_root/deploy/deployment.yaml"
deploy_kustomization="$repo_root/deploy/kustomization.yaml"
deploy_namespace="$repo_root/deploy/namespace.yaml"
deploy_rbac="$repo_root/deploy/rbac.yaml"
deploy_network_policy="$repo_root/deploy/network-policy.yaml"
generated_rbac="$repo_root/config/rbac/role.yaml"
chart="$repo_root/charts/data-product-controller/Chart.yaml"
values="$repo_root/charts/data-product-controller/values.yaml"
generated_crd="$repo_root/config/crd/bases/data.devantler.tech_dataproducts.yaml"
deploy_crd="$repo_root/deploy/data.devantler.tech_dataproducts.yaml"
chart_crd="$repo_root/charts/data-product-controller/templates/data.devantler.tech_dataproducts.yaml"

fail() {
	printf '%s\n' "release contract test failed: $1" >&2
	exit 1
}

command -v yq >/dev/null 2>&1 || fail 'yq is required to validate release manifests'

grep -Eq 'uses: devantler-tech/actions/\.github/workflows/publish-app\.yaml@[0-9a-f]{40}' "$workflow" ||
	fail 'CD must call the immutable shared publish-app workflow'
grep -F 'app-name: data-product-controller' "$workflow" >/dev/null ||
	fail 'CD must identify the controller container for digest pinning'
grep -F 'enable-caller-pin: true' "$workflow" >/dev/null ||
	fail 'CD must require an immutable caller identity before signing'

[ -f "$deployment" ] || fail 'deploy/deployment.yaml is required by publish-app'
cmp "$generated_crd" "$deploy_crd" || fail 'published and generated CRDs differ'
cmp "$generated_crd" "$chart_crd" || fail 'chart and generated CRDs differ'
container_count=$(yq '[.spec.template.spec.containers[] | select(.name == "data-product-controller")] | length' "$deployment")
[ "$container_count" = '1' ] ||
	fail 'deployment must contain exactly one data-product-controller container'
[ "$(yq '.spec.template.spec.containers[] | select(.name == "data-product-controller") | .image' "$deployment")" = 'ghcr.io/devantler-tech/data-product-controller:latest' ] ||
	fail 'publish-app must receive the expected mutable image placeholder'

chart_app_version=$(yq '.appVersion' "$chart")
[ -n "$chart_app_version" ] && [ "$chart_app_version" != 'null' ] ||
	fail 'chart appVersion is required to derive the expected image tag'
expected_tag=${chart_app_version#v}
image_tag=$(yq '.image.tag' "$values")
case "$image_tag" in
v*) fail 'the chart image tag must not include a v prefix' ;;
esac
[ "$image_tag" = "$expected_tag" ] ||
	fail 'the chart tag must match docker metadata semver output (without the v prefix)'

[ "$(yq '.namespace' "$deploy_kustomization")" = 'data-product-system' ] ||
	fail 'deploy kustomization must target data-product-system'
[ "$(yq '[.resources[] | select(. == "namespace.yaml")] | length' "$deploy_kustomization")" = '1' ] ||
	fail 'deploy kustomization must create its namespace'
[ "$(yq '.kind' "$deploy_namespace")" = 'Namespace' ] ||
	fail 'deploy/namespace.yaml must define a Namespace'
[ "$(yq '.metadata.name' "$deploy_namespace")" = 'data-product-system' ] ||
	fail 'deploy namespace must be data-product-system'
[ "$(yq '.metadata.namespace' "$deployment")" = 'data-product-system' ] ||
	fail 'deployment must declare the target namespace for standalone scanners'
[ "$(yq '.spec.template.spec.securityContext.runAsUser' "$deployment")" = '65532' ] ||
	fail 'deployment must run with the nonroot image UID'
[ "$(yq '.spec.template.spec.securityContext.runAsGroup' "$deployment")" = '65532' ] ||
	fail 'deployment must run with the nonroot image GID'
[ "$(yq '.spec.template.spec.containers[0].resources.limits.cpu' "$deployment")" = '100m' ] ||
	fail 'deployment must set a CPU limit'
[ "$(yq '.kind' "$deploy_network_policy")" = 'NetworkPolicy' ] ||
	fail 'deploy/network-policy.yaml must define a NetworkPolicy'
[ "$(yq '.metadata.namespace' "$deploy_network_policy")" = 'data-product-system' ] ||
	fail 'deploy NetworkPolicy must target data-product-system'
[ "$(yq '[.resources[] | select(. == "network-policy.yaml")] | length' "$deploy_kustomization")" = '1' ] ||
	fail 'deploy kustomization must include the NetworkPolicy'
cluster_lease_rules=$(
	yq ea '[select(.kind == "ClusterRole") | .rules[] | select(.resources[] == "leases")] | length' "$deploy_rbac"
)
[ "$cluster_lease_rules" = '0' ] || fail 'deploy ClusterRole must not grant cross-namespace Lease access'
namespace_lease_rules=$(
	yq ea '[select(.kind == "Role") | .rules[] | select(.resources[] == "leases")] | length' "$deploy_rbac"
)
[ "$namespace_lease_rules" = '1' ] || fail 'deploy Role must grant Lease access in data-product-system'
generated_cluster_lease_rules=$(
	yq ea '[select(.kind == "ClusterRole") | .rules[] | select(.resources[] == "leases")] | length' "$generated_rbac"
)
[ "$generated_cluster_lease_rules" = '0' ] ||
	fail 'controller-gen output must not grant cross-namespace Lease access'
generated_namespace_lease_rules=$(
	yq ea '[select(.kind == "Role" and .metadata.namespace == "data-product-system") | .rules[] | select(.resources[] == "leases")] | length' "$generated_rbac"
)
[ "$generated_namespace_lease_rules" = '1' ] ||
	fail 'controller-gen output must grant Lease access only in data-product-system'

printf '%s\n' 'release contract tests passed'
