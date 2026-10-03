#!/usr/bin/env bash
# Sourced only inside the owned ephemeral acceptance cluster after the combined flag rollout.
: "${repo_root:?run through tests/source/run.sh}"

addresses=$(kube get endpointslices -l kubernetes.io/service-name=dpc -o json |
	jq -er '[.items[].endpoints[] | select(.conditions.ready == true) | .addresses[]] | unique |
 if length == 2 then .[] else error("expected two ready registry endpoints") end')
first_address=$(printf '%s\n' "$addresses" | sed -n '1p')
second_address=$(printf '%s\n' "$addresses" | sed -n '2p')
kube exec consumer -- /fixture discovery \
	--url "http://$first_address:8082/api/v2/products" \
	--replica-url "http://$second_address:8082/api/v2/products" \
	--namespace products --expected coastal-summary,harbour,weather
probe --url http://dpc/api/v2/products/products/coastal-summary --contains '"apiVersion":"data-product-descriptor/v1"'
probe --url http://dpc/api/v2/products/products/missing --want-status 404
probe --url 'http://dpc/api/v2/products?namespace=products&limit=101' --want-status 400
probe --url 'http://dpc/api/v2/products?namespace=invalid_namespace&limit=1' --want-status 400
probe --url http://dpc/api/v2/schema --contains 'data-product-descriptor/v1'
echo 'PASS: Kubernetes discovery pages cross registry replicas; exact lookup, scope and bounds are enforced'
