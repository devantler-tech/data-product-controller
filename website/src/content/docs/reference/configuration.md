---
title: Configuration
description: Locate Helm settings, process flags and opt-in release capabilities.
---

The [chart values](https://github.com/devantler-tech/data-product-controller/blob/main/charts/data-product-controller/values.yaml)
are the complete Helm reference. Inspect the exact version you will install:

```bash
helm show values oci://ghcr.io/devantler-tech/charts/data-product-controller \
  --version '<version>'
```

## Installation controls

| Helm value                                       | Purpose                                    |
| ------------------------------------------------ | ------------------------------------------ |
| `image.repository`, `image.tag`, `image.digest`  | Workload identity; digest takes precedence |
| `controller.replicas`, `controller.resources`    | Controller capacity                        |
| `route.enabled`, `route.host`, `route.parentRef` | Optional Gateway API route                 |
| `demoProduct.enabled`                            | Bundled Harbour example workload           |
| `demoProduct.ksailReleases.enabled`              | Optional KSail release product declaration |

## Optional observation and publication

These `enabled` switches default to `false`. Enable only the capabilities you have configured
and evaluated:

| Helm value                   | Capability                                                                                    |
| ---------------------------- | --------------------------------------------------------------------------------------------- |
| `provisionedSources.enabled` | Externally owned source observation                                                           |
| `engineProviders.enabled`    | Supported engine/provider dispatch; also needs source observation                             |
| `connectorReadiness.enabled` | Independent connector Deployment observation                                                  |
| `contractReadiness.enabled`  | Explicitly bound independent contract probes                                                  |
| `composition.enabled`        | Declared contract compatibility and lineage                                                   |
| `registryDiscovery.enabled`  | Versioned descriptor discovery                                                                |
| `registryLineage.enabled`    | Dependency traces; also needs discovery and the controller's declared-compatibility evaluator |
| `dcatCatalog.enabled`        | Explicitly identified DCAT publication                                                        |

`dcatCatalog.id` defaults to an empty string. When enabling the catalogue, supply a
stable HTTPS URL or URN that identifies it.

The optional `httpSource` and `contractProbe` workloads have separate settings,
resource limits and access prerequisites. Read their repository guides before enabling
them; setting one boolean does not supply credentials, network policy or effective access.

For source development, run a command with `--help` to inspect its process flags.
Offline preflight additionally requires `PUBLISHER_PREFLIGHT_ENABLED=true`;
offline DSP export requires its own explicit feature gate and public provider bindings.
