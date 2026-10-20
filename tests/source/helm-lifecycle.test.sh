#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
real_helm=$(command -v helm)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export HELM_TEST_RELEASE="$work/release.json"
mkdir "$work/bin"
cat >"$work/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -eu
while [[ $1 == --* ]]; do shift 2; done
case "$1 $2 $3" in
'get metadata dpc'|'get metadata dpc-publication') jq '.metadata' "$HELM_TEST_RELEASE" ;;
'get values dpc'|'get values dpc-publication')
 [[ " $* " == *' --revision 7 '* ]] || exit 98
 jq '.values' "$HELM_TEST_RELEASE"
 if [[ ${CHANGE_REVISION:-false} == true ]]; then jq '.metadata.revision=8' "$HELM_TEST_RELEASE" >"$HELM_TEST_RELEASE.next"; mv "$HELM_TEST_RELEASE.next" "$HELM_TEST_RELEASE"; fi ;;
# This matches Helm's real status output: rel.Chart is intentionally omitted.
'status dpc --output') jq '{info:{status:.metadata.status},version:.metadata.revision,config:.values}' "$HELM_TEST_RELEASE" ;;
*) exit 99 ;;
esac
EOF
chmod +x "$work/bin/helm"
export PATH="$work/bin:$PATH"
export KUBECONFIG="$work/config"
cluster_context=owned-fixture
repo_root=$root
test_dir=$work
integration_deadline=$((SECONDS + 120))
source "$root/tests/source/chart-publication.sh"
source "$root/tests/source/helm-lifecycle.sh"
digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
jq -n --arg digest "$digest" '{metadata:{status:"deployed",revision:7,chart:"data-product-controller",version:"1.2.3",appVersion:"1.2.3"},values:{image:{repository:"ghcr.io/devantler-tech/data-product-controller",digest:$digest}}}' >"$work/good.json"
cp "$work/good.json" "$HELM_TEST_RELEASE"
helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller
[[ $(helm_installed_revision dpc-publication 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller) == 7 ]]
# The expected chart name is explicit: a release of the other chart name, or no name, is rejected.
for chart in khoist ''; do
	if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" "$chart"; then
		echo "installed release identity accepted chart name '$chart'" >&2
		exit 1
	fi
done
jq '.metadata.chart="khoist"' "$work/good.json" >"$HELM_TEST_RELEASE"
helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" khoist
cp "$work/good.json" "$HELM_TEST_RELEASE"
for mutation in '.metadata.status="pending-upgrade"' '.metadata.version="1.2.4"' '.metadata.appVersion="1.2.4"' '.values.image.digest="latest"' '.values.image.repository="unapproved.example/controller"' '.metadata.chart="other"' '.metadata.revision=0'; do
	jq "$mutation" "$work/good.json" >"$HELM_TEST_RELEASE"
	if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller; then
		echo 'installed release identity accepted drift' >&2
		exit 1
	fi
	if helm_installed_revision dpc-publication 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller >"$work/rejected-revision"; then
		echo 'publication revision accepted drift' >&2
		exit 1
	fi
	[[ ! -s "$work/rejected-revision" ]]
done
cp "$work/good.json" "$HELM_TEST_RELEASE"
export CHANGE_REVISION=true
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller; then
	echo 'installed identity accepted a release changed during readback' >&2
	exit 1
fi
unset CHANGE_REVISION
printf '{}\n' >"$HELM_TEST_RELEASE"
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller; then
	echo 'installed release identity accepted incomplete response' >&2
	exit 1
fi
echo 'installed Helm identity behavior tests passed'

