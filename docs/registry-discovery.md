# Portable registry discovery

The registry publishes public product metadata. It does not return source or probe
resource references, credentials, product records, or arbitrary provider diagnostics.
Discovery grants no data access. Product APIs and their authentication remain independently
operated.

## Enable the release capability

`REGISTRY_DISCOVERY_ENABLED` defaults to `false`. The process validates the value and
evaluates the `registry-discovery` OpenFeature flag for every request. Helm exposes
the same choice as `registryDiscovery.enabled`. Invalid settings fail startup.

When disabled, every new discovery route returns `404` without reading Kubernetes.
The existing registry workspace and `/api/v1/products` remain available.
`GET /api/v1/ui-config` advertises `discoveryEnabled` independently of the UI contract
and appearance grants. Enabling discovery does not enable either presentation grant.

## Read a product or page

`GET /api/v2/products/{namespace}/{name}` reads one exact product through the uncached
Kubernetes reader. Namespace and name must be valid Kubernetes identifiers; the
request accepts neither query parameters nor a body. A missing product returns
`404`, while a failed inventory read returns `503`.

`GET /api/v2/products?limit=50&namespace=products` returns one native Kubernetes list
page. `limit` defaults to 50 and must be an integer from 1 to 100. Omit `namespace`,
or supply an empty value, to select all namespaces accessible to the registry's
existing Kubernetes identity. A nonempty namespace is a DNS label. Namespace selection
is filtering, **not** tenant authorization; no new RBAC is granted by this feature.

```json
{
  "apiVersion": "data-product-discovery/v1",
  "products": [],
  "continue": ""
}
```

Each product has `apiVersion: data-product-descriptor/v1`, `kind: DataProduct`, and
the existing flat public metadata fields (`namespace`, `name`, `id`, `displayName`,
`description`, `version`, `owner`, outputs and optional inputs, UI and lineage).
The descriptor also carries `generation`, `observedGeneration`, `ready`, `readiness`
and the health dimensions described below.

For the next page, send the returned `continue` value unchanged and retain the same
namespace and limit. Continue until its value is empty; an empty product array with
a nonempty continuation is not the end of the inventory. The API preserves native
Kubernetes snapshot ordering and does not sort each page or fabricate an exact total.
An exact lookup is a newer independent read, not part of an earlier list snapshot.

Continuations are bounded base64url-encoded tuples containing the namespace, limit
and the native Kubernetes continuation. They work across registry replicas and
restarts. Treat them as opaque in clients. The server strictly checks their shape
and query binding, but they are **untrusted consistency input**, not signed tokens,
credentials or an authorization boundary. A caller can manufacture a tuple; Kubernetes
still validates the native continuation's snapshot, expiry and resource scope under
the same registry identity. No registry-side cursor state or secret is required.

Duplicate, malformed and unsupported query parameters return `400`. A namespace
without products is a successful empty page, not an unavailable backend. An expired
Kubernetes continuation returns `410`; discard the partial snapshot and restart from
the first page. Never merge pages from different restarted snapshots as one complete
inventory.

## Interpret public health

`health` contains `source`, `connector`, `contracts` and `composition`. Each dimension
has `state`, a static public `message`, the current product `generation`, and the
condition's `observedGeneration` (zero when no applicable condition is present).

| State | Meaning |
| --- | --- |
| `ready` | A true condition observes this exact product generation. |
| `not-ready` | A current false condition needs attention from the product owner. |
| `stale` | The condition observes another generation; its success is not current evidence. |
| `unobserved` | An applicable independent condition is absent or inconclusive. |
| `disabled` | A current false condition carries this dimension's recognized feature-disabled reason. |
| `not-applicable` | The product does not declare this capability. |

Missing independent conditions are never inferred from aggregate readiness. For example,
legacy source observation and composition without declared contract requirements can
have no independent condition; their dimension remains `unobserved`, even when the
aggregate condition supplies other information. The descriptor does not infer a live
feature flag's value from missing status.

