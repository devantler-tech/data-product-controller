# Portable product interfaces

The `data-product-ui/v1` contract lets a publisher deploy one interface and use it
in independently built catalogues. It defines public metadata, a restricted
iframe, and two optional presentation capabilities. The reference registry and
the standalone compatibility kit both implement it. Neither imports product code
into its own document, proxies product requests, or owns the product runtime.

## Release gate and rollout

The OpenFeature flag `ui-contract` defaults off in the controller, demo product,
and kit. Set `UI_CONTRACT_ENABLED=true` explicitly for a development evaluation.
The chart exposes `uiContract.enabled: true`; the registry additionally requires
`registryUI.enabled: true`. With routing enabled, the chart declares the catalogue
origin in the demo manifest and in the demo's separate `UI_HOST_ORIGINS` setting.

A contract-bearing UI is unavailable in the registry while the flag is off.
It does not fall back to a legacy frame. Existing descriptors without `contract`
continue to use message-free sandboxed embedding. Rollback disables the flag and
reloads host pages; already loaded browser sessions are not remotely revoked.
Apply the updated CRD before upgrading an existing Helm installation, as described
in the [installation guide](../README.md#install).

Platform owns production activation and immutable artifact pins. Release, local
browser tests, and a successful handshake do not prove a production rollout.
[Issue #120](https://github.com/devantler-tech/data-product-controller/issues/120)
tracks rollout acceptance and eventual removal of the temporary flag.

## Publish a manifest

The standalone manifest is exactly the descriptor's `ui` object. Publish this
metadata in `spec.ui` or distribute the JSON independently. The hosts never fetch
an arbitrary manifest URL. See [the example](../examples/ui-manifest.json):

```json
{
  "url": "https://products.example/harbour/ui",
  "title": "Explore harbour observations",
  "contract": {
    "apiVersion": "data-product-ui/v1",
    "hostOrigins": ["https://catalog.example", "https://kit.example"],
    "capabilities": ["status", "resize"]
  }
}
```

All fields shown are required and unknown fields are rejected by the browser
validator. Use `capabilities: []` when no presentation hints are needed. A manifest
is at most 16 KiB of UTF-8 JSON; a title has 1–200 characters. The HTTPS entrypoint
is at most 2,048 characters, with no credentials, whitespace, backslashes, or
fragment. URLs and manifests are public metadata: never put credentials in a URL,
query string, title, or other field.

Declare 1–16 distinct exact HTTPS host origins, each at most 253 characters.
Use lowercase DNS names or canonical IPv4 addresses; use a non-default port only
when needed (`https://127.0.0.1:8443`). Paths, trailing slashes, wildcard hosts,
credentials, queries, fragments, IPv6 literals, and explicit default `:443` ports
are not part of the v1 origin subset. Browser validation is stricter than the
CRD's structural schema and refuses noncanonical declarations before navigation.

The manifest allowlist is one side of the agreement. Configure the **product's
own deployment** with its approved hosts too. The demo reads `UI_HOST_ORIGINS`, a
comma-separated list with no spaces, only when its release flag is on. Its public
`/ui-contract-config` response contains only that list. Do not accept an allowlist
supplied in the host's initialization message. No user identity, Kubernetes
identity, access token, cookie, or dataset passes over this protocol.

## Message protocol

Every message is an object with exactly the documented keys. All messages carry
`apiVersion: "data-product-ui/v1"` and the current `session` string. Unsupported
versions, unknown fields, stale sessions, and ungranted hints are ignored.

| Direction | Type | Additional fields | Meaning |
| --- | --- | --- | --- |
| Host → product | `init` | `capabilities: []` | Fresh UUID session and intersection of requested capabilities with host policy |
| Product → host | `ready` | None | This document accepted initialization and speaks v1 |
| Product → host | `status` | `state: "ready"` or `"error"` | Optional interface status hint; requires `status` grant |
| Product → host | `resize` | `height: integer` | Optional height in CSS pixels, 240–1200 inclusive; requires `resize` grant |

The registry grants `status` and `resize` when requested; these are its fixed
local presentation policy. The kit grants neither until its operator selects
the respective checkboxes. Hosts must never interpret a message as controller
readiness, data freshness, permission to query, or a change to access policy.
The `ready` handshake is a protocol signal and requires no optional capability.

The host uses `sandbox="allow-forms allow-scripts"`, **without**
`allow-same-origin`, and `referrerpolicy="no-referrer"`. Product messages therefore
have the opaque origin `"null"`. The host requires all of: that origin, the exact
iframe `contentWindow`, the current unpredictable session, the exact message
shape, and the relevant capability. A `null` origin alone authenticates nothing.
The host sends initialization only to that iframe window. Its target origin must
be `"*"` for opaque-origin delivery; this envelope contains public protocol
metadata only. See the browser's
[postMessage rules](https://developer.mozilla.org/en-US/docs/Web/API/Window/postMessage).

The product accepts initialization only from `window.parent`, at an exact origin
in its **own** allowlist, with the documented version, shape and capabilities.
It replies with the exact `event.origin` as `targetOrigin`. See the complete
[demo implementation](../internal/demoproduct/ui/product.js). Public query endpoints
used by an opaque frame must support credential-free CORS. The demo uses a public
API; authenticated products need their own separately designed access mechanism.
V1 supplies no authentication or credential-transfer capability.

Every frame load rotates the session. Selection changes, closing, and a ten-second
handshake timeout revoke it and remove the frame. More than 256 matching-session
messages also closes the frame with an error; hints are for discrete UI changes,
not continuous telemetry. Select the product again to start a fresh session. A
query failure can report `error` followed by `ready` on recovery within a session.

The sandbox does not certify publisher code, prevent every network request it
can make, or authenticate a redirected destination. Loaded code remains untrusted.
Hosts need their own publication policy, TLS, CSP, and product access controls.
The kit checks interoperability, not publisher security or universal accessibility.

## Run the independent compatibility kit

Use a TLS certificate trusted by your browser for the chosen host. The Go command
serves only static assets and has no Kubernetes or registry dependency:

```bash
UI_CONTRACT_ENABLED=true go run ./cmd/ui-kit \
  --listen-address 127.0.0.1:8443 \
  --tls-cert /path/to/local-cert.pem \
  --tls-key /path/to/local-key.pem
```

Open `https://127.0.0.1:8443`, include that exact origin in the manifest and the
product's own allowlist, paste the manifest, choose grants, and select **Validate
and open**. Keep private keys out of the repository. The demo command is HTTP
behind an independently managed TLS endpoint; the kit does not terminate TLS for
the product. When testing the chart's demo in a second host, explicitly configure
that additional origin in both its manifest and its own deployment.

Alternatively, serve `web/index.html`, `kit.css`, `kit.js`, and `ui-contract.js`
from any HTTPS static host. Apply the policy in `web.KitHandler`: same-origin
scripts/styles, no host connections, HTTPS frames, no ancestors, objects or base
URI, no referrers, and no camera, microphone or geolocation permissions. That
static deployment is an explicit opt-in; it has no Go/OpenFeature process gate.

To build another host, load `web/ui-contract.js` in your own document and call:

```javascript
const dispose = DataProductUI.mount({
  frame: document.querySelector("iframe"),
  manifest,
  grants: ["status"],
  onState: (state) => { /* Render your own accessible loading/ready/error/timeout text. */ }
});
// Before selecting another product or closing this one:
dispose();
```

`DataProductUI.validate(manifest, location.origin)` validates without navigating.
`mount` validates again, throws on refusal, and returns an idempotent disposer.
Catch refusal and render it as inert text. Do not run multiple mounts on the same
frame; dispose the current one first. The library is framework-independent.

## Compatibility and accessibility checks

Run `go test -tags=browser ./internal/browser` to exercise actual Chromium against
independent TLS fixture servers. The suite verifies both release states, the same
demo in both hosts, queries, error recovery, denied origins, bounded capabilities,
wrong-window/origin/session rejection, navigation revocation, message limits, and
handshake timeout cleanup. Browser certificate bypass is confined to these local
test fixtures; production hosts must use verified HTTPS.

For a third-party UI, use the kit and check all of the following:

- A manifest for the wrong host or version is rejected without opening a frame.
- The loading message changes only after a compatible handshake; blocked or
  unavailable pages time out and can be retried.
- Query controls work with the keyboard, have labels and visible focus, and status
  changes are announced. Closing the interface is keyboard accessible.
- At 375 pixels wide, controls and results fit without horizontal scrolling.
- A query failure reports a useful message; a successful retry restores results.
- Unchecking a grant prevents that hint from affecting the host. Closing or
  navigating the frame makes the old session unusable.

The bundled browser tests cover keyboard submission, narrow-screen overflow and
live status semantics, but do not replace screen-reader testing of each product.
Products that cannot use the protocol can keep their existing message-free UI
declaration. [ADR 0007](adr/0007-portable-ui-contract.md) records the boundary.
