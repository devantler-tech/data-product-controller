#!/usr/bin/env bash
# Sourced by run.sh inside its owned ephemeral cluster and cleanup boundary.
: "${repo_root:?run through tests/source/run.sh}"

probe --url http://dpc/api/v1/catalog --want-status 404
kube apply -f "$repo_root/docs/examples/dcat-product.yaml"
install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true \
	--set dcatCatalog.enabled=true --set-string dcatCatalog.id=urn:example:integration-catalog
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
wait_for 'enabled chart publishes a DCAT catalog' 60 probe --url http://dpc/api/v1/catalog --contains '"@id":"urn:example:integration-catalog"'
probe --url http://dpc/api/v1/catalog --contains '"dcat:servesDataset":{"@id":"urn:example:catalog-harbour"}'
probe --url http://dpc/api/v1/catalog --contains '"dcat:endpointDescription":{"@id":"https://example.com/harbour/openapi.json"}'

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

install_chart --set httpSource.enabled=true --set connectorReadiness.enabled=true
kube --request-timeout=0 rollout status deployment/dpc --timeout=180s
probe --url http://dpc/api/v1/catalog --want-status 404
kube delete -f "$repo_root/docs/examples/dcat-product.yaml"
echo 'PASS: DCAT chart flag, publisher opt-in, duplicate rejection and rollback'
