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
assert_nonroot_containers() {
	rendered=$1
	expected_count=$2
	variant=$3
	container_count=$(printf '%s' "$rendered" | yq ea '[select(.kind == "Deployment") | .spec.template.spec.containers[]] | length' -)
	[ "$container_count" = "$expected_count" ] || fail "$variant must render $expected_count workload containers"
	missing_nonroot=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment") | .spec.template.spec.containers[] | select(.securityContext.runAsNonRoot != true) | .name' -)
	[ -z "$missing_nonroot" ] || fail "$variant containers must explicitly declare non-root execution: $missing_nonroot"
}
assert_nonroot_containers "$default_render" 2 default
chart_crds=$(helm show crds "$chart")
assert_contains "$chart_crds" 'kind: CustomResourceDefinition'
assert_contains "$chart_crds" 'name: dataproducts.data.devantler.tech'
cmp "$generated_crd" "$chart_crd" || fail "generated CRD copies differ"
assert_contains "$default_render" 'value: "false"'
assert_not_contains "$default_render" 'REGISTRY_UI_ENABLED'
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

# assert_leader_events checks the controller's exact release-local Event grant.
assert_leader_events() {
	rendered=$1
	expected_namespace=$2
	event_role=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | select(.rules[].resources[] == "events") | .metadata.namespace' -)
	[ "$event_role" = "$expected_namespace" ] || fail 'leader-election Event access must be release-local'
	event_groups=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | .rules[] | select(.resources[] == "events") | .apiGroups | join(",")' -)
	[ -z "$event_groups" ] || fail 'leader-election Events must use only the core API group'
	event_group_count=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | .rules[] | select(.resources[] == "events") | .apiGroups | length' -)
	[ "$event_group_count" = '1' ] || fail 'leader-election Events must use exactly one API group'
	event_resource_count=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | .rules[] | select(.resources[] == "events") | .resources | length' -)
	[ "$event_resource_count" = '1' ] || fail 'leader-election Event rule must not grant other resources'
	event_verbs=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | .rules[] | select(.resources[] == "events") | .verbs | sort | join(",")' -)
	[ "$event_verbs" = 'create,patch' ] || fail 'leader-election Events must grant create and patch only'
	cluster_events=$(printf '%s' "$rendered" | yq ea '[select(.kind == "ClusterRole") | .rules[] | select(.resources[] | test("^(events|\\*)$"))] | length' -)
	[ "$cluster_events" = '0' ] || fail 'ClusterRole must not grant Event or wildcard access'
	wildcard_rules=$(printf '%s' "$rendered" | yq ea '[select(.kind == "Role" or .kind == "ClusterRole") | .rules[] | select((.apiGroups + .resources + .verbs)[] | test("^\\*$"))] | length' -)
	[ "$wildcard_rules" = '0' ] || fail 'leader-election RBAC must not grant wildcard access'
	binding_namespace=$(printf '%s' "$rendered" | yq ea 'select(.kind == "RoleBinding") | .metadata.namespace' -)
	[ "$binding_namespace" = "$expected_namespace" ] || fail 'leader-election RoleBinding must be release-local'
	event_role_name=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Role") | select(.rules[].resources[] == "events") | .metadata.name' -)
	binding_role=$(printf '%s' "$rendered" | yq ea 'select(.kind == "RoleBinding") | .roleRef.kind + ":" + .roleRef.name' -)
	[ "$binding_role" = "Role:$event_role_name" ] || fail 'leader-election RoleBinding must reference the Event role'
	workload_service_account=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.serviceAccountName' -)
	event_subject_count=$(printf '%s' "$rendered" | yq ea 'select(.kind == "RoleBinding") | .subjects | length' -)
	[ "$event_subject_count" = '1' ] || fail 'leader-election RoleBinding must bind only the controller ServiceAccount'
	event_binding=$(printf '%s' "$rendered" | yq ea 'select(.kind == "RoleBinding") | .subjects[0] | .kind + ":" + .namespace + ":" + .name' -)
	[ "$event_binding" = "ServiceAccount:$expected_namespace:$workload_service_account" ] || fail 'leader-election RoleBinding must bind only the controller ServiceAccount'
}
assert_leader_events "$default_render" data-product-system
alternate_namespace_render=$(helm template data-product-controller "$chart" --namespace alternate-products)
assert_leader_events "$alternate_namespace_render" alternate-products
extra_subject_render=$(printf '%s' "$default_render" | yq ea '(select(.kind == "RoleBinding").subjects) += [{"kind": "User", "name": "other-user"}]' -)
if (assert_leader_events "$extra_subject_render" data-product-system) >/dev/null 2>&1; then
	fail 'leader-election RoleBinding must reject additional subjects'
fi
wrong_account_render=$(printf '%s' "$default_render" | yq ea '(select(.kind == "RoleBinding").subjects[0].name) = "other-account"' -)
if (assert_leader_events "$wrong_account_render" data-product-system) >/dev/null 2>&1; then
	fail 'leader-election RoleBinding must reject another ServiceAccount'
fi

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
	--set route.enabled=true \
	--set route.host=data-products.example.test)
