# Deploy an independent product UI host

The signed application image includes `/ui-kit`. It serves the embedded compatibility
page without a Kubernetes or registry connection. Pin the verified release digest,
override the image entrypoint with `/ui-kit`, and run with the image's nonroot user,
a read-only filesystem and restricted network access.

## Choose the transport

TLS is the default. Mount a certificate and private key readable by the workload's
nonroot user, then run:

```text
/ui-kit --listen-address 0.0.0.0:8443 \
  --tls-cert /tls/cert.pem --tls-key /tls/key.pem
```

The default listener is `127.0.0.1:8443` when `--listen-address` is omitted. A listener
must name an IP address or `localhost` and a port from 1 to 65535. Use bracketed IPv6
addresses when needed. Missing TLS material fails startup.

When the deployment's trusted Gateway already terminates HTTPS, explicitly choose
an internal HTTP backend:

```text
/ui-kit --http-behind-gateway --listen-address 0.0.0.0:8080
```

This mode requires an explicit listener and rejects either TLS option. The Gateway
must expose a verified HTTPS origin to browsers. Restrict access to the HTTP backend
with the deployment's Service, Gateway and network policy; the command does not
authenticate peers or establish a Gateway trust relationship. Keep the backend
private. Platform owns these resources and their rollout.

## Enable the interface and observe health

`UI_CONTRACT_ENABLED` defaults off. Set it to `true` to serve the page and assets.
`UI_APPEARANCE_ENABLED` is a separate default-off choice for optional v2 appearance
grants. Invalid boolean values fail startup. The host retains the restricted iframe
and content policy described in the [portable UI contract](ui-contract.md).

`GET /healthz` always returns `200` with the three-byte body `ok` followed by a newline,
including when the UI is disabled. `HEAD` returns the same headers without a body.
The endpoint rejects query parameters, request bodies and other methods. It reports
only process availability, supplies no configuration or product readiness, and makes
no external requests. Use it for workload liveness and readiness probes.

The listener limits headers to 16 KiB, header reads to five seconds, complete request
reads to ten seconds, writes to fifteen seconds and idle connections to sixty seconds.

The product publisher must approve this host's exact public HTTPS origin in both
the UI manifest and the product's own deployment. Opening a UI does not grant data
access, and the host supplies no credentials or product records. It never fetches or
proxies product APIs.

## Verify the packaged host

Run the container smoke against a built or verified immutable release image:

```bash
bash tests/source/ui-kit.sh ghcr.io/devantler-tech/data-product-controller@sha256:<verified-digest>
```

The smoke exercises TLS with a trusted disposable certificate and the internal HTTP
option, default-off and explicitly disabled behavior, enabled embedded assets, the
explicit v2 appearance gate, health and nonroot execution with a read-only filesystem.
It publishes only loopback ports
and deletes its containers and disposable certificate afterward. The required source
integration job runs it against the built application image. Release publication
runs it against the signed image digest after verifying the publisher, tag and source
revision.

Packaging checks establish release behavior. Platform adoption and UI flag retirement
remain tracked in [#120](https://github.com/devantler-tech/data-product-controller/issues/120).
