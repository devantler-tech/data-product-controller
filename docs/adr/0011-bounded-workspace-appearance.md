# 0011: Bounded workspace appearance across product sandboxes

Date: 2026-10-01

Status: Accepted

## Decision

The reference workspace uses compact native controls and orange accents on white
or black. Its Theme selector offers System, Light and Dark. An explicit choice
persists where browser storage is available; denied storage does not block data
discovery or queries. Product records remain in the independently deployed UI.

`data-product-ui/v1` retains exactly status and resize capabilities.
`data-product-ui/v2` uses the same closed manifest shape and adds only an optional
appearance capability. The host requires both the existing default-off OpenFeature
`ui-contract` gate and the additional default-off `ui-appearance` gate before v2
navigation. The sample requires both gates and its own publisher-approved hosts.
The new gate is long-lived: operators independently decide whether a product may
follow a workspace's cosmetic preference. It grants no data or credential access.

The host resolves System locally and sends only light or dark after an accepted
v2 ready handshake, intersecting publisher requests with explicit host grants.
The product authenticates the exact approved parent window/origin, current session,
version and closed message shape. It assigns a fixed stylesheet attribute, never
CSS, URLs, credentials or user context. Both endpoints bound updates to 256 per
session; the host suppresses duplicates. Navigation rotates the session and
requires a new handshake before applying the latest appearance.

## Rationale and boundaries

Native color-scheme inheritance does not reliably update a dynamically themed
opaque embedded document in the evaluated browsers. A bounded presentation hint
lets the product keep its current query and results without navigation. A distinct
version preserves v1's closed capability vocabulary; admission additionally
rejects appearance under v1.

Iframe permissions remain `allow-forms allow-scripts`, without allow-same-origin.
The host imports no product code, proxies no query, transfers no identity, and
owns no product runtime. Public endpoint CSP and credential-free fetches remain
independent of appearance. Source tests, hosted API admission and actual Safari
on Platform validate separate layers; source release alone is not rollout proof.
