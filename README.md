# data-product-controller

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/devantler-tech/data-product-controller.svg)](https://pkg.go.dev/github.com/devantler-tech/data-product-controller)

A cloud-native control plane for self-describing, composable data products.

`data.devantler.tech/v1alpha1` describes a data capability through ownership, inputs, outputs, standard machine-readable contracts, and an optional independently deployed user interface. The controller resolves product-to-product dependencies and reports readiness. A read-only registry and reference UI discover products from Kubernetes without compiling product-specific code into the catalogue.

## Foundation

The current foundation provides:

- a namespaced `DataProduct` CRD with guarded HTTPS interfaces and stable URI identity;
- composition through named output references, with dependency-aware readiness conditions;
- a portable JSON descriptor registry at `/api/v1/products`;
- a default-off `registry-ui` feature that renders product descriptors and embeds product UIs in a restricted sandbox;
- an independently deployed harbour-observations example with its own OpenAPI contract, query API, and UI;
- a Helm chart containing CRDs, least-privilege RBAC, hardened workloads, services, and optional Gateway API routing.

Provisioners, source connectors, richer composition semantics, and data-space exchange are roadmap work rather than capabilities claimed by this first release. See the [roadmap epic](https://github.com/devantler-tech/data-product-controller/issues/1).

## Data product contract

```yaml
apiVersion: data.devantler.tech/v1alpha1
kind: DataProduct
metadata:
  name: harbour-observations
spec:
  id: https://data-products.example.com/products/harbour
  name: Harbour observations
  description: Queryable temperature and salinity observations.
  version: v1.0.0
  owner:
    name: data-platform-team
  outputs:
    - name: observations
      protocol: OpenAPI
      url: https://data-products.example.com/products/harbour/api/observations
      contractUrl: https://data-products.example.com/products/harbour/openapi.json
      mediaType: application/json
  ui:
    title: Explore harbour observations
    url: https://data-products.example.com/products/harbour/ui
```

Another product composes this output without copying its data:

```yaml
spec:
  inputs:
    - name: harbour
      productRef:
        name: harbour-observations
        output: observations
```

The custom resource is control-plane metadata. Product data and credentials do not belong in the Kubernetes API. Credentials remain in Secrets consumed directly by provisioner or connector workloads.

## Decentralized UI contract

The `spec.ui.url` page belongs to the data product, not the registry. A compatible catalogue may render it in a sandboxed iframe or link to it directly. The reference registry:

- loads only absolute HTTPS URLs supplied by the product descriptor;
- uses `sandbox="allow-forms allow-scripts"` without `allow-same-origin`;
- passes no bearer token, Secret, or Kubernetes identity;
- never imports product JavaScript into the catalogue document.

This keeps each product portable across the reference registry and third-party catalogue implementations. Cross-window capabilities and authentication are deliberately deferred until they have a versioned, least-privilege protocol.

## Install

Install the chart with the registry UI explicitly enabled and an existing Gateway API listener:

```bash
helm upgrade --install data-product-controller \
  oci://ghcr.io/devantler-tech/charts/data-product-controller \
  --namespace data-product-system \
  --create-namespace \
  --set registryUI.enabled=true \
  --set route.enabled=true \
  --set route.host=data-products.example.com
```

The UI remains off when `registryUI.enabled` is omitted. For repository development:

```bash
go test ./...
go build ./...
sh scripts/chart.test.sh
helm lint charts/data-product-controller
```

Regenerate CRDs and RBAC after editing API markers:

```bash
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 \
  rbac:roleName=manager-role crd paths=./... \
  output:crd:artifacts:config=config/crd/bases \
  output:rbac:artifacts:config=config/rbac
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 \
  crd paths=./api/... \
  output:crd:artifacts:config=charts/data-product-controller/templates
```

## Design

[ADR 0001](docs/adr/0001-portable-data-product-control-plane.md) records why the Kubernetes resource stays a small control-plane profile and how products remain portable. The vocabulary is informed by the [Open Data Mesh Data Product Descriptor Specification](https://dpds.opendatamesh.org/), [W3C DCAT 3](https://www.w3.org/TR/vocab-dcat-3/), [OpenAPI](https://spec.openapis.org/oas/), [AsyncAPI](https://www.asyncapi.com/docs/reference/specification/v3.0.0), and the [Eclipse Dataspace Protocol](https://projects.eclipse.org/projects/technology.dataspace-protocol-base). This release does not claim full conformance with those standards.
