#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
mega_config="$repo_root/.mega-linter.yml"
chart="$repo_root/charts/data-product-controller"
trivy_ignore="$repo_root/.trivyignore.yaml"

# fail rejects a scanner exception outside its reviewed contract.
fail() {
	printf '%s\n' "scanner suppression test failed: $1" >&2
	exit 1
}

command -v yq >/dev/null 2>&1 || fail 'yq is required to validate scanner suppressions'

checkov_arguments=$(yq '.REPOSITORY_CHECKOV_ARGUMENTS // ""' "$mega_config")
case "$checkov_arguments" in
*--skip-check*) fail 'Checkov checks must be suppressed on explicit artifacts, not repository-wide' ;;
esac

rendered=$(helm template data-product-controller "$chart" \
	--namespace data-product-system \
	--set route.enabled=true)
actual_checkov_allowlist=$(
	{
		# shellcheck disable=SC2016 # $resource is a yq variable, not a shell expansion.
		printf '%s' "$rendered" |
			yq e -N 'select(.metadata.annotations != null) |
        (.kind + "/" + .metadata.name) as $resource |
        .metadata.annotations | to_entries[] |
        select(.key | test("^checkov\\.io/skip[0-9]*$")) |
        "rendered:" + $resource + " " + (.value | split("=")[0])' -
		# shellcheck disable=SC2016 # $resource is a yq variable, not a shell expansion.
		SCANNER_REPO_ROOT=$repo_root yq e -N 'select(.metadata.annotations != null) |
		(.kind + "/" + .metadata.name) as $resource |
      .metadata.annotations | to_entries[] |
      select(.key | test("^checkov\\.io/skip[0-9]*$")) |
      (filename | sub("^" + strenv(SCANNER_REPO_ROOT) + "/"; "")) + ":" + $resource + " " + (.value | split("=")[0])' \
			"$repo_root"/deploy/*.yaml "$repo_root"/tests/source/*.yaml "$repo_root"/tests/provider/*.yaml
		sed -n 's/^[[:space:]]*#checkov:skip=\([^:[:space:]]*\).*/Dockerfile \1/p' "$repo_root/Dockerfile"
	} | sort
)
expected_checkov_allowlist=$(
	printf '%s\n' \
		'Dockerfile CKV_DOCKER_2' \
		'deploy/deployment.yaml:Deployment/data-product-controller CKV_K8S_14' \
		'deploy/deployment.yaml:Deployment/data-product-controller CKV_K8S_38' \
		'deploy/deployment.yaml:Deployment/data-product-controller CKV_K8S_43' \
		'rendered:Deployment/data-product-controller CKV_K8S_21' \
		'rendered:Deployment/data-product-controller CKV_K8S_38' \
		'rendered:Deployment/data-product-controller CKV_K8S_43' \
		'rendered:Deployment/data-product-controller-harbour CKV_K8S_21' \
		'rendered:Deployment/data-product-controller-harbour CKV_K8S_43' \
		'rendered:Role/data-product-controller-leader-election CKV_K8S_21' \
		'rendered:RoleBinding/data-product-controller-leader-election CKV_K8S_21' \
		'rendered:Service/data-product-controller CKV_K8S_21' \
		'rendered:Service/data-product-controller-harbour CKV_K8S_21' \
		'rendered:ServiceAccount/data-product-controller CKV_K8S_21' \
		'tests/source/consumer.yaml:Pod/consumer CKV_K8S_43' \
		'tests/provider/workloads.yaml:Deployment/document-query CKV_K8S_43' \
		'tests/provider/workloads.yaml:Pod/document-writer CKV_K8S_43' \
		'tests/provider/workloads.yaml:Pod/document-consumer CKV_K8S_43' |
		sort
)
[ "$actual_checkov_allowlist" = "$expected_checkov_allowlist" ] ||
	fail 'Checkov suppressions must match the approved rule-and-artifact allowlist'

