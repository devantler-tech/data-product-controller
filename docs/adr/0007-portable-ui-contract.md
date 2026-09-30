# 0007: Portable, capability-limited product UI contract

Date: 2026-09-30

Status: Accepted

## Decision

A versioned UI manifest is the product descriptor's `ui` object. It contains the
existing HTTPS `url` and accessible `title`, plus an optional `contract` containing
`apiVersion: data-product-ui/v1`, exact HTTPS `hostOrigins`, and requested
`capabilities`. The only v1 capabilities are `status` and `resize`. This is public
control-plane metadata; no application records, credentials or user identity are
included. Publishing a contract does not grant capabilities: the host explicitly
intersects the request with its local policy.

The registry supports this contract behind the default-off OpenFeature
`ui-contract` release flag. A contract-bearing product is unavailable when the
flag is disabled; it never silently falls back to legacy embedding. Existing
products without a contract retain their restricted, message-free embedding.

The host validates the whole manifest before navigation and keeps the iframe
sandbox at `allow-scripts allow-forms`, without `allow-same-origin`. Consequently
child messages have an opaque `null` origin. Hosts require that origin, the exact
iframe window, a fresh cryptographically random session per load, the v1 message
shape, and an explicit capability grant. Origin checks alone cannot authenticate
an opaque-origin child. The session is a correlation token, never a credential.

Hosts send only version, session and granted capabilities to that exact window.
Opaque-origin delivery requires `targetOrigin: "*"`; it must never carry private
data. Products accept initialization only from `window.parent` at an exact
origin in their own deployment configuration and reply to that exact origin.
Navigation, selection changes, disposal and timeouts invalidate the old session.
Messages are presentation hints, never changes to controller readiness or policy.

A portable browser library and static compatibility page are usable without
Kubernetes or registry imports. The registry and compatibility page load the same
independently deployed demo. Tests use real Chromium and synthetic hostile frames
to validate the boundary and the user-visible failure states.

## Limits and recovery

Manifests are limited to 16 KiB, 16 host origins, two capabilities and a 200-character
title. Origins have no paths, wildcards, credentials, query or fragment. Entry URLs
are HTTPS without credentials or fragments. Status messages contain only a fixed
enum; resize accepts integer heights from 240 to 1200 pixels. A ten-second
handshake timeout removes the frame and offers retry by selecting the product
again. The library never fetches a manifest or proxies product requests.

The sandbox cannot prove a publisher's code or data is trustworthy, prevent every
network request from that code, or authenticate the final destination of a frame
redirect. All loaded code remains untrusted; hosts pass no secrets, retain browser
sandbox restrictions, and independently govern which products may be published.
The kit establishes protocol compatibility, not security certification or full
accessibility conformance. Production rollout and flag retirement require separate
Platform acceptance.