Top-level `observedGeneration` refers to the aggregate Ready condition. `ready` is false
when this condition is absent, unknown or stale. `readiness.reason` and optional
`composition.reason` use the same public state vocabulary. All explanatory messages
are fixed public text; provider errors, raw condition reasons and condition messages
are not copied. Lineage reasons are reduced to `InputReady` or `InputNotReady`.
Lineage is included only when the composition condition observes the current generation.
The aggregate condition retains its own semantics; independent dimensions do not
rewrite it. In particular, aggregate `ready: true` may coexist with an `unobserved`
dimension, and consumers must display that dimension without inventing a successful check.
All generation values must be nonnegative integers no larger than 9,007,199,254,740,991,
so independent JavaScript clients can compare them exactly.

These are metadata observations at the time of the read. They do not assert current
endpoint availability, schema compatibility beyond declared metadata, effective data
permissions, successful data-plane requests or production adoption. An exported file
is an offline snapshot and cannot authorize a later UI session.

## Bounds, failures and schema compatibility

Every inventory read has a five-second deadline and a maximum native page size of
100 products. A descriptor is limited to 64 KiB of encoded UTF-8 JSON; individual
strings are limited to 16 KiB of UTF-8 and port/lineage arrays to 1,024 entries.
The entire encoded page is limited to 2 MiB. The native continuation is limited to
8 KiB and the wrapped cursor to 16 KiB. Success is buffered before any descriptor
bytes are written. Oversized metadata or pages return `413`, never a truncated success;
reduce the page limit or the publisher's metadata as applicable.
The v2 projection also validates required metadata, canonical product/port identities,
the protocol vocabulary and public HTTPS links before encoding. Embedded credentials,
fragments, backslashes and malformed hosts are rejected with `422`; optional empty
fields are omitted according to the existing descriptor types. These v2 checks do not
change the legacy v1 projection.

Successful responses and errors use `Cache-Control: no-store`. Errors are JSON objects
with stable `error` and public `message` fields:

| HTTP status | Code / recovery |
| --- | --- |
| `400` | Invalid request, query, product reference or native continuation. Correct the input. |
| `404` | Disabled capability or `product-not-found`. |
| `408` | `request-canceled`; the request was canceled. |
| `410` | `continuation-expired`; restart the inventory snapshot. |
| `413` | `descriptor-too-large` or legacy `collection-too-large`; reduce metadata or use smaller pages. |
| `422` | `invalid-descriptor`; the published metadata cannot be encoded for this contract. |
| `502` | The inventory reader violated the requested object count, namespace or identity. |
| `503` | `backend-unavailable`; retry explicitly. |
| `504` | `deadline-exceeded`; the bounded inventory read timed out. |

`GET /api/v2/schema` (or `?type=descriptor`) serves the descriptor's JSON Schema
2020-12 document with ID `urn:data-product-descriptor:v1`. `?type=discovery` serves the
page schema with ID `urn:data-product-discovery:v1`. Register both bundled documents
by these IDs for offline validation; the page references the descriptor URN. Neither
schema requires a network fetch. JSON Schema counts string characters; consumers must
also enforce the documented UTF-8 byte bounds and total encoded size before parsing.
UI URL, origin and capability validation still belongs to the existing UI contract;
schema validation alone grants no permission to mount a product surface.

Consumers reject unsupported `apiVersion` and `kind` values. The v1 schema enumerates
its accepted fields; incompatible shape changes require a new descriptor version.
The old `/api/v1/products` retains its response shape and readiness projection for
inventories that fit one bounded 100-product read and the 2-MiB response cap. It returns
an explicit `413` if more products remain, rather than claiming the first page is the
complete inventory. Its success remains sorted by namespace and name.

The unit suite validates actual responses against these schemas using an independent
offline validator, tests incompatible versions and extra private fields, and exercises
page traversal across separate handler instances. Production rollout and flag retirement
remain separately tracked in issue #193.
