#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
package_dir=$(mktemp -d)
trap 'rm -rf "$package_dir"' EXIT HUP INT TERM

fail() {
	printf '%s\n' "chart image test failed: $1" >&2
	exit 1
}

# Package exactly as publication does, with versions different from the source
# placeholders and each other. Image selection must follow appVersion.
helm package "$chart" --version 9.8.7 --app-version 7.6.5 --destination "$package_dir" >/dev/null
package="$package_dir/data-product-controller-9.8.7.tgz"

assert_images() {
	expected=$1
	expected_count=$2
	shift 2
	rendered=$(helm template image-test "$package" --namespace products "$@")
	images=$(printf '%s' "$rendered" | yq ea -N 'select(.kind == "Deployment") | .spec.template.spec.containers[].image' -)
	count=$(printf '%s\n' "$images" | wc -l | tr -d ' ')
	[ "$count" = "$expected_count" ] || fail "expected $expected_count workload images, got $count"
	while IFS= read -r image; do
		[ "$image" = "$expected" ] || fail "expected $expected, got $image"
	done <<EOF
$images
EOF
}

assert_images ghcr.io/devantler-tech/data-product-controller:7.6.5 2
assert_images ghcr.io/devantler-tech/data-product-controller:custom-build 2 --set-string image.tag=custom-build
assert_images example.test/products/controller:7.6.5 2 --set image.repository=example.test/products/controller

digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
assert_images "ghcr.io/devantler-tech/data-product-controller@$digest" 3 \
	--set-string image.tag=ignored-tag --set image.digest="$digest" \
	--set httpSource.enabled=true --set httpSource.secretName=existing-export \
	--set httpSource.sourceCIDR=192.0.2.10/32 \
	--set httpSource.consumerPodLabels.app=trusted-consumer

# The publisher uses semver image tags without a leading v.
helm package "$chart" --version 9.8.7 --app-version v7.6.5 --destination "$package_dir" >/dev/null
assert_images ghcr.io/devantler-tech/data-product-controller:7.6.5 2

printf '%s\n' 'packaged chart image behavior tests passed'
