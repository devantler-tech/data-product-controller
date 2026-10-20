---
title: Capabilities and maturity
description: Choose a supported path without confusing controlled acceptance with production adoption.
---

The API is `v1alpha1`. These docs describe main-branch behaviour; a release may
contain a smaller set. Check its chart values and notes before enabling a capability.

## Available foundation

KHoist provides DataProduct metadata, named standard interfaces, dependency-aware
readiness and an API-only registry workspace. Applications query products directly.
The [quickstart](/start/quickstart/) uses synthetic data and no external account.

## Optional capabilities

| Capability                                 | State and boundary                                                                      |
|--------------------------------------------|-----------------------------------------------------------------------------------------|
| Source and engine observation              | Default-off; observes externally owned resources and publication metadata               |
| Connector and contract readiness           | Default-off; uses independently operated workloads and probes                           |
| Declared composition and lineage           | Default-off; checks declared contracts and observations, never executes transformations |
| Descriptor discovery and dependency traces | Default-off; publishes bounded public metadata, not data access                         |
| DCAT catalogue publication                 | Default-off; requires explicit dataset annotation and catalogue identity                |
| DSP catalogue export                       | Default-off offline command; no negotiation, authorisation enforcement or transfer      |
| Publisher preflight                        | Default-off local command and report viewer; validates declarations only                |

Use [configuration](/reference/configuration/) to locate the exact controls.
Turning on a flag is a deployment decision, not proof of adoption.

## Provider profiles

The observer supports SQL/native through CloudNativePG, Document/native through Percona
MongoDB, Graph/native through ArangoDB/AQL, and Document/Graph hybrid profiles through
PostgreSQL JSONB and Apache AGE.

Real disposable acceptance covers these supported profiles. Production rollout,
operational fit and release-gate retirement remain separate acceptance work.
In particular, an ArangoDB edition marker does not establish a deployment entitlement.
Read the [engine-provider contract](https://github.com/devantler-tech/khoist/blob/main/docs/engine-providers.md)
before selecting a source, operator, publication or image.

## Interoperability limits

DCAT and DSP publication are bounded metadata profiles. The real Eclipse EDC import
checkpoint reports loss of typed distribution identities. It does not establish full
provider interoperability, contract negotiation, authenticated transfer or usage control.

The [roadmap](https://github.com/devantler-tech/khoist/issues/1)
tracks adoption and the accepted API-only direction. Retained legacy UI metadata and
runtime are deprecated compatibility; new integrations use named APIs.
