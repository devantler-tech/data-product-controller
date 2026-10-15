#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/chart/templates"
export REAL_HELM
REAL_HELM=$(command -v helm)
cat >"$work/chart/Chart.yaml" <<'EOF'
apiVersion: v2
name: data-product-controller
version: 1.2.3
appVersion: 1.2.3
EOF
"$REAL_HELM" package "$work/chart" --destination "$work" >/dev/null
yq -i '.name="khoist"' "$work/chart/Chart.yaml"
"$REAL_HELM" package "$work/chart" --destination "$work" >/dev/null
yq -i '.name="data-product-controller"' "$work/chart/Chart.yaml"
export TEST_ARCHIVE="$work/data-product-controller-1.2.3.tgz"
export TEST_CALLER_REPOSITORY=devantler-tech/data-product-controller
export TEST_IMAGE_REPOSITORY=ghcr.io/devantler-tech/data-product-controller
export TEST_CHART_REPOSITORY=ghcr.io/devantler-tech/charts/data-product-controller
export TEST_CHART_SIGNER=https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3
export TEST_OPERATION_LOG="$work/operations"
export TEST_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export TEST_IMAGE_DIGEST=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
export TEST_CHART_DIGEST=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
export TEST_RUNTIME_DIGEST=sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
export TEST_PULL_MARKER="$work/pulled-platform"
cat >"$work/bin/cosign" <<'EOF'
#!/usr/bin/env bash
set -eu
printf 'cosign %s\n' "$*" >>"$TEST_OPERATION_LOG"
[[ $1 == verify ]] || exit 90
args=" $* "
if [[ ${EMPTY_SIGNATURE:-false} == true ]]; then exit 0; fi
if [[ ${MULTI_SIGNATURE:-false} == true ]]; then printf '[{"critical":{"image":{"docker-manifest-digest":"sha256:wrong"}}}]\n'; fi
[[ $args == *' --certificate-oidc-issuer https://token.actions.githubusercontent.com '* &&
   $args == *" --certificate-github-workflow-repository $TEST_CALLER_REPOSITORY "* &&
   $args == *' --certificate-github-workflow-ref refs/tags/v1.2.3 '* &&
   $args == *" --certificate-github-workflow-sha $TEST_SHA "* ]] || exit 91
last=${!#}
case "$last" in
"$TEST_IMAGE_REPOSITORY@$TEST_IMAGE_DIGEST")
 [[ $args == *' --certificate-identity https://github.com/devantler-tech/actions/.github/workflows/publish-app.yaml@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb '* ]] || exit 92
 [[ ${IMAGE_VERIFY_EXIT:-0} == 0 ]] || exit "$IMAGE_VERIFY_EXIT"
 printf '[{"critical":{"image":{"docker-manifest-digest":"%s"}}}]\n' "$TEST_IMAGE_DIGEST" ;;
"$TEST_CHART_REPOSITORY@$TEST_CHART_DIGEST")
 [[ $args == *" --certificate-identity $TEST_CHART_SIGNER "* ]] || exit 93
 [[ ${CHART_VERIFY_EXIT:-0} == 0 ]] || exit "$CHART_VERIFY_EXIT"
 printf '[{"critical":{"image":{"docker-manifest-digest":"%s"}}}]\n' "$TEST_CHART_DIGEST" ;;
*) exit 94 ;;
esac
if [[ ${PARTIAL_SIGNATURE:-false} == true ]]; then exit 42; fi
EOF
cat >"$work/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -eu
printf 'helm %s\n' "$*" >>"$TEST_OPERATION_LOG"
case "$1" in
pull)
 [[ $2 == "oci://$TEST_CHART_REPOSITORY@$TEST_CHART_DIGEST" && $3 == --destination ]] || exit 90
 [[ ${PULL_EXIT:-0} == 0 ]] || exit "$PULL_EXIT"
 cp "$TEST_ARCHIVE" "$4/data-product-controller-1.2.3.tgz" ;;
show) exec "$REAL_HELM" "$@" ;;
*) exit 91 ;;
esac
EOF
cat >"$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
printf 'docker %s\n' "$*" >>"$TEST_OPERATION_LOG"
case "$1 $2" in
'pull --platform')
 [[ $3 == linux/amd64 || $3 == linux/arm64 ]] || exit 93
 [[ $4 == "$TEST_IMAGE_REPOSITORY@$TEST_IMAGE_DIGEST" ]] || exit 94
 [[ ${IMAGE_PULL_EXIT:-0} == 0 ]] || exit "$IMAGE_PULL_EXIT"
 printf '%s\n' "$3" >"$TEST_PULL_MARKER" ;;
