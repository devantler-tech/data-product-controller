#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
fail() {
	printf '%s\n' "HTTP source chart test failed: $1" >&2
	exit 1
}

render() {
	helm template source-test "$chart" --namespace products \
		--set httpSource.enabled=true \
		--set httpSource.secretName=existing-export \
		--set httpSource.sourceCIDR=192.0.2.10/32 \
		--set httpSource.consumerPodLabels.app=trusted-consumer \
		--set image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
		"$@"
}

defaults=$(helm template source-test "$chart" --namespace products)
count=$(printf '%s' "$defaults" | yq ea '[select(.metadata.labels."app.kubernetes.io/component" == "http-source")] | length' -)
[ "$count" = 0 ] || fail 'source workload must be absent by default'

enabled=$(render)
deployment=$(printf '%s' "$enabled" | yq ea 'select(.kind == "Deployment" and .metadata.labels."app.kubernetes.io/component" == "http-source")' -)
[ -n "$deployment" ] || fail 'enabled source has no Deployment'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].command[0]' -)" = '/http-source' ] || fail 'source must execute its own binary'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].env[] | select(.name == "HTTP_SOURCE_ENABLED") | .value' -)" = true ] || fail 'enabled workload must opt into the release flag'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.automountServiceAccountToken' -)" = false ] || fail 'source must not receive an API token'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].volumeMounts[0].readOnly' -)" = true ] || fail 'Secret must mount read-only'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].volumeMounts[0].subPath // ""' -)" = '' ] || fail 'Secret rotation requires a directory mount without subPath'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.volumes[0].secret.secretName' -)" = existing-export ] || fail 'source must use the existing Secret'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].readinessProbe.timeoutSeconds' -)" -gt 5 ] || fail 'readiness timeout must exceed the source timeout'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].readinessProbe.httpGet.path' -)" = /readyz ] || fail 'readiness must check source access'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].livenessProbe.httpGet.path' -)" = /healthz ] || fail 'liveness must not depend on source availability'
[ "$(printf '%s' "$deployment" | yq '.spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem' -)" = true ] || fail 'source must have a read-only root filesystem'

policy=$(printf '%s' "$enabled" | yq ea 'select(.kind == "NetworkPolicy" and .metadata.labels."app.kubernetes.io/component" == "http-source")' -)
[ "$(printf '%s' "$policy" | yq '.policyTypes // .spec.policyTypes | sort | join(",")' -)" = Egress,Ingress ] || fail 'source needs ingress and egress isolation'
[ "$(printf '%s' "$policy" | yq '.spec.ingress[0].from[0].podSelector.matchLabels.app' -)" = trusted-consumer ] || fail 'source must restrict consumers by explicit pod labels'
[ "$(printf '%s' "$policy" | yq '.spec.ingress[0].from[0].namespaceSelector // "absent"' -)" = absent ] || fail 'consumer pod selector must stay in the release namespace'
[ "$(printf '%s' "$policy" | yq '.spec.egress[0].to[0].ipBlock.cidr' -)" = 192.0.2.10/32 ] || fail 'source egress must use the declared address'
[ "$(printf '%s' "$policy" | yq '.spec.egress[0].ports[0].port' -)" = 443 ] || fail 'source egress must restrict TLS port'
[ "$(printf '%s' "$policy" | yq '.spec.ingress | length' -)" = 1 ] || fail 'management access must be absent by default'
dangerous=$(printf '%s' "$enabled" | yq ea '[select(.metadata.labels."app.kubernetes.io/component" == "http-source" and (.kind == "DataProduct" or .kind == "HTTPRoute" or .kind == "Role" or .kind == "ClusterRole" or .kind == "RoleBinding" or .kind == "ClusterRoleBinding" or .kind == "Secret"))] | length' -)
[ "$dangerous" = 0 ] || fail 'source must not create credentials, API authority, product registration, or a public route'

data_service=$(printf '%s' "$enabled" | yq ea 'select(.kind == "Service" and .metadata.name == "source-test-http-source")' -)
metrics_service=$(printf '%s' "$enabled" | yq ea 'select(.kind == "Service" and .metadata.name == "source-test-http-source-metrics")' -)
[ "$(printf '%s' "$data_service" | yq '.spec.ports | length' -)" = 1 ] || fail 'data Service must not carry management traffic'
[ "$(printf '%s' "$data_service" | yq '.spec.publishNotReadyAddresses // false' -)" = false ] || fail 'data routing must remain readiness-gated'
[ "$(printf '%s' "$metrics_service" | yq '.spec.publishNotReadyAddresses' -)" = true ] || fail 'management must stay observable while the source is unready'
[ "$(printf '%s' "$metrics_service" | yq '.spec.ports | length' -)" = 1 ] || fail 'management Service must carry one port'
[ "$(printf '%s' "$metrics_service" | yq '.spec.ports[0].targetPort' -)" = management ] || fail 'management must not bypass data readiness'

for invalid in httpSource.secretName= httpSource.sourceCIDR= httpSource.sourceCIDR=0.0.0.0/0 httpSource.sourceCIDR=192.0.2.10/24 image.digest=; do
	if render --set "$invalid" >/dev/null 2>&1; then fail "accepted unsafe setting $invalid"; fi
done
if render --set httpSource.consumerPodLabels.app=null >/dev/null 2>&1; then fail 'accepted empty consumer selector'; fi
if render --namespace default >/dev/null 2>&1; then fail 'accepted the default namespace'; fi
if helm template source-test "$chart" --namespace products --set httpSource.enabled=true >/dev/null 2>&1; then fail 'enabled without connection and ingress configuration'; fi

printf '%s\n' 'HTTP source chart behavior tests passed'
