---
title: Compose products
description: Declare named producer outputs and inspect dependency readiness.
---

A product can declare inputs from other products in the same namespace.
KHoist observes that dependency graph. Your independently operated workload performs
the actual queries and transformations.

## Declare an input

An input chooses a product and one of its named outputs:

```yaml
inputs:
  - name: observations
    productRef:
      name: harbour
      namespace: products
      output: query
    contract:
      protocol: OpenAPI
      minimumVersion: v1.1.0
```

The namespace defaults to the consumer's namespace. Cross-namespace composition is
rejected; discovering a product does not authorise a cross-namespace dependency.

## Validate the graph

From a source checkout:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file docs/examples/composition.yaml
```

This checks a three-product example and reports the required `composition` feature.
For a deployed graph with declared contract requirements, the operator must explicitly
enable `composition.enabled`. A successful offline check does not enable it.

## Read the result

Inspect the consumer's `CompositionReady` condition, aggregate `Ready` condition
and observed lineage. Missing products or outputs, incompatible declared contracts,
cycles and unavailable producers prevent readiness.

Version checks use publisher declarations: a minimum stable version within major 1
accepts compatible later stable major-1 versions. Major-zero versions require an exact
match. KHoist does not fetch schemas or prove that responses have compatible semantics.

The [composition reference](https://github.com/devantler-tech/khoist/blob/main/docs/composition.md)
contains the complete example, failure reasons and graph bounds.
