# Connect an existing HTTPS JSON export

The optional `http-source` workload serves a governed JSON export through `GET /api/data` and
publishes its contract at `/openapi.json`. It reads the source itself; the controller receives no
source credentials or data. The workload is absent from default chart installations, and the
binary's OpenFeature `http-source` release flag defaults off.

## Source and access contract

Use an existing HTTPS endpoint with a certificate trusted by the connector's system trust store.
The endpoint must return HTTP 200, `Content-Type: application/json`, and one complete JSON value no
larger than 1 MiB. Redirects, compressed responses, partial responses, and invalid JSON fail closed.
The connector supports a fixed export rather than arbitrary queries: it accepts no query parameters,
request bodies, caller-selected destinations, writes, or forwarded caller credentials.

The source owner must issue a **read-only credential scoped to the intended export**. Sending only
GET requests does not make a credential read-only, and the connector does not filter source fields.
Everything the export returns is available to every permitted consumer. Consumer authentication,
authorization, source-side filtering, and retention policy belong to the product's deployment and
source owner.

The internal Service has no public route, wildcard CORS policy, or automatic registry registration.
The chart allows data access only from pods matching explicit labels in the release namespace.
Namespace operators who can create or label those pods can grant that access. Use a namespace with
appropriate administrative ownership and a CNI that enforces NetworkPolicy. Policies are additive:
a broader policy selecting this workload can expand its access.

## Configure the projected Secret

Use your deployment's secret-management system to create a Secret in the release namespace with a
`config.json` key. Its value has exactly these fields:

```json
{
  "endpointURL": "https://source.example.com/export",
  "bearerToken": "replace-with-a-read-only-token"
}
```

Keep this file and its values out of Git, terminal output, and product descriptors. The HTTPS URL
must contain a host and no embedded credentials, query string, or fragment. The token must use
bearer-token characters without whitespace. Configuration is limited to 16 KiB; missing fields,
unknown fields, malformed JSON, and trailing documents make the source unavailable.

The chart references an existing Secret; it never creates one or reads its values during rendering.
It mounts the directory read-only without `subPath`. Every source access reopens the file, so
projected Secret updates take effect after Kubernetes updates the volume. Endpoint and credential
must be in the same JSON file so rotation cannot combine values from different Secret versions.
No restart or DataProduct change is required.

## Enable the workload

Add these settings to the controller chart's deployment-owned values file, replacing the examples
with the released digest, existing Secret, permitted consumer labels, and the source's IPv4 address:

```yaml
image:
  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
httpSource:
  enabled: true
  secretName: existing-export
  sourceCIDR: 192.0.2.10/32
  consumerPodLabels:
    app: trusted-consumer
  monitorPodLabels:
    app: trusted-monitor
```

The example digest and address are placeholders. Render for a non-default namespace before applying:

```bash
helm template data-product-controller ./charts/data-product-controller \
  --namespace products --values /path/to/private-values.yaml
```

The chart requires an immutable image digest and one IPv4 `/32` source address. Source egress is
limited to TCP 443 at that address plus TCP/UDP DNS to `kube-system` pods labeled `k8s-app: kube-dns`.
The HTTPS hostname must resolve to that address. Multi-address sources, alternate ports, IPv6, and
clusters with a different DNS topology require a separately reviewed deployment policy; the
reference chart does not widen access for them automatically.

The workload uses a dedicated service account without an API token or RBAC, runs as a non-root user,
drops capabilities, and has a read-only root filesystem. The source Secret uses a group-readable
volume owned by the pod's filesystem group. Removing the workload leaves the source and its Secret
intact.

The Service `<release>-http-source` exposes data on port 80. The separate management-only Service
`<release>-http-source-metrics` exposes port 8081 and publishes unready addresses, so outage metrics
remain reachable while data routing stays gated on readiness. An allowed consumer can request:

```bash
curl --fail http://data-product-controller-http-source.products.svc/api/data
curl --fail http://data-product-controller-http-source.products.svc/openapi.json
```

For a long Helm release name, the source resource prefix is truncated to keep names within 63
characters. `monitorPodLabels` is optional; omitting it permits no pod-to-management access through
this policy. Only the separate management port exposes probes and metrics.

## Health, limits, and recovery

| Endpoint or limit        | Behavior                                                                                                                                            |
|--------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `/healthz` on management | Process liveness; source failure does not restart the pod.                                                                                          |
| `/readyz` on management  | Executes a bounded GET and validates the JSON; reports `SourceReady`, `SourceUnavailable`, or `FeatureDisabled` without returning source data.      |
| `/metrics` on management | Prometheus counters by fixed operation/result, latest observed readiness, and observation timestamp.                                                |
| Source timeout           | Five seconds for connection, headers, and body together.                                                                                            |
| Query capacity           | Four concurrent source reads; excess requests receive 503 without being queued.                                                                     |
| Probe capacity           | One separate source read, independent of query saturation.                                                                                          |
| Response boundary        | Complete validated JSON only; no upstream cookies, redirects, authentication challenges, or other headers. Responses use `Cache-Control: no-store`. |

The chart probes readiness every 30 seconds with a seven-second timeout. Each probe reads the full
export, so include that traffic in the source's request budget. The connector performs no application
retries or caching. Source outages, TLS failures, invalid responses, missing credentials, and revoked
credentials fail closed; the next successful access restores observed readiness. The source owner
controls how long old credentials remain valid during rotation.

`http_source_requests_total` uses `operation="query"` or `operation="probe"` and fixed result values:
`success`, `disabled`, `configuration_error`, `upstream_error`, `invalid_response`, or `busy`.
`http_source_ready` is zero before the first observation and otherwise reflects the most recently
completed access or configuration check. A busy response does not change it. Use
`http_source_last_observation_timestamp_seconds` to distinguish recent evidence from an old sample;
metrics collection itself never contacts the source.

## Run and verify locally

```bash
go build -o /tmp/http-source ./cmd/http-source
HTTP_SOURCE_ENABLED=true /tmp/http-source \
  --listen-address 127.0.0.1:8080 \
  --management-address 127.0.0.1:8081 \
  --source-config /path/to/private-config.json
```

Unset or false `HTTP_SOURCE_ENABLED` keeps the API and contract unavailable and makes no source
requests. An invalid boolean stops startup. Both listeners close on SIGTERM, and active source reads
are cancelled. Bind local development listeners to loopback; the binary does not authenticate peers.

Run the repeatable HTTPS scenarios and chart boundary tests:

```bash
go test -race ./internal/httpsource ./cmd/http-source
sh scripts/http-source-chart.test.sh
```

These exercise fixed read-only access, trusted and untrusted TLS, denied writes, response limits,
timeouts, cancellation, proxy and redirect rejection, credential rotation, source failure recovery,
and readiness during query saturation.

## Controller integration and rollout

The connector does not write Kubernetes status or register a DataProduct. The controller's optional
[Deployment observer](connector-readiness.md) reports `ConnectorReady` and includes it in aggregate
readiness when an authored product declares `spec.connector`. Workload readiness reflects its probes;
public contract reachability remains separate work under
[#4](https://github.com/devantler-tech/data-product-controller/issues/4).

Platform owns production deployment, routing, and verification of signed released artifacts.
Local tests do not establish production access or adoption. The release gate remains until
[#46](https://github.com/devantler-tech/data-product-controller/issues/46) records real rollout,
rotation, recovery, and rollback evidence. [ADR 0003](adr/0003-read-only-http-source-connector.md)
records the workload and credential boundaries.
