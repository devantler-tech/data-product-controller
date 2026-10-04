# Trace product dependencies

Select a product and choose **Trace inputs** to inspect its upstream products,
owners, versions and current readiness. The input table explains missing outputs
and incompatible declared contracts separately from producer health. You can open
an upstream product even when it is not ready, or choose **Save trace** to keep the
current observation as JSON.

Tracing is available when both `registryDiscovery.enabled` and
`registryLineage.enabled` are true. The process settings are
`REGISTRY_DISCOVERY_ENABLED` and `REGISTRY_LINEAGE_ENABLED`; both default to false.
The `registry-lineage` OpenFeature gate is evaluated on every request. Invalid
settings fail startup. The workspace advertises the capability as `lineageEnabled`.
The adoption decision is tracked in [#217](https://github.com/devantler-tech/data-product-controller/issues/217).

## Read a trace from another client

`GET /api/v2/products/{namespace}/{name}/lineage` accepts an exact Kubernetes
identity, with no body or query parameters. The bundled schema is available at
`GET /api/v2/schema?type=lineage`. Disabled trace routes return `404` before
reading any products. Invalid requests return `400`; an unavailable or invalid
root returns a fixed `404`, `422` or `502` error. A concurrent trace returns
`429`; retry explicitly. Every response uses `Cache-Control: no-store`.

The format is `data-product-lineage/v1`. `root` is the selected
`namespace/name`. `nodes` contain each inspected product once; `edges` connect
a consumer's named input to the producer's named output. Products and edges are
sorted by identity and input name. `depth` records the traversal depth where an
edge was first inspected, rather than a unique distance in a graph with shared inputs.

An edge's `compatibility` is `compatible`, `output-missing`,
`contract-incompatible` or `not-evaluated`. Required stable versions accept newer
versions within the same major; major zero requires an exact match. Compatibility
uses declared protocol and version metadata, without reading schema contents.

Node `state` and generation fields explain current readiness independently.
`stale` means the observation belongs to another generation. Missing conditions
remain `unobserved`; deleting producers are explicitly `deleting`.
Independent `health` dimensions retain their own observation states.

The browser validates each retained node against its containing product generation
and the public text and URL profile. Failed lookups carry only their identity,
fixed state and zero generations; they do not invent publisher metadata or contract
compatibility. Every retained node must connect to the selected root. Resolved
edges remain acyclic even beside incomplete branches, and depths must describe a
possible bounded path. Shared dependencies may have several valid path lengths.
Known version contradictions are rejected using exact integer comparisons; absent
output declarations cannot establish output existence or protocol compatibility.

`complete: true` means the permitted dependency graph was inspected. It does
**not** mean every product is ready, every contract is compatible, an endpoint is
reachable, or data access is authorized. Reads happen over time and are not an
atomic inventory snapshot. A saved trace cannot authorize a later product session.

Incomplete traces retain useful branches and a fixed `issues` list: missing or
unavailable products, invalid public metadata, unexpected identities, foreign
namespace references, cycles, timeouts and traversal limits. An edge may name a
target that was not inspected; never infer its readiness from its reference.

## Inspect a saved trace offline

The example client uses only the Go standard library and imports no controller or
Kubernetes packages:

```bash
go run docs/examples/lineage-client/main.go products-root-lineage.json
```

It reads a caller-selected regular JSON file up to 2 MiB, checks the format version,
rejects unknown fields and prints declared relationships with producer states.
It is a reading example, not a full schema validator or a data client. Validate
untrusted files against the bundled schema when building a consumer. It performs
no network requests and requires no Kubernetes configuration.

## Observation limits

The registry follows declared inputs in the selected namespace and rejects foreign
references before a read. Status lineage cannot redirect traversal. The public
projection excludes provider resource references, credentials and raw condition
diagnostics; tracing never fetches contracts, endpoints or product data.

Each instance runs one trace at a time. A trace shares a five-second deadline and
permits at most 256 unique read attempts, including missing products, 1,024 inputs
and 64 product levels. It retains at most 1 MiB of canonical public metadata and
buffers the response before sending at most 2 MiB. Limits produce an explicit
incomplete observation; an oversized encoded response returns `413`. Refresh or
product navigation revokes pending work and downloads.

[ADR 0018](adr/0018-dependency-traces.md) records the architecture and permission boundary.
