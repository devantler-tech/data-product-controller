# ADR 0002: Delegated provisioned sources

- Status: Accepted
- Date: 2026-09-05

## Context

Product authors need to connect product readiness to a newly provisioned source without copying
database schemas, credentials, or cloud lifecycle code into the data-product controller. Provisioning
operators already own resource creation, upgrades, connection publication, and deletion.

## Decision

An optional `spec.source` references one provisioner resource and one connection Secret in the
DataProduct's namespace. The versioned observation interface is read-only. The `crossplane/v1`
adapter requires `Ready=True` and `Synced=True`, a matching `spec.writeConnectionSecretToRef`, and
a published Secret owned by the current source UID. Explicit observed generations must match the
resource generation; absent generations follow Crossplane's condition contract.

The controller uses uncached reads and requests `PartialObjectMetadata` for Secrets. It never
requests Secret values, copies provider messages into public status, writes external resources, or
adds owner references or finalizers. Installation RBAC permits only explicitly selected resources
and Secret names. Metadata-only requests do not imply metadata-only Kubernetes permissions.

Resources are discovered through their API group, version, and kind. Cluster-scoped references are
rejected before resource reads. Readiness is refreshed every 30 seconds, including failure states,
without adding cluster-wide watches or rewriting unchanged status.

Creation, storage sizing, backup, credential rotation, and deletion remain with the provisioner.
Deleting a DataProduct retains its source. The `provisioned-sources` OpenFeature release flag is
default-off; removal is tracked by [#41](https://github.com/devantler-tech/data-product-controller/issues/41).

## Consequences

- [#3](https://github.com/devantler-tech/data-product-controller/issues/3) remains the foundational
  provisioner-reference contract.
- The engine choices in [#33](https://github.com/devantler-tech/data-product-controller/issues/33)
  can use external provisioners and additional versioned adapters. The engine dispatch and admission
  matrix in [#34](https://github.com/devantler-tech/data-product-controller/issues/34) remains separate work.
- A successful observation proves control-plane readiness and publication ownership. It does not
  authenticate a database connection, validate Secret contents, or prove query availability.
- Operators explicitly install and authorize each provisioner. A missing API or permission produces
  an actionable unready condition without broadening controller access.
