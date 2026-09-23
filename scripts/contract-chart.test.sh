#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() {
	echo "contract chart test failed: $1" >&2
	exit 1
}
helm template dpc "$chart" --namespace products >"$tmp/default.yaml"
flag=$(yq ea 'select(.kind == "Deployment" and .metadata.name == "dpc") | .spec.template.spec.containers[0].env[] | select(.name == "CONTRACT_READINESS_ENABLED") | .value' "$tmp/default.yaml")
[ "$flag" = false ] || fail 'contract observation must default off'
count=$(yq ea '[select(.metadata.labels."app.kubernetes.io/component" == "contract-probe")] | length' "$tmp/default.yaml")
[ "$count" = 0 ] || fail 'probe workload must be absent by default'
cat >"$tmp/values.yaml" <<'VALUES'
image:
  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
contractReadiness:
  enabled: true
contractProbe:
  enabled: true
  url: https://contracts.example.com/schema
  targetCIDR: 192.0.2.1/32
  monitorPodLabels:
    app: monitor
VALUES
helm template dpc "$chart" --namespace products -f "$tmp/values.yaml" >"$tmp/enabled.yaml"
yq ea -o=json '[.]' "$tmp/enabled.yaml" | jq -e '.[] | select(.kind == "Deployment" and .metadata.labels."app.kubernetes.io/component" == "contract-probe") | .spec.template.spec | (.automountServiceAccountToken == false) and (.containers[0].command[0] == "/contract-probe") and (.containers[0].readinessProbe.httpGet.path == "/readyz") and (.containers[0].securityContext.readOnlyRootFilesystem == true)' >/dev/null || fail 'probe workload boundary missing'
yq ea -o=json '[.]' "$tmp/enabled.yaml" | jq -e '.[] | select(.kind == "NetworkPolicy" and .metadata.labels."app.kubernetes.io/component" == "contract-probe") | (.spec.egress[0].to[0].ipBlock.cidr == "192.0.2.1/32") and (.spec.egress[0].ports[0].port == 443) and (.spec.ingress[0].from[0].podSelector.matchLabels.app == "monitor")' >/dev/null || fail 'explicit network boundaries missing'
for invalid in 'contractProbe.url=http://example.com/schema' 'contractProbe.url=https://user:secret@example.com/schema' 'contractProbe.url=https://example.com/schema?token=secret' 'contractProbe.url=https://example.com/$(CONTRACT_READINESS_ENABLED)' 'contractProbe.targetCIDR=0.0.0.0/0' 'image.digest=' 'contractProbe.monitorPodLabels=null'; do
	if helm template dpc "$chart" --namespace products -f "$tmp/values.yaml" --set "$invalid" >/dev/null 2>&1; then fail "accepted unsafe setting: $invalid"; fi
done
if helm template dpc "$chart" -f "$tmp/values.yaml" >/dev/null 2>&1; then fail 'probe accepted default namespace'; fi
grants=$(yq ea '[select(.kind == "Role" or .kind == "ClusterRole") | .rules[] | select(.resources[] | test("^(deployments|secrets|\\*)$"))] | length' "$tmp/enabled.yaml")
[ "$grants" = 0 ] || fail 'enablement must not widen controller reads'
echo 'contract chart behavior tests passed'