# The real observation must request the page shipped by each installed release.
# Other rollout and registry checks retain their independent behavior controls.
product_digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
candidate_chart=$work/candidate.tgz
source "$root/tests/source/installed-matrix.sh"
bash() {
	[[ $1 == "$repo_root/scripts/observe-rollout.sh" ]] || return 1
	printf '%s\n' "$*" >"$work/observation-args"
}
kube() {
	[[ "$*" == 'get dataproduct existing-export -o json' ]] || return 99
	printf '%s\n' '{"metadata":{"uid":"product-1"}}'
}
registry_ready() { [[ $1 == true ]]; }
registry_replicas_ready() {
	if [[ ${1:-KHoist} != "$wanted_registry_title" ]]; then
		printf 'wrong registry page for %s: expected %s, requested %s\n' \
			"$phase" "$wanted_registry_title" "${1:-KHoist}" >&2
		return 1
	fi
}
matrix_product_uid=product-1
# Only a job running the contracts family declares a contract check, so only it requires ContractsReady.
for matrix_contracts in true false; do
	for observation in 'candidate:KHoist' 'rollback:Data products' 'restored-candidate:KHoist'; do
		phase=${observation%%:*}
		wanted_registry_title=${observation#*:}
		matrix_observation "$phase" candidate-image "$product_digest"
		grep -Fq -- '--condition Ready --condition ConnectorReady' "$work/observation-args"
		grep -Fq -- '--deployment dpc-contract-probe:contract-probe' "$work/observation-args"
		if [[ $matrix_contracts == true ]]; then
			grep -Fq -- '--condition ContractsReady' "$work/observation-args"
		elif grep -Fq ContractsReady "$work/observation-args"; then
			echo 'a matrix without the contracts family required contract readiness' >&2
			exit 1
		fi
	done
done
unset matrix_contracts
if matrix_observation candidate candidate-image "$product_digest" 2>/dev/null; then
	echo 'matrix observation ran without a family selection' >&2
	exit 1
fi
echo 'installed matrix observation selects each released registry page and the conditions its families declare'

# Named --ignore-not-found reads succeed without output when both objects are absent.
# Exercise the real lifecycle preflight and the shared post-uninstall guard; only external I/O is fault-injected.
cat >"$work/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -eu
while [[ $1 == --* ]]; do shift 2; done
[[ "$*" == 'list --all --filter ^dpc-publication$ --output json --namespace products' ]] || exit 99
printf '[]\n'
EOF
cat >"$work/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
set -eu
[[ "$*" == "--kubeconfig $KUBECONFIG --context owned-fixture --request-timeout=15s --namespace products get dataproduct harbour-observations ksail-releases --ignore-not-found -o "* ]] || exit 99
case "$PUBLICATION_READ_MODE" in
absent) ;;
harbour) printf 'dataproduct.data.devantler.tech/harbour-observations\n' ;;
ksail) printf 'dataproduct.data.devantler.tech/ksail-releases\n' ;;
both) printf 'dataproduct.data.devantler.tech/harbour-observations\ndataproduct.data.devantler.tech/ksail-releases\n' ;;
unavailable) echo 'synthetic transport failure' >&2; exit 8 ;;
partial) printf 'dataproduct.data.devantler.tech/harbour-observations\n'; exit 8 ;;
*) exit 99 ;;
esac
EOF
chmod +x "$work/bin/helm" "$work/bin/kubectl"
if ! command -v timeout >/dev/null; then ln -s "$(command -v gtimeout)" "$work/bin/timeout"; fi
export PUBLICATION_TEST_ROOT=$root PUBLICATION_TEST_WORK=$work
cat >"$work/publication-check.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
repo_root=$PUBLICATION_TEST_ROOT
test_dir=$PUBLICATION_TEST_WORK
cluster_context=owned-fixture
integration_deadline=$((SECONDS + 60))
candidate_chart=$test_dir/candidate.tgz
source "$repo_root/tests/source/chart-publication.sh"
# Reaching signature verification proves the empty-state preflight did not abort or suppress a failed API read.
bash() {
 [[ $1 == "$repo_root/scripts/verify-release-artifacts.sh" &&
    " $* " == *' --profile data-product-controller '* ]] || return 99
 touch "$test_dir/verification-reached"
 return 77
}
if [[ $1 == preflight ]]; then chart_publication_lifecycle; else publication_products_absent; fi
EOF
for stage in preflight cleanup; do
	for mode in absent harbour ksail both unavailable partial; do
		rm -f "$work/verification-reached"
		result=0
		PUBLICATION_READ_MODE=$mode "$BASH" "$work/publication-check.sh" "$stage" >"$work/publication.log" 2>&1 || result=$?
		if [[ $mode == absent ]]; then
			wanted=0
			if [[ $stage == preflight ]]; then wanted=77; fi
			[[ $result == "$wanted" ]] || {
				echo "$stage rejected successful absence (exit $result, expected $wanted)" >&2
				cat "$work/publication.log" >&2
				exit 1
			}
			if [[ $stage == preflight ]]; then [[ -f "$work/verification-reached" ]]; fi
		else
			[[ $result != 0 && $result != 77 && ! -f "$work/verification-reached" ]] || {
				echo "$stage accepted existing or unreadable products: $mode" >&2
				exit 1
			}
		fi
	done
