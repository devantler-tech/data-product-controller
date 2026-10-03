#!/usr/bin/env bash
# Verify declared public release identities without using shared registry credentials.
set -euo pipefail
umask 077
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
evidence_created=false
fail() {
	if [[ $evidence_created == true ]]; then rm -f "$output_dir/release.json"; fi
	printf '{"complete":false,"reason":"%s"}\n' "$1"
	exit 1
}
tag='' source_sha='' image_digest='' chart_digest='' publisher_sha='' platform='' output_dir=''
budget=180
while [[ $# -gt 0 ]]; do
	[[ $# -ge 2 ]] || fail configuration
	case "$1" in
	--tag) tag=$2 ;;
	--source-sha) source_sha=$2 ;;
	--image-digest) image_digest=$2 ;;
	--chart-digest) chart_digest=$2 ;;
	--publisher-sha) publisher_sha=$2 ;;
	--platform) platform=$2 ;;
	--output-dir) output_dir=$2 ;;
	--timeout) budget=$2 ;;
	*) fail configuration ;;
	esac
	shift 2
done
[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && $source_sha =~ ^[a-f0-9]{40}$ &&
	$publisher_sha =~ ^[a-f0-9]{40}$ && $image_digest =~ ^sha256:[a-f0-9]{64}$ &&
	$chart_digest =~ ^sha256:[a-f0-9]{64}$ && $platform =~ ^linux/(amd64|arm64)$ &&
	$budget =~ ^[1-9][0-9]{0,2}$ && $budget -le 600 && -n $output_dir ]] || fail configuration
[[ ! -e $output_dir && ! -L $output_dir ]] || fail evidence-path-exists
for tool in cosign docker helm jq yq; do command -v "$tool" >/dev/null || fail prerequisite; done
timeout_tool=$(command -v timeout || command -v gtimeout || true)
[[ -n $timeout_tool ]] || fail prerequisite
mkdir -m 700 "$output_dir" || fail evidence-directory
evidence_created=true
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'fail interrupted' INT TERM HUP
mkdir "$work/registry" "$work/charts"
printf '{"auths":{}}\n' >"$work/registry/config.json"
export DOCKER_CONFIG="$work/registry" HELM_REGISTRY_CONFIG="$work/registry/config.json"
deadline=$((SECONDS + budget))
bounded() {
	local remaining=$((deadline - SECONDS))
	((remaining > 0)) || return 124
	"$timeout_tool" --signal=TERM --kill-after=5 "${remaining}s" "$@"
}
image="ghcr.io/devantler-tech/data-product-controller@$image_digest"
chart="ghcr.io/devantler-tech/charts/data-product-controller@$chart_digest"
issuer=https://token.actions.githubusercontent.com
caller=(--certificate-oidc-issuer "$issuer"
	--certificate-github-workflow-repository devantler-tech/data-product-controller
	--certificate-github-workflow-ref "refs/tags/$tag"
	--certificate-github-workflow-sha "$source_sha")
signature_bound() {
	[[ $(wc -c <"$1") -le 1048576 ]] && bounded jq -se --arg mode signature --arg digest "$2" \
		-f "$script_dir/verify-release-artifacts.jq" "$1" >/dev/null 2>&1
}
bounded cosign verify --certificate-identity \
	"https://github.com/devantler-tech/actions/.github/workflows/publish-app.yaml@$publisher_sha" \
	"${caller[@]}" "$image" >"$output_dir/image-signature.json" 2>"$output_dir/image-verification.log" || fail image-unverified
signature_bound "$output_dir/image-signature.json" "$image_digest" || fail image-signature-incomplete
bounded cosign verify --certificate-identity \
	"https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/$tag" \
	"${caller[@]}" "$chart" >"$output_dir/chart-signature.json" 2>"$output_dir/chart-verification.log" || fail chart-unverified
signature_bound "$output_dir/chart-signature.json" "$chart_digest" || fail chart-signature-incomplete
bounded docker buildx imagetools inspect --raw "$image" >"$output_dir/image-manifest.json" 2>"$output_dir/manifest-read.log" || fail manifest-unverified
[[ $(wc -c <"$output_dir/image-manifest.json") -le 262144 ]] || fail manifest-bound
architecture=${platform#linux/}
runtime_digest=$(bounded jq -ser --arg mode manifest --arg arch "$architecture" \
	-f "$script_dir/verify-release-artifacts.jq" "$output_dir/image-manifest.json" 2>"$output_dir/manifest-selection.log") || fail platform-unverified
bounded docker pull --platform "$platform" "$image" >"$output_dir/image-pull.log" 2>&1 || fail image-read-unverified
revision=$(bounded docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>"$output_dir/image-revision.log") || fail revision-unverified
[[ $revision == "$source_sha" ]] || fail revision-mismatch
bounded helm pull "oci://$chart" --destination "$work/charts" >"$output_dir/chart-pull.log" 2>&1 || fail chart-read-unverified
shopt -s nullglob
archives=("$work/charts/"*.tgz)
[[ ${#archives[@]} == 1 && -f ${archives[0]} && $(wc -c <"${archives[0]}") -le 16777216 ]] || fail chart-archive-unverified
bounded helm show chart "${archives[0]}" >"$output_dir/chart-metadata.yaml" 2>"$output_dir/chart-metadata.log" || fail chart-metadata-unverified
version=${tag#v}
export RELEASE_EXPECTED_VERSION="$version"
bounded yq -e '.name == "data-product-controller" and .version == strenv(RELEASE_EXPECTED_VERSION) and .appVersion == strenv(RELEASE_EXPECTED_VERSION)' "$output_dir/chart-metadata.yaml" >/dev/null 2>&1 || fail chart-version-mismatch
((SECONDS < deadline)) || fail deadline
bounded cp "${archives[0]}" "$output_dir/release-chart.tgz" || fail finalization
bounded jq -n --arg mode receipt --arg tag "$tag" --arg sha "$source_sha" --arg publisher "$publisher_sha" \
	--arg image "$image_digest" --arg chart "$chart_digest" --arg platform "$platform" --arg runtime "$runtime_digest" \
	-f "$script_dir/verify-release-artifacts.jq" \
	>"$work/release.json" || fail finalization
((SECONDS < deadline)) || fail deadline
bounded mv "$work/release.json" "$output_dir/release.json" || fail finalization
((SECONDS < deadline)) || fail deadline
bounded cat "$output_dir/release.json" || fail finalization
