# Publish a DCAT catalog

`GET /api/v1/catalog` publishes a read-only DCAT 3 JSON-LD projection for independent
catalog consumers. The Kubernetes resources remain the source of truth. The endpoint
returns `application/ld+json` with an embedded context and `Cache-Control: no-store`.
It does not fetch remote contexts, contracts, or product data.

## Declare dataset semantics

A publisher opts a product into the profile with this exact annotation:

```yaml
metadata:
  annotations:
    data.devantler.tech/dcat-type: Dataset
```

The annotation asserts that the product describes **one logical dataset** and that
**every named output provides access to a representation of that dataset**. A
general processing capability or catalog-document endpoint must not opt in unless
it satisfies that assertion. The output's protocol name alone does not establish
dataset semantics. OpenAPI, AsyncAPI, GraphQL, DCAT and ArrowFlight outputs use the
same publisher-declared mapping; the controller does not verify their contents.

An absent annotation excludes the product. An empty, differently cased or unknown
value fails the whole export with HTTP 422. Removing the annotation withdraws the
dataset without deleting the product or its workloads. Ready and unready products
are eligible: publication is independent of controller readiness.

The catalog exposes public identity, ownership, documentation and interface
metadata. It omits source and Secret references, connector/probe identities,
conditions, lineage and records. A listing does not grant authorization, promise
availability, or assert licenses, policies, timestamps or provenance. The API uses
the controller's cluster-wide product reader; it is not a tenant-filtered view.
Platform owns the endpoint's routing and access policy.

## Enable publication

The OpenFeature `dcat-catalog` release flag defaults off. It is independent of the
registry UI and portable UI flags. With it disabled, the endpoint returns 404
without reading Kubernetes.

Configure the chart with a stable identity owned by the catalog operator:

```yaml
dcatCatalog:
  enabled: true
  id: https://catalog.example.com/id
```

The equivalent process settings are `DCAT_CATALOG_ENABLED=true` and
`DCAT_CATALOG_ID=https://catalog.example.com/id`. Invalid booleans or a malformed,
nonempty ID prevent startup. Helm rejects enablement without a nonblank ID and
rejects Kubernetes environment expansion syntax in the ID. An enabled handler
without an ID returns 503 before any Kubernetes read.

Catalog and product identities accept absolute HTTPS identifiers or the supported
URN subset. URN namespace identifiers contain 2–32 ASCII letters, digits or hyphens
and begin and end with a letter or digit. Their namespace-specific string is
nonempty and contains only letters, digits, colons, periods, underscores or
hyphens. This is a restricted syntax profile, not a complete RFC 8141 parser.
Publishers are responsible for using a registered URN namespace according to its
assignment rules. `urn:example:...` values in fixtures are examples, not production
namespace assignments. Identities and public links are limited to 2,048 bytes;
credentials, whitespace and control characters are rejected.

Raw brackets in URL paths, queries or fragments are rejected consistently with the
Dataspace consumer profile. Percent-encoded brackets and IPv6 authority brackets remain
supported. Rejected product metadata yields no partial catalog.

The configured catalog ID is independent of the request URL, proxy headers and
public routing host. Keep it stable across redeployments. The controller never
constructs it from `Host` or `X-Forwarded-Host`.

When the chart enables DCAT, its optional harbour demo DataProduct receives the
Dataset annotation. That resource still requires `demoProduct.enabled=true` and
`route.enabled=true`. Other products need their publishers' explicit annotations.

## Metadata mapping and identity

| Public metadata                      | RDF term or relationship                                                         |
|--------------------------------------|----------------------------------------------------------------------------------|
| Configured catalog ID                | Catalog `@id`; type `dcat:Catalog`                                               |
| Product `spec.id`                    | Dataset `@id`; literal `dcterms:identifier`                                      |
| Product name, description, version   | `dcterms:title`, `dcterms:description`, `dcat:version`                           |
| Owner name and support/ownership URL | `dcterms:publisher` as a `foaf:Agent`, with `foaf:name` and optional `foaf:page` |
| Documentation URL                    | Optional `dcat:landingPage`                                                      |
| Named output                         | `dcat:Distribution` linked by `dcat:distribution`                                |
| Output URL                           | Distribution `dcat:accessURL` and service `dcat:endpointURL`                     |
| Output's service                     | `dcat:DataService`, linked by `dcat:accessService` and catalog `dcat:service`    |
| Product served by the service        | `dcat:servesDataset`                                                             |
| Contract URL                         | `dcat:endpointDescription`                                                       |
| Optional media-type description      | Literal `dcterms:format`, preserving the declared text                           |