done
echo 'publication preflight and cleanup accept successful absence and reject present or incompletely read products'

source "$root/tests/source/ui-free-rollback.sh"
jq -n '{complete:true,tag:"v5.0.0",platform:"linux/amd64",
  releaseProfile:"data-product-controller",
  imageReference:"ghcr.io/devantler-tech/data-product-controller@sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c",
  chartReference:"ghcr.io/devantler-tech/charts/data-product-controller@sha256:0707d9669f46cde154e4d9f38ef4d47bc4312e663f0e21d12a8afedb6e93e0ea",
  sourceSHA:"29b605885064a3b9f24c799a0d7112d3a6d4333b",
  publisherSHA:"ea4df9004d4ab43b6c9af5b0c5f55712f7211328",
  imageDigest:"sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c",
  chartDigest:"sha256:0707d9669f46cde154e4d9f38ef4d47bc4312e663f0e21d12a8afedb6e93e0ea",
  runtimeDigest:"sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c"}' >"$work/verified.json"
ui_free_release_receipt "$work/verified.json"
for mutation in 'del(.releaseProfile)' '.releaseProfile="khoist"' '.releaseProfile=[]' \
	'del(.imageReference)' '.imageReference="ghcr.io/devantler-tech/khoist@sha256:b59ad9e0d5b09e9a3eb8f453646f016c0348cd5877c943d12ff71476fec1498c"' \
	'del(.chartReference)' '.chartReference="ghcr.io/devantler-tech/charts/khoist@sha256:0707d9669f46cde154e4d9f38ef4d47bc4312e663f0e21d12a8afedb6e93e0ea"' \
	'.complete=false' '.tag="v4.0.0"' '.platform="linux/arm64"' \
	'.sourceSHA="unknown"' '.publisherSHA="unknown"' '.imageDigest="latest"' \
	'.chartDigest="latest"' '.runtimeDigest=null' '.runtimeDigest="sha256:short"' '.runtimeDigest=7'; do
	jq "$mutation" "$work/verified.json" >"$work/rejected.json"
	if ui_free_release_receipt "$work/rejected.json"; then
		echo 'UI-free release accepted incomplete or incorrectly bound verification' >&2
		exit 1
	fi
done
cat "$work/verified.json" "$work/verified.json" >"$work/rejected.json"
if ui_free_release_receipt "$work/rejected.json"; then
	echo 'UI-free release accepted ambiguous receipts' >&2
	exit 1
fi
echo 'UI-free release receipt rejects incomplete, ambiguous and incorrectly bound artifacts'