'image inspect')
 [[ $3 == "$TEST_IMAGE_REPOSITORY@$TEST_IMAGE_DIGEST" && $4 == --format ]] || exit 90
 [[ -f $TEST_PULL_MARKER ]] || exit 95
 case "$5" in
 '{{index .Config.Labels "org.opencontainers.image.revision"}}') printf '%s\n' "${REVISION:-$TEST_SHA}" ;;
 '{{.Os}}/{{.Architecture}}')
  printf '%s/%s\n' "${RUNTIME_OS:-linux}" "${RUNTIME_ARCH:-amd64}"
  exit "${RUNTIME_INSPECT_EXIT:-0}" ;;
 *) exit 96 ;;
 esac ;;
'buildx imagetools')
 [[ $3 == inspect && $4 == --raw && $5 == "$TEST_IMAGE_REPOSITORY@$TEST_IMAGE_DIGEST" ]] || exit 91
 if [[ ${MANIFEST_EMPTY:-false} == true ]]; then printf '{}\n'; exit; fi
 case "${MANIFEST_KIND:-oci-index}" in
 oci-index) media_type=application/vnd.oci.image.index.v1+json ;;
 docker-index) media_type=application/vnd.docker.distribution.manifest.list.v2+json ;;
 oci-manifest) media_type=application/vnd.oci.image.manifest.v1+json ;;
 docker-manifest) media_type=application/vnd.docker.distribution.manifest.v2+json ;;
 *) exit 97 ;;
 esac
 if [[ ${MANIFEST_KIND:-oci-index} == *-manifest ]]; then
  config_type=application/vnd.oci.image.config.v1+json
  if [[ $MANIFEST_KIND == docker-manifest ]]; then config_type=application/vnd.docker.container.image.v1+json; fi
  printf '{"schemaVersion":2,"mediaType":"%s","config":{"mediaType":"%s","digest":"%s","size":123},"layers":[]}\n' "$media_type" "$config_type" "$TEST_RUNTIME_DIGEST"
 else
  descriptor=$(printf '{"digest":"%s","platform":{"os":"%s","architecture":"%s"}}' "${RUNTIME_DIGEST:-$TEST_RUNTIME_DIGEST}" "${MANIFEST_OS:-linux}" "${MANIFEST_ARCH:-amd64}")
  if [[ ${DUPLICATE_PLATFORM:-false} == true ]]; then descriptor="$descriptor,$descriptor"; fi
  printf '{"schemaVersion":2,"mediaType":"%s","manifests":[%s]}\n' "$media_type" "$descriptor"
 fi ;;
*) exit 92 ;;
esac
EOF
cat >"$work/bin/timeout" <<'EOF'
#!/usr/bin/env bash
set -eu
[[ $1 == --signal=TERM && $2 == --kill-after=5 && $3 =~ ^[1-9][0-9]*s$ ]] || exit 90
[[ ${TIMEOUT_EXIT:-0} == 0 ]] || exit "$TIMEOUT_EXIT"
shift 3
exec "$@"
EOF
cat >"$work/bin/cp" <<'EOF'
#!/usr/bin/env bash
set -eu
if [[ ${SLOW_FINALIZATION:-false} == true && $2 == */release-chart.tgz ]]; then sleep 3; fi
exec /bin/cp "$@"
EOF
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH"
base=(--tag v1.2.3 --source-sha "$TEST_SHA" --image-digest "$TEST_IMAGE_DIGEST"
	--chart-digest "$TEST_CHART_DIGEST" --publisher-sha bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
	--platform linux/amd64)
