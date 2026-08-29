#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
workflow="$repo_root/.github/workflows/cd.yaml"
deployment="$repo_root/deploy/deployment.yaml"
values="$repo_root/charts/data-product-controller/values.yaml"
generated_crd="$repo_root/config/crd/bases/data.devantler.tech_dataproducts.yaml"
deploy_crd="$repo_root/deploy/data.devantler.tech_dataproducts.yaml"

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
container_count=$(yq '[.spec.template.spec.containers[] | select(.name == "data-product-controller")] | length' "$deployment")
[ "$container_count" = '1' ] ||
  fail 'deployment must contain exactly one data-product-controller container'
[ "$(yq '.spec.template.spec.containers[] | select(.name == "data-product-controller") | .image' "$deployment")" = 'ghcr.io/devantler-tech/data-product-controller:latest' ] ||
  fail 'publish-app must receive the expected mutable image placeholder'

[ "$(yq '.image.tag' "$values")" = '0.1.0' ] ||
  fail 'the chart tag must match docker metadata semver output (without the v prefix)'

printf '%s\n' 'release contract tests passed'
