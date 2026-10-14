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
jq -n --arg digest "$digest" '{metadata:{status:"deployed",revision:7,chart:"khoist",version:"1.2.3",appVersion:"1.2.3"},values:{image:{repository:"ghcr.io/devantler-tech/khoist",digest:$digest}}}' >"$work/good.json"
cp "$work/good.json" "$HELM_TEST_RELEASE"
helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/khoist@$digest"
for mutation in '.metadata.status="pending-upgrade"' '.metadata.version="1.2.4"' '.metadata.appVersion="1.2.4"' '.values.image.digest="latest"' '.values.image.repository="unapproved.example/controller"' '.metadata.chart="other"' '.metadata.revision=0'; do
	jq "$mutation" "$work/good.json" >"$HELM_TEST_RELEASE"
	if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/khoist@$digest"; then
		echo 'installed release identity accepted drift' >&2
		exit 1
	fi
done
cp "$work/good.json" "$HELM_TEST_RELEASE"
export CHANGE_REVISION=true
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/khoist@$digest"; then
	echo 'installed identity accepted a release changed during readback' >&2
	exit 1
fi
unset CHANGE_REVISION
printf '{}\n' >"$HELM_TEST_RELEASE"
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/khoist@$digest"; then
	echo 'installed release identity accepted incomplete response' >&2
	exit 1
fi
# Upgrade and recovery compare each release's real chart name explicitly.
jq '.metadata.chart="data-product-controller" | .values.image.repository="ghcr.io/devantler-tech/data-product-controller"' "$work/good.json" >"$HELM_TEST_RELEASE"
helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest" data-product-controller
if helm_installed_identity 1.2.3 "ghcr.io/devantler-tech/data-product-controller@$digest"; then
	echo 'canonical candidate accepted the legacy baseline chart name' >&2
	exit 1
fi
echo 'installed Helm identity behavior tests passed'

# Exercise the actual serving-replica proof without a cluster. Historical pages
# are accepted only when the caller explicitly selects the legacy profile.
awk '/^registry_replicas_ready\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' \
	"$root/tests/source/run.sh" >"$work/registry-proof.sh"
# shellcheck source=tests/source/run.sh
source "$work/registry-proof.sh"
kube() {
	case "$1 $2" in
	'get deployment/dpc') printf '%s\n' '{"metadata":{"uid":"deployment","generation":1,"annotations":{"deployment.kubernetes.io/revision":"2"}},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":2,"readyReplicas":2,"availableReplicas":2}}' ;;
	'get replicasets') printf '%s\n' '{"items":[{"metadata":{"uid":"rs","annotations":{"deployment.kubernetes.io/revision":"2"},"ownerReferences":[{"uid":"deployment","controller":true}]}}]}' ;;
	'get pods') jq -n '{items:["192.0.2.1","192.0.2.2"] | map({metadata:{ownerReferences:[{uid:"rs",controller:true}]},status:{conditions:[{type:"Ready",status:"True"}],containerStatuses:[{name:"controller",ready:true}],podIP:.}})}' ;;
	*) return 99 ;;
	esac
}
probe() {
	[[ $1 == --url && $3 == --contains ]] || return 99
	if [[ $2 == */api/* ]]; then return 0; fi
	[[ $page_title == "$4" ]]
}
page_title='<title>KHoist · Data products</title>'
registry_replicas_ready
if registry_replicas_ready legacy; then
	echo 'legacy proof accepted a canonical page' >&2
	exit 1
fi
page_title='<title>Data products</title>'
registry_replicas_ready legacy
if registry_replicas_ready; then
	echo 'canonical proof accepted a legacy page' >&2
	exit 1
fi
if registry_replicas_ready unknown; then
	echo 'unknown release profile was admitted' >&2
	exit 1
fi
echo 'serving registry identity behavior tests passed'