# Run the real verifier with fresh evidence and a recorded platform-specific pull.
run_verify() {
	rm -rf "$work/result"
	rm -f "$TEST_PULL_MARKER"
	rm -f "$TEST_OPERATION_LOG"
	bash "$root/scripts/verify-release-artifacts.sh" "${base[@]}" --output-dir "$work/result" "$@" >"$work/output" 2>"$work/error"
}
# Report the failed assertion and the verifier's diagnostic output.
fail() {
	echo "release artifact verification test: $1" >&2
	cat "$work/error" >&2
	exit 1
}
# Exercise our verifier; only registry/signer boundaries are replaced, archive parsing remains real.
run_verify || fail 'a complete verified release was rejected'
jq -e --arg runtime "$TEST_RUNTIME_DIGEST" '.complete == true and .runtimeDigest == $runtime and .platform == "linux/amd64"' "$work/result/release.json" >/dev/null || fail 'missing verified platform identity'
"$REAL_HELM" show chart "$work/result/release-chart.tgz" | yq '.version == "1.2.3"' - | grep -Fx true >/dev/null
# Require verification failure without a release receipt.
reject() {
	if run_verify "$@"; then fail 'unverified or mismatched identity produced success'; fi
	[[ ! -f "$work/result/release.json" ]] || fail 'failed verification left a completeness record'
}
# A supported manifest must reach the runtime platform check after its exact pull.
reject_platform() {
	local requested=${1:-linux/amd64}
	reject --platform "$requested"
	jq -e '.reason == "platform-mismatch"' "$work/output" >/dev/null || fail 'runtime platform was not checked'
	[[ $(cat "$TEST_PULL_MARKER") == "$requested" ]] || fail 'runtime platform checked before exact pull'
}
export RUNTIME_OS=windows
reject_platform
unset RUNTIME_OS
export RUNTIME_ARCH=arm64
reject_platform
unset RUNTIME_ARCH
export RUNTIME_INSPECT_EXIT=42
reject
unset RUNTIME_INSPECT_EXIT
export DUPLICATE_PLATFORM=true
reject
unset DUPLICATE_PLATFORM
export MANIFEST_KIND=docker-index
run_verify || fail 'a signed Docker platform index was rejected'
jq -e --arg runtime "$TEST_RUNTIME_DIGEST" '.runtimeDigest == $runtime' "$work/result/release.json" >/dev/null || fail 'Docker index child identity missing'
export MANIFEST_ARCH=arm64 RUNTIME_ARCH=arm64
run_verify --platform linux/arm64 || fail 'a signed arm64 platform index was rejected'
jq -e --arg runtime "$TEST_RUNTIME_DIGEST" '.complete == true and .runtimeDigest == $runtime and .platform == "linux/arm64"' "$work/result/release.json" >/dev/null || fail 'arm64 index child identity missing'
unset MANIFEST_ARCH RUNTIME_ARCH
for kind in docker-manifest oci-manifest; do
	export MANIFEST_KIND="$kind"
	run_verify || fail "a signed $kind release was rejected"
	jq -e --arg runtime "$TEST_IMAGE_DIGEST" '.complete == true and .runtimeDigest == $runtime and .platform == "linux/amd64"' "$work/result/release.json" >/dev/null || fail 'single manifest identity changed'
	[[ $(cat "$TEST_PULL_MARKER") == linux/amd64 ]] || fail 'single manifest was not pulled for the requested platform'
	export RUNTIME_OS=windows
	reject_platform
	unset RUNTIME_OS
	export RUNTIME_ARCH=arm64
	reject_platform
	run_verify --platform linux/arm64 || fail "a signed $kind arm64 release was rejected"
	jq -e --arg runtime "$TEST_IMAGE_DIGEST" '.complete == true and .runtimeDigest == $runtime and .platform == "linux/arm64"' "$work/result/release.json" >/dev/null || fail 'single manifest arm64 identity missing'
	unset RUNTIME_ARCH
	reject_platform linux/arm64
done
unset MANIFEST_KIND
for var in IMAGE_VERIFY_EXIT CHART_VERIFY_EXIT IMAGE_PULL_EXIT PULL_EXIT TIMEOUT_EXIT; do
	export "$var=42"
	reject
	unset "$var"
