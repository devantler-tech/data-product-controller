#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
printf '{}\n' >"$work/catalog.json"
export EDC_RELEASE_TEST_ROOT=$root EDC_RELEASE_TEST_WORK=$work

# Supply only the external verifier receipt and stop at the first subsequent image request.
# The actual EDC orchestrator must reject a bad receipt before that observable side effect.
cat >"$work/bin/timeout" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ $1 == --signal=TERM && $2 == --kill-after=5 ]] || exit 99
shift 3
if [[ $1 == docker ]]; then
 case "${2:-}" in
 ps) exit 0 ;;
 pull) touch "$EDC_RELEASE_TEST_WORK/image-requested"; exit 77 ;;
 *) exit 99 ;;
 esac
fi
[[ $1 == bash ]] || exit 99
shift
case "$1" in
"$EDC_RELEASE_TEST_ROOT/tests/dataspace/edc/artifacts.test.sh"|"$EDC_RELEASE_TEST_ROOT/tests/dataspace/edc/deadline.test.sh") exit 0 ;;
"$EDC_RELEASE_TEST_ROOT/scripts/verify-release-artifacts.sh") ;;
*) exit 99 ;;
esac
[[ " $* " == *' --tag v4.0.0 '* ]] || exit 99
if [[ " $* " == *' --profile data-product-controller '* ]]; then
 touch "$EDC_RELEASE_TEST_WORK/profile-selected"
fi
while (($#)); do
 if [[ $1 == --output-dir ]]; then
  mkdir "$2"
  cp "$EDC_RELEASE_TEST_WORK/receipt.json" "$2/release.json"
  exit "${EDC_RELEASE_TEST_VERIFIER_EXIT:-0}"
 fi
 shift
done
exit 99
EOF
chmod +x "$work/bin/timeout"
export PATH="$work/bin:$PATH"
jq -n '{
 complete:true,tag:"v4.0.0",platform:"linux/amd64",releaseProfile:"data-product-controller",
 sourceSHA:"4fbc7d215b3ce30091e74e9909a3ad822e878a05",
 publisherSHA:"ea4df9004d4ab43b6c9af5b0c5f55712f7211328",
 imageDigest:"sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862",
 chartDigest:"sha256:338521dc3e85774d6fff817a1af073ed1a462ee67e61459bf37f15e9a370ec89",
 runtimeDigest:"sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862",
 imageReference:"ghcr.io/devantler-tech/data-product-controller@sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862",
 chartReference:"ghcr.io/devantler-tech/charts/data-product-controller@sha256:338521dc3e85774d6fff817a1af073ed1a462ee67e61459bf37f15e9a370ec89"
}' >"$work/good.json"
for mutation in '.' 'del(.releaseProfile)' '.releaseProfile="khoist"' \
	'del(.imageReference)' '.imageReference="ghcr.io/devantler-tech/khoist@"+.imageDigest' \
	'del(.chartReference)' '.chartReference="ghcr.io/devantler-tech/charts/khoist@"+.chartDigest' \
	'.complete=false' '.platform="linux/arm64"' ambiguous partial; do
	jq '.' "$work/good.json" >"$work/receipt.json"
	verifier_exit=0
	case "$mutation" in
	ambiguous) cat "$work/good.json" >>"$work/receipt.json" ;;
	partial) verifier_exit=42 ;;
	*) jq "$mutation" "$work/good.json" >"$work/receipt.json" ;;
	esac
	rm -f "$work/image-requested" "$work/profile-selected"
	result=0
	EDC_RELEASE_TEST_VERIFIER_EXIT=$verifier_exit "$BASH" "$root/tests/source/edc-import.sh" "$work/catalog.json" >"$work/result.log" 2>&1 || result=$?
	if [[ $mutation == . ]]; then
		[[ $result == 77 && -f $work/image-requested ]] || {
			echo "EDC rejected a complete historical receipt (exit $result)" >&2
			cat "$work/result.log" >&2
			exit 1
		}
	else
		[[ $result != 0 && $result != 77 && $result != 99 && ! -e $work/image-requested ]] || {
			echo "EDC used an incomplete or mixed release receipt: $mutation (exit $result)" >&2
			exit 1
		}
	fi
done
[[ -f $work/profile-selected ]] || {
	echo 'EDC did not select its historical release profile' >&2
	exit 1
}
echo 'EDC rejects missing, mixed, ambiguous and failed release evidence before image requests'
