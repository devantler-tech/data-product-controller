---
title: Registry HTTP API
description: Identify metadata routes and the capabilities needed to use them.
---

Registry endpoints return public descriptions. They do not execute queries, return
product records or grant permission to use a published URL.
Deployment authentication and access policy are owned by the platform.

| Route                                             | Purpose                          | Required optional capability   |
| ------------------------------------------------- | -------------------------------- | ------------------------------ |
| `GET /`                                           | API-only registry workspace      | None                           |
| `GET /api/v1/products`                            | Existing product-list projection | None                           |
| `GET /api/v1/ui-config`                           | Workspace capability settings    | None                           |
| `GET /api/v2/products`                            | Bounded descriptor discovery     | Registry discovery             |
| `GET /api/v2/products/{namespace}/{name}`         | One public descriptor            | Registry discovery             |
| `GET /api/v2/schema`                              | Discovery and descriptor schemas | See discovery reference        |
| `GET /api/v2/products/{namespace}/{name}/lineage` | Bounded dependency trace         | Registry discovery and lineage |
| `GET /api/v1/catalog`                             | DCAT catalogue                   | DCAT catalogue                 |

## Select a named output

Use the descriptor's output name and standard contract in your application.
Keep metadata validation separate from endpoint approval and query execution.
The [consumer guide](/guides/consume-a-product/) demonstrates that boundary.

## Handle incomplete observations

A disabled capability is not an empty successful catalogue. Invalid, oversized or
unreadable metadata can be rejected or explicitly omitted from discovery.
Do not treat an incomplete scan as proof that a product is absent.

The [discovery reference](https://github.com/devantler-tech/data-product-controller/blob/main/docs/registry-discovery.md)
defines query parameters, response shapes, limits and HTTP failures.
The [dependency-trace reference](https://github.com/devantler-tech/data-product-controller/blob/main/docs/dependency-traces.md)
defines partial branches and lineage requirements.
