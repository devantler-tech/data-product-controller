#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export HELM_TEST_RELEASE="$work/release.json"
mkdir "$work/bin"
cat >"$work/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -eu
while [[ $1 == --* ]]; do shift 2; done
case "$1 $2 $3" in
'get metadata dpc') jq '.metadata' "$HELM_TEST_RELEASE" ;;
'get values dpc')
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
source "$root/tests/source/helm-lifecycle.sh"
digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
jq -n --arg digest "$digest" '{metadata:{status:"deployed",revision:7,chart:"data-product-controller",version:"1.2.3",appVersion:"1.2.3"},values:{image:{repository:"ghcr.io/devantler-tech/data-product-controller",digest:$digest}}}' >"$work/good.json"
cp "$work/good.json" "$HELM_TEST_RELEASE"
helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest"
for mutation in '.metadata.status="pending-upgrade"' '.metadata.version="1.2.4"' '.metadata.appVersion="1.2.4"' '.values.image.digest="latest"' '.values.image.repository="unapproved.example/controller"' '.metadata.chart="other"' '.metadata.revision=0'; do
	jq "$mutation" "$work/good.json" >"$HELM_TEST_RELEASE"
	if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest"; then
		echo 'installed release identity accepted drift' >&2
		exit 1
	fi
done
cp "$work/good.json" "$HELM_TEST_RELEASE"
export CHANGE_REVISION=true
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest"; then
	echo 'installed identity accepted a release changed during readback' >&2
	exit 1
fi
unset CHANGE_REVISION
printf '{}\n' >"$HELM_TEST_RELEASE"
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest"; then
	echo 'installed release identity accepted incomplete response' >&2
	exit 1
fi
echo 'installed Helm identity behavior tests passed'

# The real observation must request the page shipped by each installed release.
# Other rollout and registry checks retain their independent behavior controls.
product_digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
candidate_chart=$work/candidate.tgz
source "$root/tests/source/installed-matrix.sh"
bash() { [[ $1 == "$repo_root/scripts/observe-rollout.sh" ]]; }
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
for observation in 'candidate:KHoist' 'rollback:Data products' 'restored-candidate:KHoist'; do
	phase=${observation%%:*}
	wanted_registry_title=${observation#*:}
	matrix_observation "$phase" candidate-image "$product_digest"
done
echo 'installed matrix observation selects each released registry page'

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
 [[ $1 == "$repo_root/scripts/verify-release-artifacts.sh" ]] || return 99
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