assert_not_contains "$hosted_render" 'REGISTRY_UI_ENABLED'
assert_contains "$hosted_render" 'kind: HTTPRoute'
assert_contains "$hosted_render" 'name: harbour-observations'
assert_contains "$hosted_render" 'https://data-products.example.test/products/harbour/ui'
assert_contains "$hosted_render" 'https://data-products.example.test/products/harbour/openapi.json'
demo_public_url=$(printf '%s' "$hosted_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "product") | .spec.template.spec.containers[0].env[] | select(.name == "PUBLIC_BASE_URL") | .value' -)
[ "$demo_public_url" = 'https://data-products.example.test/products/harbour' ] || fail 'hosted sample must declare its public URL for opaque-origin CSP'
assert_not_contains "$default_render" 'PUBLIC_BASE_URL'
separate_product_render=$(helm template data-product-controller "$chart" --set demoProduct.publicBaseURL=https://harbour.example.test)
demo_public_url=$(printf '%s' "$separate_product_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "product") | .spec.template.spec.containers[0].env[] | select(.name == "PUBLIC_BASE_URL") | .value' -)
[ "$demo_public_url" = 'https://harbour.example.test' ] || fail 'separate-host sample must use its explicit public URL'

dcat_flag=$(printf '%s' "$default_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "DCAT_CATALOG_ENABLED") | .value' -)
[ "$dcat_flag" = 'false' ] || fail 'DCAT catalog must default off'
dcat_id=$(printf '%s' "$default_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "DCAT_CATALOG_ID") | .value' -)
[ -z "$dcat_id" ] || fail 'DCAT catalog must not invent a default identity'
dcat_type=$(printf '%s' "$hosted_render" | yq ea 'select(.kind == "DataProduct") | .metadata.annotations."data.devantler.tech/dcat-type"' -)
[ "$dcat_type" = 'null' ] || fail 'demo product must not opt into DCAT by default'

dcat_render=$(helm template data-product-controller "$chart" \
	--namespace data-product-system \
	--set dcatCatalog.enabled=true \
	--set-string dcatCatalog.id=urn:example:catalog:harbour \
	--set route.enabled=true \
	--set route.host=data-products.example.test)
dcat_flag=$(printf '%s' "$dcat_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "DCAT_CATALOG_ENABLED") | .value' -)
[ "$dcat_flag" = 'true' ] || fail 'DCAT catalog must be explicitly enableable'
dcat_id=$(printf '%s' "$dcat_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "DCAT_CATALOG_ID") | .value' -)
[ "$dcat_id" = 'urn:example:catalog:harbour' ] || fail 'DCAT catalog identity must reach the controller unchanged'
dcat_type=$(printf '%s' "$dcat_render" | yq ea 'select(.kind == "DataProduct") | .metadata.annotations."data.devantler.tech/dcat-type"' -)
[ "$dcat_type" = 'Dataset' ] || fail 'enabled demo product must declare its dataset semantics'
if helm template data-product-controller "$chart" --set dcatCatalog.enabled=true >/dev/null 2>&1; then
	fail 'enabled DCAT catalog must require an explicit identity'
fi
if helm template data-product-controller "$chart" --set dcatCatalog.enabled=true --set-string 'dcatCatalog.id=   ' >/dev/null 2>&1; then
	fail 'enabled DCAT catalog must reject a blank identity'
fi
if helm template data-product-controller "$chart" --set-string dcatCatalog.enabled=true >/dev/null 2>&1; then
	fail 'DCAT catalog flag must be a boolean'
fi
if helm template data-product-controller "$chart" --set dcatCatalog.id=42 >/dev/null 2>&1; then
	fail 'DCAT catalog identity must be a string'
fi
if helm template data-product-controller "$chart" --set dcatCatalog.enabled=true --set-string "dcatCatalog.id=https://example.test/catalog/\$(TOKEN)" >/dev/null 2>&1; then
	fail 'DCAT catalog identity must not expand environment variables'
fi

sh "$repo_root/scripts/http-source-chart.test.sh"
sh "$repo_root/scripts/connector-chart.test.sh"
sh "$repo_root/scripts/contract-chart.test.sh"
sh "$repo_root/scripts/composition-chart.test.sh"
sh "$repo_root/scripts/ui-contract-chart.test.sh"
sh "$repo_root/scripts/ui-host-origins-chart.test.sh"
sh "$repo_root/scripts/engine-provider-chart.test.sh"

optional_render=$(helm template data-product-controller "$chart" \
	--namespace data-product-system \
	--set image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
	--set httpSource.enabled=true \
	--set httpSource.secretName=existing-export \
	--set httpSource.sourceCIDR=192.0.2.10/32 \
	--set httpSource.consumerPodLabels.app=trusted-consumer \
	--set contractProbe.enabled=true \
	--set contractProbe.url=https://contracts.example.com/schema \
	--set contractProbe.targetCIDR=192.0.2.1/32 \
	--set contractProbe.monitorPodLabels.app=monitor)
assert_nonroot_containers "$optional_render" 4 optional

discovery_flag=$(printf '%s' "$default_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "REGISTRY_DISCOVERY_ENABLED") | .value' -)
[ "$discovery_flag" = 'false' ] || fail 'registry discovery must default off'
discovery_render=$(helm template data-product-controller "$chart" --namespace discovery-system --set registryDiscovery.enabled=true)
discovery_flag=$(printf '%s' "$discovery_render" | yq ea 'select(.kind == "Deployment" and .spec.template.spec.containers[0].name == "controller") | .spec.template.spec.containers[0].env[] | select(.name == "REGISTRY_DISCOVERY_ENABLED") | .value' -)
[ "$discovery_flag" = 'true' ] || fail 'registry discovery must be explicitly enableable'

printf '%s\n' 'chart behavior tests passed'
