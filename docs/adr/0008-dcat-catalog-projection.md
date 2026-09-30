# 0008: Opt-in DCAT catalog projection

Date: 2026-09-30

Status: Accepted

## Context

Independent catalogs need standard relationships between datasets, their
representations and the services exposing them. DataProducts carry the relevant
public metadata, but a general data capability is not necessarily a dataset.
Discovery must preserve publisher intent without copying credentials, querying
products or claiming authorization and availability.

## Decision

The default-off OpenFeature `dcat-catalog` flag controls a read-only
`GET /api/v1/catalog` endpoint. The operator supplies a stable catalog URI through
`DCAT_CATALOG_ID`; HTTP request hosts and proxy headers never determine identity.
The chart exposes `dcatCatalog.enabled` and `dcatCatalog.id`. The JSON-LD context is
embedded and uses DCAT 3, Dublin Core and FOAF terms without remote resolution.

A publisher opts in with `data.devantler.tech/dcat-type: Dataset`. This declares
one logical dataset whose named outputs provide access to its representations.
An absent annotation excludes the product; any unsupported present value fails
the export. The annotation is a publisher assertion, not observed semantic
conformance. The profile requires no new CRD fields.

Each participating product becomes a Dataset. Its named outputs become
Distributions with associated DataServices, preserving access URLs and endpoint
description links. Ownership is represented by a `foaf:Agent`; its support link
uses `foaf:page` so a shared page does not assert shared agent identity. Media-type
text remains descriptive `dcterms:format` metadata. The projection does not invent
download URLs, standards conformance, licenses, policy, provenance or timestamps.
Readiness, private infrastructure references, Secrets and product records are
outside the projection.

The product's published ID identifies the Dataset. Output identities use
`https://devantler.tech/.well-known/data-product/{kind}/{digest}`, with kind
`distribution` or `service` and the lowercase hexadecimal SHA-256 digest of the
UTF-8 JSON array `[kind, productID, outputName]`. The owned namespace supplies
identifiers, not resolving endpoints. Kubernetes names, catalog host, output
ordering, endpoint changes and product versions do not alter these identities.
Conflicting identities across all emitted entity kinds fail the export.

Identifiers use absolute HTTPS or a restricted URN profile: a 2–32 character
ASCII alphanumeric/hyphen namespace identifier with alphanumeric ends, followed
by a nonempty namespace-specific string containing only letters, digits, colons,
periods, underscores or hyphens. Publishers own the namespace-registration and
assignment obligations. The parser does not claim to accept every RFC 8141 form.

One catalog request runs per handler; concurrent requests receive 503. Kubernetes
reads use at most 16 objects per page, a 256-product budget, at most 17 requests
and one five-second deadline. Public metadata is validated and copied per page;
private and nonparticipating object fields are discarded. Bounds also limit
outputs to 1,024, individual metadata fields to 16 KiB, retained public metadata
to 1 MiB and encoded JSON-LD to 2 MiB. No failure returns a partial catalog as a
successful response.

## Consequences

An independent JSON-LD consumer can discover the actual TLS demo query service
without controller-specific descriptor parsing or remote context loading. The
Kubernetes suite separately verifies chart activation and rollback, publisher
withdrawal, malformed declarations and duplicate-identity rejection.

Discovery remains cluster-wide public metadata and grants no data access. Platform
owns routing, access policy and production activation. The default-off gate and
publisher annotation provide separate deployment and publication controls.
[Issue #123](https://github.com/devantler-tech/data-product-controller/issues/123)
tracks rollout and flag retirement with a review on 2026-10-30. Release and test
evidence alone do not activate production.

The [catalog guide](../dcat-catalog.md) defines the mapping, limits, errors and
examples. Dataspace Protocol adapters, policy negotiation, transfers, federation
and import remain in [#7](https://github.com/devantler-tech/data-product-controller/issues/7).

## References

- [W3C DCAT 3](https://www.w3.org/TR/vocab-dcat-3/)
- [W3C JSON-LD 1.1](https://www.w3.org/TR/json-ld11/)
- [FOAF vocabulary](https://xmlns.com/foaf/spec/)
- [RFC 8141: Uniform Resource Names](https://www.rfc-editor.org/rfc/rfc8141.html)