[ -f "$trivy_ignore" ] || fail 'Trivy suppressions must use the structured path-scoped ignore file'
trivy_arguments=$(yq '.REPOSITORY_TRIVY_ARGUMENTS // ""' "$mega_config")
[ "$trivy_arguments" = '--ignorefile .trivyignore.yaml' ] ||
	fail 'MegaLinter must load the structured Trivy ignore file'
unscoped_trivy_ignores=$(
	yq '[.misconfigurations[] | select(.paths == null or (.paths | length) == 0)] | length' "$trivy_ignore"
)
[ "$unscoped_trivy_ignores" = '0' ] || fail 'every Trivy suppression must have an artifact path allowlist'
actual_trivy_allowlist=$(
	yq e -N '.misconfigurations[] | .id + " " + .paths[]' "$trivy_ignore" |
		sort
)
expected_trivy_allowlist=$(
	printf '%s\n' \
		'DS-0026 Dockerfile' \
		'KSV-0013 deploy/deployment.yaml' \
		'KSV-0113 docs/examples/document-provider-observer-rbac.yaml' \
		'KSV-0113 docs/examples/graph-provider-observer-rbac.yaml' \
		'KSV-0113 docs/examples/sql-provider-observer-rbac.yaml' \
		'KSV-0125 charts/data-product-controller/templates/controller-deployment.yaml' \
		'KSV-0125 charts/data-product-controller/templates/demo-deployment.yaml' \
		'KSV-0125 deploy/deployment.yaml' \
		'KSV-0125 tests/source/consumer.yaml' |
		sort
)
[ "$actual_trivy_allowlist" = "$expected_trivy_allowlist" ] ||
	fail 'Trivy suppressions must match the approved artifact allowlist'

# The one module-wide advisory is specific to an unused, unmaintained package.
# The required independent fixture test rejects importing it, including in tests.
trivy_vulnerability_exceptions=$(yq -o=json -I=0 '.vulnerabilities' "$trivy_ignore")
trivy_vulnerability_scope=$(printf '%s' "$trivy_vulnerability_exceptions" |
	yq -o=json -I=0 '[.[] | {"id":.id,"paths":.paths,"expired_at":.expired_at}]')
[ "$trivy_vulnerability_scope" = '[{"id":"GO-2026-5932","paths":["tests/provider/fixture/go.mod"],"expired_at":"2026-11-02"}]' ] ||
	fail 'vulnerability exceptions must stay bound to the unused OpenPGP package, fixture module and expiration'

# RBAC cannot restrict Secret gets to metadata. Every suppressed example must stay namespaced
# and contain only its two exact-name GET rules; extra or wildcard rules must also fail.
# check_observer_grants bounds each scanner exception to its approved exact-name GETs.
check_observer_grants() {
	observer_rbac="$repo_root/docs/examples/$1-provider-observer-rbac.yaml"
	expected_grants=$2
	resource_kinds=$(yq ea -N '[.kind] | sort' "$observer_rbac" | yq -o=json -I=0 '.')
	[ "$resource_kinds" = '["Role","RoleBinding"]' ] || fail 'observer examples must contain only one Role and RoleBinding'
	namespaces=$(yq ea -N '[.metadata.namespace] | unique' "$observer_rbac" | yq -o=json -I=0 '.')
	[ "$namespaces" = '["products"]' ] || fail 'observer grants must stay in the product namespace'
	grants=$(yq ea -N -o=json -I=0 'select(.kind == "Role") | [.rules[] | [.apiGroups, .resources, .resourceNames, .verbs]]' "$observer_rbac")
	[ "$grants" = "$expected_grants" ] || fail 'observer grants must stay limited to getting their named source and application Secret'
}
check_observer_grants sql '[[["postgresql.cnpg.io"],["clusters"],["warehouse"],["get"]],[[""],["secrets"],["warehouse-app"],["get"]]]'
check_observer_grants document '[[["psmdb.percona.com"],["perconaservermongodbs"],["documents"],["get"]],[[""],["secrets"],["documents-reader"],["get"]]]'
check_observer_grants graph '[[["database.arangodb.com"],["arangodeployments"],["lineage"],["get"]],[[""],["secrets"],["lineage-reader"],["get"]]]'

printf '%s\n' 'scanner suppression tests passed'
