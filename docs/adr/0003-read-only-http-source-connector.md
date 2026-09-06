# ADR 0003: Read-only HTTP source connector

- Status: Accepted
- Date: 2026-09-06

## Context

An existing source can expose a governed JSON export without giving consumers its connection
details. The controller owns metadata and must not become a query proxy or receive source
credentials. Product authors need a reference workload with an explicit access boundary and
observable failure recovery.

## Decision

The `http-source` binary is an independently deployable data-plane workload. It serves one fixed
`GET /api/data` operation and an OpenAPI 3.1 document. A projected Secret contains one JSON file
with an HTTPS endpoint and a read-only bearer credential. Each access reopens that file, preserving
the endpoint and credential as a pair during rotation.

Consumers cannot select a destination, append query parameters, submit a request body, or execute
writes. The connector verifies TLS, refuses redirects and ambient proxies, forwards no caller
credentials or cookies, and publishes no upstream response headers. A successful response is one
complete JSON value of at most 1 MiB, buffered and validated before publication. Requests have a
five-second deadline; four queries and one readiness probe may contact sources concurrently.

The source owner grants an explicitly read-only credential scoped to the intended export.
HTTP GET alone does not enforce source-side authorization. Every peer permitted by the connector's
ingress policy receives the authority to read that export. The chart creates an internal service
and requires explicit consumer labels and source egress CIDR configuration. It creates neither a
public route nor a DataProduct registration.

Management endpoints use a separate listener and a management-only Service that remains reachable
when the pod is unready. Data routing remains readiness-gated. Liveness checks only the process; readiness executes
a bounded source read and discards the data. Probe capacity is separate from query capacity to avoid
evicting healthy pods under query saturation. Prometheus metrics expose fixed operation and result
labels, last observed readiness, and observation time. Errors, probes, contracts, and metrics omit
connection details, credentials, and source response bodies.

The OpenFeature `http-source` release flag defaults off. The optional chart workload explicitly
enables it. Rollout and retirement are tracked in [#46](https://github.com/devantler-tech/data-product-controller/issues/46).

## Consequences

- Existing HTTPS JSON exports can be queried through a small, documented contract without putting
  source credentials or data in Kubernetes custom resources.
- Data selection and authorization belong to the source's governed export. This connector supports
  neither arbitrary queries nor transformations, writes, caching, or general HTTP forwarding.
- NetworkPolicy enforcement and any wider ingress authorization belong to the deployment. Policies
  are additive; another policy selecting the connector can broaden its access.
- Readiness adds one full export read per probe. The deployment must choose its probe period to fit
  the source's request budget. The chart uses a 30-second period and a seven-second timeout.
- DataProduct health observation, public contract reachability, and registration remain separate
  work under [#4](https://github.com/devantler-tech/data-product-controller/issues/4). A healthy local
  probe does not prove that an external consumer can reach a public contract.

## References

- [Go HTTP client behavior](https://pkg.go.dev/net/http#Client)
- [Kubernetes Secret volume updates](https://kubernetes.io/docs/concepts/configuration/secret/)
- [Kubernetes NetworkPolicy behavior](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
- [Prometheus metric naming](https://prometheus.io/docs/practices/naming/)
