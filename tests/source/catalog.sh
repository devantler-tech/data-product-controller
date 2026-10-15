#!/usr/bin/env bash
# Sourced by run.sh inside its owned ephemeral cluster and cleanup boundary.
: "${repo_root:?run through tests/source/run.sh}"
: "${test_dir:?}" "${cluster_context:?}" "${integration_deadline:?}"

catalog_edc_products_absent() {
	local products
	products=$(kube get dataproduct edc-harbour edc-buoy --ignore-not-found -o name) || return 1
	[[ -z $products ]] || {
		echo 'EDC catalog fixture products already exist or remain installed' >&2
		return 1
	}
}

probe --url http://dpc/api/v1/catalog --want-status 404
kube apply -f "$repo_root/docs/examples/dcat-product.yaml"
install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true \
	--set dcatCatalog.enabled=true --set-string dcatCatalog.id=urn:example:integration-catalog
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
wait_for 'enabled chart publishes a DCAT catalog' 60 probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:integration-catalog"'
probe --url http://dpc/api/v1/catalog --contains '"dcat:servesDataset":{"@id":"urn:example:catalog-harbour"}'
probe --url http://dpc/api/v1/catalog --contains '"dcat:endpointDescription":{"@id":"https://example.com/harbour/openapi.json"}'

# Capture the actual controller response; the independent consumer never reads Kubernetes itself.
catalog_edc_products_absent
kube apply -f "$repo_root/tests/dataspace/edc/products.yaml"
wait_for 'EDC fixture reaches the enabled controller catalog' 30 \
	probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:edc-buoy"'
kubectl --kubeconfig "$KUBECONFIG" --context "$cluster_context" --request-timeout=15s \
	get --raw /api/v1/namespaces/products/services/dpc:http/proxy/api/v1/catalog \
	>"$test_dir/edc-controller-catalog.json"
bash "$repo_root/tests/source/edc-import.sh" "$test_dir/edc-controller-catalog.json" \
	"$((integration_deadline - SECONDS))"
kube delete -f "$repo_root/tests/dataspace/edc/products.yaml" --wait=true --timeout=20s
catalog_edc_products_absent

# Publication is a publisher assertion, independent of product readiness.
kube annotate dataproduct catalog-harbour data.devantler.tech/dcat-type-
probe --url http://dpc/api/v1/catalog --contains '"dcat:dataset":[]'
kube annotate dataproduct catalog-harbour data.devantler.tech/dcat-type=Invalid
probe --url http://dpc/api/v1/catalog --want-status 422
kube annotate dataproduct catalog-harbour data.devantler.tech/dcat-type=Dataset --overwrite
kube get dataproduct catalog-harbour -o json | jq 'del(.metadata, .status) |
  .metadata={name:"duplicate-catalog-id",namespace:"products",annotations:{"data.devantler.tech/dcat-type":"Dataset"}}' | kube apply -f -
probe --url http://dpc/api/v1/catalog --want-status 422
kube delete dataproduct duplicate-catalog-id
probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:catalog-harbour"'

# More than one API page must produce a complete catalog with the real Kubernetes reader.
kube get dataproduct catalog-harbour -o json | jq '. as $product |
  {apiVersion:"v1",kind:"List",items:[range(0;17) as $index |
    $product | del(.metadata,.status) |
    .metadata={name:("catalog-page-" + ("0" + ($index|tostring))[-2:]),namespace:"products",
      labels:{"catalog-pagination-test":"true"},annotations:{"data.devantler.tech/dcat-type":"Dataset"}} |
    .spec.id=("urn:example:pagination:" + ($index|tostring))]}' | kube apply -f -
probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:pagination:0"'
probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:pagination:16"'
kube delete dataproduct -l catalog-pagination-test=true

install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
wait_for 'disabled catalog returns 404 after rollback' 60 probe --url http://dpc/api/v1/catalog --want-status 404
kube delete -f "$repo_root/docs/examples/dcat-product.yaml"
echo 'PASS: DCAT chart flag, publisher opt-in, duplicate rejection and rollback'