# Exercise each real historical consumer, rejecting corrupt receipts before installation or image use.
cat >"$work/historical-check.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
repo_root=$PUBLICATION_TEST_ROOT
test_dir=$PUBLICATION_TEST_WORK/historical-$1
mkdir -p "$test_dir/baseline" "$test_dir/publication-release"
printf '{}\n' >"$test_dir/values.yaml"
cp "$PUBLICATION_TEST_WORK/historical-receipt.json" "$test_dir/baseline/release.json"
cp "$PUBLICATION_TEST_WORK/historical-receipt.json" "$test_dir/publication-release/release.json"
cluster_context=owned-fixture
product_digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
candidate_chart=$test_dir/candidate.tgz
integration_deadline=$((SECONDS + 60))
source "$repo_root/tests/source/chart-publication.sh"
source "$repo_root/tests/source/helm-lifecycle.sh"
source "$repo_root/tests/source/installed-matrix.sh"
# The verified receipt is supplied at the external verifier boundary; consumption remains real.
bash() {
 [[ $1 == "$repo_root/scripts/verify-release-artifacts.sh" &&
    " $* " == *' --profile data-product-controller '* ]] || exit 99
}
helm_scoped() { touch "$PUBLICATION_TEST_WORK/receipt-used"; exit 77; }
helm() { touch "$PUBLICATION_TEST_WORK/receipt-used"; exit 77; }
publication_helm() {
 if [[ $1 == list ]]; then printf '[]\n'; return; fi
 touch "$PUBLICATION_TEST_WORK/receipt-used"
 exit 77
}
publication_kube() {
 if [[ $1 == get && $2 == dataproduct ]]; then return; fi
 if [[ $1 == get && $2 == crd/dataproducts.data.devantler.tech ]]; then
  printf '{"metadata":{"uid":"crd-1"},"spec":{}}\n'
  return
 fi
 exit 99
}
case "$1" in
v1) helm_release_lifecycle ;;
v4) chart_publication_lifecycle ;;
matrix) installed_lifecycle_matrix source replicas contracts ;;
matrix-source | matrix-replicas | matrix-contracts) installed_lifecycle_matrix "${1#matrix-}" ;;
matrix-none) installed_lifecycle_matrix ;;
matrix-unknown) installed_lifecycle_matrix source sql ;;
*) exit 99 ;;
esac
EOF
for consumer in v1 v4 matrix; do
	if [[ $consumer == v4 ]]; then
		tag=v4.0.0
		sha=4fbc7d215b3ce30091e74e9909a3ad822e878a05
		publisher=ea4df9004d4ab43b6c9af5b0c5f55712f7211328
		image=sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862
		chart=sha256:338521dc3e85774d6fff817a1af073ed1a462ee67e61459bf37f15e9a370ec89
	else
		tag=v1.12.0
		sha=ff7ddcf264f8ce4813ccdb6675a45b4d1e6d1b78
		publisher=df7fd4f83edade31c121a9de563d9a7b9b1f900d
		image=sha256:62350d68a853708357f463b74ea9ff847d94100d042125b4f2d0c6f25da54886
		chart=sha256:9d57e4c7b26664a560a2b72cb8f89dfee4c1466223081a039579cc9ffa3d8575
	fi
	jq -n --arg tag "$tag" --arg sha "$sha" --arg publisher "$publisher" --arg image "$image" --arg chart "$chart" \
		'{complete:true,tag:$tag,platform:"linux/amd64",releaseProfile:"data-product-controller",
      sourceSHA:$sha,publisherSHA:$publisher,imageDigest:$image,chartDigest:$chart,runtimeDigest:$image,
      imageReference:("ghcr.io/devantler-tech/data-product-controller@"+$image),
      chartReference:("ghcr.io/devantler-tech/charts/data-product-controller@"+$chart)}' >"$work/historical-good.json"
	for mutation in '.' 'del(.releaseProfile)' '.releaseProfile="khoist"' \
		'del(.imageReference)' '.imageReference="ghcr.io/devantler-tech/khoist@"+.imageDigest' \
		'del(.chartReference)' '.chartReference="ghcr.io/devantler-tech/charts/khoist@"+.chartDigest' '.complete=false'; do
		jq "$mutation" "$work/historical-good.json" >"$work/historical-receipt.json"
		rm -f "$work/receipt-used"
		result=0
		"$BASH" "$work/historical-check.sh" "$consumer" >"$work/historical.log" 2>&1 || result=$?
		if [[ $mutation == . ]]; then
			[[ $result == 77 && -f $work/receipt-used ]] || {
				echo "$consumer rejected a complete historical receipt (exit $result)" >&2
				cat "$work/historical.log" >&2
				exit 1
			}
		else
			[[ $result != 0 && $result != 77 && $result != 99 && ! -e $work/receipt-used ]] || {
				echo "$consumer used a corrupt historical receipt: $mutation" >&2
				exit 1
			}
		fi
	done
done
echo 'historical lifecycle consumers reject missing or mixed profiles before installation or image use'

# Each CI family consumes the same verified baseline; an empty or unknown selection fails before any release use.
cp "$work/historical-good.json" "$work/historical-receipt.json"
for consumer in matrix-source matrix-replicas matrix-contracts matrix-none matrix-unknown; do
	rm -f "$work/receipt-used"
	result=0
	"$BASH" "$work/historical-check.sh" "$consumer" >"$work/historical.log" 2>&1 || result=$?
	case $consumer in
	matrix-none | matrix-unknown)
		if [[ $result == 0 || $result == 77 || $result == 99 || -e $work/receipt-used ]] ||
			! grep -Eq 'select at least one installed matrix family|unsupported installed matrix family: sql' "$work/historical.log"; then
			echo "$consumer accepted an invalid family selection (exit $result)" >&2
			cat "$work/historical.log" >&2
			exit 1
		fi
		;;
	*)
		[[ $result == 77 && -f $work/receipt-used ]] || {
			echo "$consumer rejected a complete historical receipt (exit $result)" >&2
			cat "$work/historical.log" >&2
			exit 1
		}
		;;
	esac
