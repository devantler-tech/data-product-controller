#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
mega_config="$repo_root/.mega-linter.yml"
chart="$repo_root/charts/data-product-controller"
trivy_ignore="$repo_root/.trivyignore.yaml"

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
		SCANNER_REPO_ROOT=$repo_root yq e -N 'select(.metadata.annotations != null) |
      .metadata.annotations | to_entries[] |
      select(.key | test("^checkov\\.io/skip[0-9]*$")) |
      (filename | sub("^" + strenv(SCANNER_REPO_ROOT) + "/"; "")) + " " + (.value | split("=")[0])' \
			"$repo_root"/deploy/*.yaml
		sed -n 's/^[[:space:]]*#checkov:skip=\([^:[:space:]]*\).*/Dockerfile \1/p' "$repo_root/Dockerfile"
	} | sort
)
expected_checkov_allowlist=$(
	printf '%s\n' \
		'Dockerfile CKV_DOCKER_2' \
		'deploy/deployment.yaml CKV_K8S_14' \
		'deploy/deployment.yaml CKV_K8S_38' \
		'deploy/deployment.yaml CKV_K8S_43' \
		'rendered:Deployment/data-product-controller CKV_K8S_21' \
		'rendered:Deployment/data-product-controller CKV_K8S_38' \
		'rendered:Deployment/data-product-controller CKV_K8S_43' \
		'rendered:Deployment/data-product-controller-harbour CKV_K8S_21' \
		'rendered:Deployment/data-product-controller-harbour CKV_K8S_43' \
		'rendered:Role/data-product-controller-leader-election CKV_K8S_21' \
		'rendered:RoleBinding/data-product-controller-leader-election CKV_K8S_21' \
		'rendered:Service/data-product-controller CKV_K8S_21' \
		'rendered:Service/data-product-controller-harbour CKV_K8S_21' \
		'rendered:ServiceAccount/data-product-controller CKV_K8S_21' |
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
		'KSV-0125 charts/data-product-controller/templates/controller-deployment.yaml' \
		'KSV-0125 charts/data-product-controller/templates/demo-deployment.yaml' \
		'KSV-0125 deploy/deployment.yaml' |
		sort
)
[ "$actual_trivy_allowlist" = "$expected_trivy_allowlist" ] ||
	fail 'Trivy suppressions must match the approved artifact allowlist'

printf '%s\n' 'scanner suppression tests passed'
