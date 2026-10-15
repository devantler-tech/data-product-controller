---
title: Architecture and ownership
description: Understand the metadata boundary and the responsibilities of publishers, consumers and operators.
---

KHoist is a control plane for data-product descriptions. Its Kubernetes resource is
`DataProduct`, in API group `data.devantler.tech`. The controller observes readiness
and dependencies; the registry presents public metadata.

## Who owns what?

| Responsibility                                              | Owner                            |
|-------------------------------------------------------------|----------------------------------|
| Product identity, owner, named outputs and contracts        | Publisher                        |
| Records, query execution, authentication and credentials    | Product service and its operator |
| Database creation, backups, upgrades, rotation and deletion | Independent database operator    |
| Metadata readiness and dependency observations              | KHoist controller                |
| Catalogue access policy and workload deployment             | Platform operator                |
| Data access, UI and rendering                               | Consumer application             |

The controller does not store records or credentials in custom resources.
Supported source observers read explicit resource and Secret metadata; they do not
query a database or read its connection values.

## How a consumer uses a product

1. The publisher describes a running API and its standard contract.
2. KHoist makes that description discoverable and reports its observed health.
3. The consumer selects a named output and approves its endpoint independently.
4. The consumer calls the product service using its own access policy.

The registry does not proxy the query, load publisher code or embed product pages.
A product may operate its own UI; no shared product UI runtime is required for integration.

## What readiness means

Readiness depends on the observations configured for that product. A product with
metadata only has no automatic endpoint or database query check. Source, connector,
contract and composition conditions describe separate observations.

Always read the condition's reason and observed generation. A descriptor is a snapshot,
not a grant of access or a promise that a future request will succeed.
