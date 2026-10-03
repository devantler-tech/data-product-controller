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
export TEST_ARCHIVE="$work/data-product-controller-1.2.3.tgz"
export TEST_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export TEST_IMAGE_DIGEST=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
export TEST_CHART_DIGEST=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
export TEST_RUNTIME_DIGEST=sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
cat >"$work/bin/cosign" <<'EOF'
#!/usr/bin/env bash
set -eu
[[ $1 == verify ]] || exit 90
args=" $* "
if [[ ${EMPTY_SIGNATURE:-false} == true ]]; then exit 0; fi
if [[ ${MULTI_SIGNATURE:-false} == true ]]; then printf '[{"critical":{"image":{"docker-manifest-digest":"sha256:wrong"}}}]\n'; fi
[[ $args == *' --certificate-oidc-issuer https://token.actions.githubusercontent.com '* &&
   $args == *' --certificate-github-workflow-repository devantler-tech/data-product-controller '* &&
   $args == *' --certificate-github-workflow-ref refs/tags/v1.2.3 '* &&
   $args == *" --certificate-github-workflow-sha $TEST_SHA "* ]] || exit 91
last=${!#}
case "$last" in
"ghcr.io/devantler-tech/data-product-controller@$TEST_IMAGE_DIGEST")
 [[ $args == *' --certificate-identity https://github.com/devantler-tech/actions/.github/workflows/publish-app.yaml@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb '* ]] || exit 92
 [[ ${IMAGE_VERIFY_EXIT:-0} == 0 ]] || exit "$IMAGE_VERIFY_EXIT"
 printf '[{"critical":{"image":{"docker-manifest-digest":"%s"}}}]\n' "$TEST_IMAGE_DIGEST" ;;
"ghcr.io/devantler-tech/charts/data-product-controller@$TEST_CHART_DIGEST")
 [[ $args == *' --certificate-identity https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3 '* ]] || exit 93
 [[ ${CHART_VERIFY_EXIT:-0} == 0 ]] || exit "$CHART_VERIFY_EXIT"
 printf '[{"critical":{"image":{"docker-manifest-digest":"%s"}}}]\n' "$TEST_CHART_DIGEST" ;;
*) exit 94 ;;
esac
EOF
cat >"$work/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -eu
case "$1" in
pull)
 [[ $2 == "oci://ghcr.io/devantler-tech/charts/data-product-controller@$TEST_CHART_DIGEST" && $3 == --destination ]] || exit 90
 [[ ${PULL_EXIT:-0} == 0 ]] || exit "$PULL_EXIT"
 cp "$TEST_ARCHIVE" "$4/data-product-controller-1.2.3.tgz" ;;
show) exec "$REAL_HELM" "$@" ;;
*) exit 91 ;;
esac
EOF
cat >"$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
case "$1 $2" in
'pull --platform')
 [[ $3 == linux/amd64 || $3 == linux/arm64 ]] || exit 93
 [[ $4 == "ghcr.io/devantler-tech/data-product-controller@$TEST_IMAGE_DIGEST" ]] || exit 94
 exit "${IMAGE_PULL_EXIT:-0}" ;;
'image inspect')
 [[ $3 == "ghcr.io/devantler-tech/data-product-controller@$TEST_IMAGE_DIGEST" && $4 == --format ]] || exit 90
 printf '%s\n' "${REVISION:-$TEST_SHA}" ;;
'buildx imagetools')
 [[ $3 == inspect && $4 == --raw && $5 == "ghcr.io/devantler-tech/data-product-controller@$TEST_IMAGE_DIGEST" ]] || exit 91
 if [[ ${MANIFEST_EMPTY:-false} == true ]]; then printf '{}\n'; exit; fi
 printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"%s","platform":{"os":"linux","architecture":"amd64"}}]}\n' "${RUNTIME_DIGEST:-$TEST_RUNTIME_DIGEST}" ;;
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
run_verify() {
	rm -rf "$work/result"
	bash "$root/scripts/verify-release-artifacts.sh" "${base[@]}" --output-dir "$work/result" "$@" >"$work/output" 2>"$work/error"
}
fail() {
	echo "release artifact verification test: $1" >&2
	cat "$work/error" >&2
	exit 1
}
# Exercise our verifier; only registry/signer boundaries are replaced, archive parsing remains real.
run_verify || fail 'a complete verified release was rejected'
jq -e --arg runtime "$TEST_RUNTIME_DIGEST" '.complete == true and .runtimeDigest == $runtime and .platform == "linux/amd64"' "$work/result/release.json" >/dev/null || fail 'missing verified platform identity'
"$REAL_HELM" show chart "$work/result/release-chart.tgz" | yq '.version == "1.2.3"' - | grep -Fx true >/dev/null
reject() {
	if run_verify "$@"; then fail 'unverified or mismatched identity produced success'; fi
	[[ ! -f "$work/result/release.json" ]] || fail 'failed verification left a completeness record'
}
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
sed 's/1.2.3/1.2.4/g' "$work/chart/Chart.yaml" >"$work/chart/next.yaml"
mv "$work/chart/next.yaml" "$work/chart/Chart.yaml"
"$REAL_HELM" package "$work/chart" --destination "$work" >/dev/null
export TEST_ARCHIVE="$work/data-product-controller-1.2.4.tgz"
reject
echo 'release artifact verification behavior tests passed'
