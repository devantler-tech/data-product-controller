# 0004: Observe connector Deployments without owning their lifecycle

Status: Accepted

## Context

A published connector has its own deployment, probes, source credentials, and network boundary. Product readiness must account for that workload without making the control plane a data-plane client or a workload provisioner.

## Decision

`spec.connector` selects the `deployment/v1` observation contract and a named `apps/v1` Deployment in the DataProduct's namespace. The reference's namespace must be omitted or empty; a nonempty namespace is rejected. This is independent of `spec.source`, so a product may require both provisioned-source and connector readiness.

The versioned observer performs one uncached Kubernetes GET with a five-second deadline. Operators
grant `get` on exact Deployment names using namespaced Roles; the controller's default ClusterRole
gains no workload or Secret permissions. A Deployment response includes its spec and status. The
observer inspects identity, desired replicas, and readiness status only, and never exports the
template, source records, credentials, or provider error messages.

Readiness requires a non-deleting Deployment with a positive desired replica count (an omitted
count means one), a positive generation matched exactly by `status.observedGeneration`, and
updated, total, ready, and available replicas all equal to the desired count, with zero unavailable
replicas. This intentionally requires full current-generation availability, including rollout
completion. A Deployment's minimum-availability or historical progress condition alone is
insufficient. Kubernetes readiness remains an observation, not a guarantee that an endpoint is
reachable.

The controller publishes `ConnectorReady` independently of provisioned-source and composition
failures, and aggregate `Ready` requires every declared capability. Existing source and dependency
failures retain their aggregate reason precedence. Removing the connector reference removes its
condition. Connector observations refresh every 30 seconds; each read uses current API state, and
unchanged observations do not rewrite status. Kubernetes control-plane outages or an inactive
controller can leave last-observed status stale, so consumers must not interpret the condition as a
liveness lease or use its transition time as an observation timestamp.

The `connector-readiness` OpenFeature release flag is default-off. A declared connector with the flag disabled is unready and causes no workload reads. Products without a connector preserve their existing behavior. Production rollout and release-flag retirement require separate acceptance evidence.

## Consequences

Deployment ownership, deletion, probes, credentials, public routing, and network policy stay with the product operator. Source-health meaning depends on those probes. The controller does not query public contracts, register a product automatically, create workloads, or grant connectors Kubernetes access. Other workload kinds require another explicit observation contract.

The complete-rollout rule follows the [Kubernetes Deployment
model](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#complete-deployment).
Namespace selection is deliberately implicit; [CRD
validation](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules)
cannot compare arbitrary reference namespaces with `metadata.namespace` using root CEL metadata
access.
