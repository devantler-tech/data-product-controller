# 0005: Independent contract reachability

Status: Accepted
Date: 2026-09-24

## Context

Connector availability does not establish whether a consumer can retrieve an output's published
contract. Fetching arbitrary descriptor URLs in the controller would give untrusted metadata the
controller's network privileges. Probing a connector's own public route from its readiness probe
would also create a routing/readiness cycle.

## Decision

An independently operated, credential-free `contract-probe` Deployment fetches one
operator-configured HTTPS URL. It verifies TLS, rejects redirects and ambient proxies, bounds
response size, elapsed time and concurrency, and exposes only fixed readiness reasons and metrics.
It never publishes response bytes or reads Kubernetes resources.

Optional `spec.contractChecks` select named outputs and same-namespace Deployments. The controller
reads those Deployments by exact name through its uncached reader under operator-granted RBAC. It
requires full current-generation availability and verifies the named `contract-probe` container has
a literal URL matching the selected output, the enabled flag, and its HTTP readiness probe. It never
fetches contract URLs, reads probe credentials, or owns these workloads. All selected checks
contribute to an independent `ContractsReady` condition and aggregate product readiness; an empty
selection imposes no contract check.

Operators own probe images, configuration, network policy, trust roots and truthful readiness
reporting, just as they own connector readiness. URL matching prevents accidental reuse of a healthy
check for an old contract; it is not workload attestation. Public descriptors omit probe references.

The default-off OpenFeature `contract-readiness` release flag controls both observation and probe
execution. Retirement is tracked in #101. The Helm chart provides one optional probe per release;
other outputs can reference separately operated probes. There are at most eight checks per product,
polled every 30 seconds with a shared five-second observation deadline.

## Consequences

Reachability means a complete, nonempty HTTP 200 response of at most one MiB over verified TLS from
the probe's network vantage point. It does not prove protocol conformance, data availability, or
reachability from every public network. Authenticated contracts remain outside this credential-free
increment. Independent probes avoid removing the connector's serving endpoints when contract
publication fails. Production rollout remains owned by Platform.
