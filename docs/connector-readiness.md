# Observe connector workload readiness

`spec.connector` makes a DataProduct wait for its independently operated connector Deployment. It uses the `deployment/v1` observation contract, supports only `apps/v1` Deployments, and always reads in the product's namespace. Omit the reference namespace; a nonempty value is rejected by the CRD and observer.

## Enable and authorize observation

The `connector-readiness` OpenFeature release flag defaults off. Enable it with Helm's `--set connectorReadiness.enabled=true` or `CONNECTOR_READINESS_ENABLED=true` for the manager process. A product with a connector reference stays unready while the flag is disabled, and the controller makes no connector reads. Products without that reference retain their existing behavior.

Upgrade the CRD before the controller when upgrading an existing installation, following the [Helm upgrade procedure](../README.md#install). Helm does not update CRDs during chart upgrades. Keep artifacts pinned to the released immutable digests.

The chart and generated default RBAC grant no Deployment or Secret access for this feature. Operators supply a namespaced Role with only `get` on each authorized Deployment name. The [scoped RBAC example](examples/connector-observer-rbac.yaml) grants access to `products/export-http-source` to the controller ServiceAccount `data-product-system/data-product-controller`. Adapt those names to the deployment; enablement alone does not authorize access. No `list`, `watch`, write, or Secret permission is required.

A Kubernetes Deployment GET returns the spec and status, including its template. The observer examines only identity, desired replica count, and readiness status; it never publishes the template or underlying API errors. Keep credentials in workload-mounted Secrets, and treat workload-read grants as access to the Deployment document.

## Publish a product

The [authored product example](examples/http-source-product.yaml) references `products/export-http-source` and publishes its named `query` output through ordinary HTTPS `url` and `contractUrl` fields. The operator creates the Deployment, configures its probes, and owns the HTTPS routing and network policies. Replace the example URLs with those routes. The controller does not probe them or register a DataProduct automatically.

The [HTTP source guide](http-source.md) defines the export workload's own Secret, TLS, timeout, and access boundaries. Its `http-source` release flag is independent of controller observation. Enabling connector readiness neither deploys nor enables that workload.

## Conditions and recovery

The controller reads the current Deployment every 30 seconds with a five-second read deadline. `ConnectorReady=True` requires a non-deleting workload, a positive desired replica count, and a positive generation matched exactly by `status.observedGeneration`. An omitted desired count means one. Updated, total, ready, and available replicas must each equal the desired count, and unavailable replicas must be zero. This requires full capacity and completion of the current rollout; minimum availability alone is insufficient.

| Reason                     | Operator action                                                       |
|----------------------------|-----------------------------------------------------------------------|
| `ConnectorFeatureDisabled` | Enable the observation release flag.                                  |
| `ConnectorInvalid`         | Use the supported adapter, API, kind, and same-namespace name.        |
| `ConnectorNotFound`        | Create or correct the referenced Deployment.                          |
| `ConnectorDeleting`        | Restore the workload or select its replacement.                       |
| `ConnectorScaledToZero`    | Scale the connector to at least one replica.                          |
| `ConnectorStatusStale`     | Wait for the Deployment controller to observe the current generation. |
| `ConnectorNotReady`        | Inspect the rollout, replica availability, and probes.                |
| `ConnectorAccessDenied`    | Grant exact-name Deployment get access in the product namespace.      |
| `ConnectorUnavailable`     | Check Kubernetes API availability and controller configuration.       |

Aggregate `Ready` also requires every declared provisioned source and product input. Source and input failures keep their aggregate reason precedence; `ConnectorReady` still refreshes independently. The registry's existing `ready` and `readiness` fields reflect aggregate readiness, including a connector failure when no earlier prerequisite blocks the product. Workload probes determine how source failures affect Deployment availability. Public contract reachability and source data correctness require separate evidence.

When the controller and Kubernetes API are available, polling observes recovery on the next reconciliation. Kubernetes reconciliation, workqueue delays, and probe timing add latency. Status is the last observation, not a liveness lease: a stopped controller can leave it stale. `lastTransitionTime` changes only with condition state and must not be treated as a last-check timestamp. Identical observations do not rewrite status.

Removing `spec.connector` removes its condition and leaves the workload alone. Deleting a DataProduct does not delete or adopt the Deployment, source, credentials, or routes. Release rollout and gate retirement are tracked in [#49](https://github.com/devantler-tech/data-product-controller/issues/49); these semantics are recorded in [ADR 0004](adr/0004-connector-workload-observation.md).