Owner links use `foaf:page`: a shared support page does not identify two teams as
the same agent. A contract document describes the actual endpoint; the projection
does not invent a `dcterms:conformsTo` standard. Output URLs are access locations,
not asserted `dcat:downloadURL` files. Descriptive media-type text does not assert
IANA registration. The projection is a constrained DCAT profile and does not claim
DCAT-AP or Dataspace Protocol conformance.

Dataset identity is the exact published `spec.id`. Distribution and service IDs
have this form:

```text
https://devantler.tech/.well-known/data-product/{kind}/{digest}
```

`kind` is `distribution` or `service`. `digest` is lowercase hexadecimal SHA-256 of
the UTF-8 JSON array `[kind, productID, outputName]`. These are stable identifiers
in an owned HTTPS namespace, not resolving endpoints. Clients use the separately
published endpoint and contract URLs for access. Identity remains stable across
Kubernetes names, namespaces, output ordering, catalog identity, endpoint rotation
and product-version changes. Changing the product ID or output name creates a
different identity. Duplicate product IDs and collisions between catalog, dataset,
distribution and service identities fail the entire export.

## Limits and errors

Each handler permits one concurrent catalog request. A busy handler returns 503
instead of queuing another scan. Each scan uses pages of at most 16 Kubernetes
objects, a 256-product scan budget, at most 17 API requests, and one shared
five-second read deadline. Only validated public metadata from participating
products is retained between pages; unrelated/private object fields are discarded.

The export permits at most 1,024 outputs, 16 KiB per metadata field, 1 MiB of
retained public metadata and 2 MiB of encoded JSON-LD. The scan budget includes
products without the annotation. Failure never returns a partial catalog as a
successful response.

| Status | Meaning and action                                                                                                                         |
|--------|--------------------------------------------------------------------------------------------------------------------------------------------|
| 200    | Complete JSON-LD catalog, possibly containing no datasets.                                                                                 |
| 404    | Release flag disabled; no Kubernetes read occurred.                                                                                        |
| 413    | Scan or encoded-response bound exceeded; reduce the catalog's size.                                                                        |
| 422    | Invalid annotation, public metadata or conflicting identity, including metadata/output bounds; correct the publisher's declaration.        |
| 503    | Missing configured ID, unavailable/timed-out Kubernetes API, or another active catalog request; fix configuration or retry after recovery. |

Errors do not echo backend diagnostics or private resource references. The limits
bound discovery work; they do not attest to a product's data quality or service
health.

## Try the profile

The [dataset example](examples/dcat-product.yaml) is a public contract that can be
applied to Kubernetes.
Its `example.com` links are illustrative: applying it does not deploy those
services or make their data available.

In a disposable cluster where this release is installed as `dpc` in namespace
`products`, with DCAT enabled and an explicit catalog ID:

```bash
kubectl apply -f docs/examples/dcat-product.yaml
kubectl port-forward -n products service/dpc 8082:80
```

In another terminal, read the complete catalog:

```bash
curl --fail --silent --show-error http://127.0.0.1:8082/api/v1/catalog
```

Withdraw the example from discovery while preserving the resource:

```bash
kubectl annotate -n products dataproduct/catalog-harbour data.devantler.tech/dcat-type-
```

Run the independent consumer against an actual local TLS product without a cluster:

```bash
go test ./internal/catalog -run '^TestCatalogIndependentConsumer$' -count=1
```

The test exercises the catalog's HTTP handler, expands its response using the
independent JSON-Gold processor with remote document loading disabled, follows the
Dataset → Distribution → DataService relationships, and queries the discovered TLS
demo endpoint. It verifies JSON-LD processing without external context resolution;
the final product query is a real HTTPS request.

The [Kubernetes acceptance suite](source-integration-tests.md) additionally verifies
the installed chart's disabled/enabled/rollback states, annotation withdrawal,
invalid declarations, duplicate identities and recovery. These fixtures provide
controlled interoperability evidence, not production rollout evidence.

## Rollout and remaining scope

Platform owns production activation, access policy and rollback. Disable
`dcatCatalog.enabled` to restore 404 responses; published products and workloads
remain intact. [Issue #123](https://github.com/devantler-tech/data-product-controller/issues/123)
tracks activation and flag retirement, with an acceptance review on **2026-10-30**.
That date does not automatically enable publication or remove the flag.

Dataspace Protocol adapters, policy negotiation, transfers, federation and import
remain in [roadmap #7](https://github.com/devantler-tech/data-product-controller/issues/7).

## References

- [W3C DCAT 3](https://www.w3.org/TR/vocab-dcat-3/)
- [W3C JSON-LD 1.1](https://www.w3.org/TR/json-ld11/)
- [FOAF vocabulary](https://xmlns.com/foaf/spec/)
- [RFC 8141: Uniform Resource Names](https://www.rfc-editor.org/rfc/rfc8141.html)
