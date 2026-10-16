---
title: Troubleshooting
description: Diagnose discovery, readiness, installation and API failures.
---

Start with the failing operation. A healthy registry page, ready controller pod or
valid declaration answers a different question from a successful product query.

## A product is missing or not ready

Inspect the intended namespace and object:

```bash
kubectl --namespace products get dataproduct harbour-observations -o yaml
kubectl --namespace products describe dataproduct harbour-observations
```

Check the Kubernetes name, conditions, reasons and observed generation.
Use the controller's current-generation observation; an old Ready condition may be stale.

| Observation                      | Next action                                                                |
|----------------------------------|----------------------------------------------------------------------------|
| Missing product or output        | Correct the reference or publish the named producer/output                 |
| `CrossNamespaceDependencyDenied` | Keep producer and consumer in the same namespace                           |
| `CompositionFeatureDisabled`     | Ask the operator to configure composition before requiring contract checks |
| `ContractIncompatible`           | Check the declared protocol and product version                            |
| Source or publication not ready  | Inspect the independently owned source and publication metadata            |
| Connector or contract not ready  | Inspect the exact observer/probe Deployment and its current generation     |

Never copy Secret values into an issue or product resource.

## The registry works, but an API request fails

Call the approved product endpoint from the actual consumer's environment.
Check its contract, authentication, network policy and availability with the product owner.
The registry does not proxy that request or supply credentials.
The local quickstart uses HTTP; the independent published Harbour consumer requires HTTPS.

## Discovery returns unavailable

Check whether the operator enabled `registryDiscovery.enabled`.
Dependency traces additionally need `registryLineage.enabled`.
Do not convert a disabled or failed response into an empty successful result.

## An upgrade rejects a field

Confirm the installed CRD matches the selected release:

```bash
kubectl get crd dataproducts.data.devantler.tech
kubectl explain dataproduct.spec --api-version=data.devantler.tech/v1alpha1
```

Helm does not upgrade existing CRDs from `crds/`. Follow the
[upgrade procedure](/guides/upgrade/) and review schema compatibility.
Do not delete the CRD to fix a field error.

## Report a reproducible problem

Open an [issue](https://github.com/devantler-tech/data-product-controller/issues/new)
with the operation, expected result, actual result and release/chart identity.
Include a minimal declaration with public example URLs and redact credentials,
personal data and private infrastructure details.