done
export REVISION=ffffffffffffffffffffffffffffffffffffffff
reject
unset REVISION
export MANIFEST_EMPTY=true
reject
unset MANIFEST_EMPTY
export EMPTY_SIGNATURE=true
reject
unset EMPTY_SIGNATURE
export MULTI_SIGNATURE=true
reject
unset MULTI_SIGNATURE
export SLOW_FINALIZATION=true
reject --timeout 2
unset SLOW_FINALIZATION
export RUNTIME_DIGEST=not-a-digest
reject
unset RUNTIME_DIGEST
reject --tag main
reject --source-sha bad
reject --publisher-sha bad
reject --image-digest latest
reject --chart-digest latest
reject --platform linux/arm64
reject --timeout 0
reject --unknown unsafe
# Profiles select complete fixed identity tuples; the verifier must never try the other tuple.
for profile in data-product-controller khoist; do
	if [[ $profile == khoist ]]; then
		export TEST_CALLER_REPOSITORY=devantler-tech/khoist
		export TEST_IMAGE_REPOSITORY=ghcr.io/devantler-tech/khoist
		export TEST_CHART_REPOSITORY=ghcr.io/devantler-tech/charts/khoist
		export TEST_CHART_SIGNER=https://github.com/devantler-tech/khoist/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3
		other=data-product-controller
	else
		other=khoist
	fi
	export TEST_ARCHIVE="$work/$profile-1.2.3.tgz"
	run_verify --profile "$profile" || fail "complete $profile profile rejected"
	jq -e --arg profile "$profile" --arg image "$TEST_IMAGE_REPOSITORY@$TEST_IMAGE_DIGEST" \
		--arg chart "$TEST_CHART_REPOSITORY@$TEST_CHART_DIGEST" \
		'.complete == true and .releaseProfile == $profile and .imageReference == $image and .chartReference == $chart' \
		"$work/result/release.json" >/dev/null || fail 'receipt omitted the selected artifact profile'
	for boundary in TEST_CALLER_REPOSITORY TEST_IMAGE_REPOSITORY TEST_CHART_REPOSITORY TEST_CHART_SIGNER; do
		saved=${!boundary}
		case "$boundary" in
		TEST_CALLER_REPOSITORY) export "$boundary=devantler-tech/$other" ;;
		TEST_IMAGE_REPOSITORY) export "$boundary=ghcr.io/devantler-tech/$other" ;;
		TEST_CHART_REPOSITORY) export "$boundary=ghcr.io/devantler-tech/charts/$other" ;;
		TEST_CHART_SIGNER) export "$boundary=https://github.com/devantler-tech/$other/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3" ;;
		esac
		reject --profile "$profile"
		export "$boundary=$saved"
	done
	export TEST_ARCHIVE="$work/$other-1.2.3.tgz"
	reject --profile "$profile"
	export TEST_ARCHIVE="$work/$profile-1.2.3.tgz"
	for fault in IMAGE_VERIFY_EXIT CHART_VERIFY_EXIT IMAGE_PULL_EXIT PULL_EXIT TIMEOUT_EXIT; do
		export "$fault=42"
		reject --profile "$profile"
		if [[ -f $TEST_OPERATION_LOG ]] && grep -F "devantler-tech/$other" "$TEST_OPERATION_LOG"; then
			fail 'failed profile silently tried the other identity'
		fi
		unset "$fault"
	done
	export PARTIAL_SIGNATURE=true
	reject --profile "$profile"
	jq -e '.reason == "image-unverified"' "$work/output" >/dev/null || fail 'partial signer failure was hidden'
	unset PARTIAL_SIGNATURE
done
for profile in foreign '' 'khoist/../data-product-controller'; do
	reject --profile "$profile"
	[[ ! -s $TEST_OPERATION_LOG && ! -e $work/result ]] || fail 'unknown profile reached an external operation'
done
reject --profile khoist --profile data-product-controller
[[ ! -s $TEST_OPERATION_LOG && ! -e $work/result ]] || fail 'ambiguous profile reached an external operation'
# An omitted selection must keep the original historical profile.
export TEST_CALLER_REPOSITORY=devantler-tech/data-product-controller
export TEST_IMAGE_REPOSITORY=ghcr.io/devantler-tech/data-product-controller
export TEST_CHART_REPOSITORY=ghcr.io/devantler-tech/charts/data-product-controller
export TEST_CHART_SIGNER=https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3
export TEST_ARCHIVE="$work/data-product-controller-1.2.3.tgz"
run_verify || fail 'default historical identity changed'
jq -e '.releaseProfile == "data-product-controller"' "$work/result/release.json" >/dev/null || fail 'default profile was not recorded'
sed 's/1.2.3/1.2.4/g' "$work/chart/Chart.yaml" >"$work/chart/next.yaml"
mv "$work/chart/next.yaml" "$work/chart/Chart.yaml"
"$REAL_HELM" package "$work/chart" --destination "$work" >/dev/null
export TEST_ARCHIVE="$work/data-product-controller-1.2.4.tgz"
reject
echo 'release artifact verification behavior tests passed'