done
echo 'installed matrix families each consume the verified baseline and reject empty or unknown selections'

# Render real chart products; fault-inject only Helm/Kubernetes reads around the actual snapshot guard.
"$real_helm" template dpc-publication "$root/charts/data-product-controller" --namespace products \
	--set route.enabled=true --set route.host=products.example.test --set demoProduct.ksailReleases.enabled=true \
	--set uiContract.enabled=true --set uiAppearance.enabled=true |
	"$BASH" "$root/tests/source/publication-products.sh" >"$work/snapshot-original.yaml"
yq ea -o=json -I=0 '.' "$work/snapshot-original.yaml" | jq -s '{items:map(
  .metadata.uid=("fixture-"+.metadata.name) |
  .metadata.annotations["meta.helm.sh/release-name"]="dpc-publication" |
  .metadata.annotations["meta.helm.sh/release-namespace"]="products" |
  .metadata.labels["app.kubernetes.io/managed-by"]="Helm")}' >"$work/live-original.json"
printf '%s\n' '{"metadata":{"uid":"crd-1"},"spec":{"group":"data.devantler.tech"}}' >"$work/crd-original.json"
jq -S '{uid:.metadata.uid,spec:.spec}' "$work/crd-original.json" >"$work/publication-original-crd.json"
cat >"$work/snapshot-check.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
repo_root=$PUBLICATION_TEST_ROOT
test_dir=$PUBLICATION_TEST_WORK
source "$repo_root/tests/source/chart-publication.sh"
publication_helm() {
 [[ "$*" == 'get manifest dpc-publication' ]] || return 99
 cat "$test_dir/snapshot.yaml"
}
publication_kube() {
 case "$*" in
 'get dataproduct harbour-observations ksail-releases -o json')
   cat "$test_dir/live.json"
   [[ ${SNAPSHOT_READ:-complete} != partial ]] ;;
 'get crd/dataproducts.data.devantler.tech -o json') cat "$test_dir/crd.json" ;;
 *) return 99 ;;
 esac
}
publication_snapshot "$1" false
EOF
cp "$work/snapshot-original.yaml" "$work/snapshot.yaml"
cp "$work/live-original.json" "$work/live.json"
cp "$work/crd-original.json" "$work/crd.json"
"$BASH" "$work/snapshot-check.sh" baseline
"$BASH" "$work/snapshot-check.sh" ui-free-rollback
for fault in stored-ui live-ui changed-output recreated-product changed-owner changed-crd missing-product partial; do
	cp "$work/snapshot-original.yaml" "$work/snapshot.yaml"
	cp "$work/live-original.json" "$work/live.json"
	cp "$work/crd-original.json" "$work/crd.json"
	case "$fault" in
	stored-ui) yq '(.spec.ui)={"url":"https://unapproved.example/"}' "$work/snapshot-original.yaml" >"$work/snapshot.yaml" ;;
	live-ui) jq '.items[0].spec.ui={"url":"https://unapproved.example/"}' "$work/live-original.json" >"$work/live.json" ;;
	changed-output) jq '.items[0].spec.outputs[0].url="https://changed.example/"' "$work/live-original.json" >"$work/live.json" ;;
	recreated-product) jq '.items[0].metadata.uid="replacement"' "$work/live-original.json" >"$work/live.json" ;;
	changed-owner) jq '.items[0].metadata.annotations["meta.helm.sh/release-name"]="other"' "$work/live-original.json" >"$work/live.json" ;;
	changed-crd) jq '.metadata.uid="replacement"' "$work/crd-original.json" >"$work/crd.json" ;;
	missing-product) jq '.items|=.[0:1]' "$work/live-original.json" >"$work/live.json" ;;
	partial) ;;
	esac
	result=0
	SNAPSHOT_READ=$fault "$BASH" "$work/snapshot-check.sh" ui-free-rollback >"$work/snapshot.log" 2>&1 || result=$?
	[[ $result != 0 ]] || {
		echo "UI-free stored rollback accepted drift: $fault" >&2
		exit 1
	}
done
echo 'UI-free stored/live snapshot controls reject legacy UI, changed outputs, replacement UIDs, ownership drift and incomplete reads'
