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
