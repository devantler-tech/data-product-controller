# Contract reachability

A healthy connector can still publish a broken contract link. Optional
`spec.contractChecks` make selected output contracts part of product readiness.
A separate `contract-probe` Deployment performs the network requests. The
controller only observes the Deployment through an exact-name, uncached GET.

The default-off OpenFeature `contract-readiness` flag controls controller
observation and probe execution. Flag retirement is tracked in [#101](https://github.com/devantler-tech/data-product-controller/issues/101).
Products without checks retain their existing behavior. A product that declares
checks while observation is disabled reports `ContractFeatureDisabled` and is
not Ready.

## Deploy and select a probe

Apply the release's updated CRD before upgrading an existing Helm installation;
Helm does not upgrade CRDs from its `crds/` directory. Enable observation using
`contractReadiness.enabled=true`, or `CONTRACT_READINESS_ENABLED=true` when
running the controller directly.

The chart can deploy one independent probe per release. For example:

```yaml
image:
  digest: sha256:<verified-image-digest>
contractReadiness:
  enabled: true
contractProbe:
  enabled: true
  url: https://export.example.com/openapi.json
  targetCIDR: 192.0.2.10/32 # replace with the contract server's actual IPv4 address
  monitorPodLabels:
    app: contract-monitor
```

Install into an explicit non-default namespace. The chart requires an immutable
image digest, HTTPS on port 443, one explicit IPv4 `/32` egress destination and
nonempty monitor labels. DNS is limited to the cluster DNS Pods. Select a stable
address or maintain the policy when the host changes. Contracts requiring broader
or different network routing need an independently operated probe and an explicit
operator-reviewed policy. Monitor selectors select Pods in the probe namespace.

The probe has no Kubernetes API token or credential volume. Its nonroot container
has a read-only filesystem, dropped capabilities, CPU/memory bounds, and separate
liveness and readiness endpoints. The management Service publishes unready
addresses so authorized monitors can inspect failures. No public route is created.

The Deployment name is `<release>-contract-probe` (with the chart's usual name
truncation). Add its reference to the product in the **same namespace**:

```yaml
spec:
  contractChecks:
    - output: query
      resourceRef:
        apiVersion: apps/v1
        kind: Deployment
        name: export-contract-probe
```

See the [complete product example](examples/contract-checked-product.yaml).
Grant the controller only this Deployment's `get` permission using the
[scoped Role and RoleBinding](examples/contract-observer-rbac.yaml), replacing
the controller ServiceAccount name/namespace and Deployment name for your install.
Enablement itself adds no workload or Secret permissions. Up to eight distinct
outputs can be checked, each with its own independently operated Deployment.

## Binding and readiness

Each referenced Deployment must contain a `contract-probe` container with command
`[/contract-probe]`, no arguments, literal `CONTRACT_PROBE_URL` equal to the output's
current `contractUrl`, literal `CONTRACT_READINESS_ENABLED=true`, and an HTTP
`/readyz` probe on container port 8081. Secret/ConfigMap-derived target values are
not accepted. Changing a product URL invalidates the old probe binding until its
Deployment configuration and rollout catch up.

The controller requires every desired replica to be updated, ready and available,
with an observed generation equal to the current Deployment generation. A missing,
deleting, scaled-to-zero, stale or partially rolled out Deployment fails closed.
All checks share a five-second Kubernetes observation deadline and are polled
every 30 seconds. Detection also includes kubelet readiness and Deployment status
propagation, so 30 seconds is not an end-to-end availability guarantee.

`ContractsReady` refreshes independently of connector, source and dependency
failures. Any failed selected check prevents aggregate `Ready`, which the registry
and downstream composed products consume. Stable reasons distinguish a disabled
feature, missing output, invalid selection, access denial, missing probe,
configuration mismatch, stale rollout and unhealthy probe. The condition reports
the first failed selection; the probe's management endpoint gives the network
failure reason. Removing all checks removes the condition. Deleting a product
does not delete or adopt its probes. Registry descriptors omit private probe refs.

Operators own the probe image, workload and honest readiness reporting. Matching
the configuration prevents stale/mistaken bindings; it is not attestation against
a malicious operator. Keep probes independent of the connector. In particular,
probing a connector's public route from that connector's own readiness probe can
create a cycle in which Kubernetes removes the endpoint needed for recovery.

## Probe results and limits

The binary reads `CONTRACT_PROBE_URL` once from operator configuration. It accepts
HTTPS URLs without embedded credentials, query strings or fragments, verifies
certificate chains and hostnames, disables ambient proxies, and refuses redirects.
There is no URL parameter, Kubernetes lookup or authentication support. Requests
are GET-only with identity encoding, a five-second deadline, one concurrent fetch,
a 16 KiB response-header limit and a one MiB response-body limit. Response bytes
are discarded and never copied to logs, status or metrics.

`/healthz` reports process health. `/readyz` returns 200 with `ContractReachable`
only for a complete, nonempty HTTP 200 response within those limits. Failures
return 503 with `FeatureDisabled`, `ContractConfigurationInvalid`,
`ContractUnavailable`, `ContractInvalidResponse`, or `ContractProbeBusy`.
Unsupported methods, query parameters and request bodies are rejected without
fetching the contract. `/metrics` does not trigger a fetch and exposes:

- `contract_probe_ready`: latest completed result, zero before the first check;
- `contract_probe_last_observation_timestamp_seconds`: freshness of that result;
- `contract_probe_requests_total{result}`: counters using only fixed reason labels.

A busy request does not replace the last completed result. Readiness evaluates
fresh network access; metrics describe the last completed observation.

Reachability is from the probe's network vantage point. It proves neither protocol
schema conformance nor universal internet availability, and it does not establish
data API health. Publish a public contract when using this credential-free probe;
authenticated contracts need a separately designed adapter. Platform owns
production activation and its route/policy acceptance. The required disposable
Kubernetes suite is a controlled integration proxy, not production rollout proof.

[ADR 0005](adr/0005-independent-contract-reachability.md) records the design.
