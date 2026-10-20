# 0024: Retire the legacy UI declaration within v1alpha1

Date: 2026-10-17

Status: Accepted

## Context

[ADR 0023](0023-api-only-product-integration.md) made product integration API-only and kept the
legacy `spec.ui` declaration and its public descriptor twin as bounded compatibility metadata.
Their removal needs a versioned migration that a released controller can roll back across.

The delivered CRD serves one version, `data.devantler.tech/v1alpha1`, which is also the storage
version. `spec.ui` is optional there and in the released controllers' types. The public descriptor
schema `descriptor-v1.json` is closed, and its `ui` property is optional. Chart-authored product
declarations already omit `spec.ui`; hand-authored ones may still carry it.

The [admission checkpoint](../api-only-admission.md) proves, on owned Kubernetes 1.35.0 and 1.37.0
API servers, a rejecting rule at `spec.ui` that ratchets: new or changed UI is rejected, unchanged
legacy UI survives unrelated edits, and omission removes it while UIDs, other fields and status are
preserved. Helm installs a chart's CRD on first install only. It neither upgrades nor rolls back
that CRD, so every CRD change is applied explicitly and survives a Helm rollback.

## Decision

Keep `v1alpha1` as the only served and storage version, and retire the field inside it in two
stages. Each stage ships as a major release. No new API version and no conversion webhook are
introduced, and `status.storedVersions` stays `[v1alpha1]`.

**Stage 1 — stop new declarations**
([#443](https://github.com/devantler-tech/khoist/issues/443)). The delivered CRD
gains the checkpoint's ratcheting rule and keeps the typed property. Creating a product with `spec.ui`, adding it or changing it is rejected
with an actionable message. An unchanged legacy declaration remains valid and can be removed.
There is no conversion in either direction: stored objects are untouched, and the rule only
narrows writes. A rollback to the previous released controller reads the same objects, because
Helm leaves the newer CRD in place and `spec.ui` is optional in that controller's types.
Explicitly applying the previous release's CRD is the full rollback and allows UI writes again.
Helm never upgrades a CRD from `crds/`, so a plain `helm upgrade` leaves the ratcheting rule out of
the cluster. The Stage 1 release notes therefore name the explicit CRD apply step, and the stage
takes effect only after it.

**Stage 2 — remove the field.** Once the preconditions below hold, the `ui` property and its types
leave `v1alpha1`, and every generated copy is regenerated. The API server then drops an unknown
`spec.ui` from writes: clients using strict field validation, such as `kubectl`, are rejected, and
others receive a warning. This is the in-place removal option. It is harmless here because
Stage 2 starts only when no stored object carries the field, so nothing a rollback needs is
discarded. A rollback to the last Stage 1 release reads every object written after the removal,
since those objects carry no `ui` and the field is optional in that release. Explicitly applying
that release's CRD restores the typed property and no stored data reappears or is lost.

**Descriptor twin.** Stopping the registry from emitting `ui` is compatible with every reader of
`descriptor-v1.json`, because the property is optional. The v1 schema keeps that optional property
so previously exported descriptors stay valid, and its description states that the registry no
longer emits it. Removing the property from the schema itself would invalidate those exports, so it
requires a new descriptor version and is not part of this migration. The offline publisher report
formats are unchanged.

## Preconditions for removing the field

Slice D ([#439](https://github.com/devantler-tech/khoist/issues/439)) merges only
after each of these is verified and recorded on its issue:

1. The Stage 1 release is installed in every consuming cluster, shown by reading the live CRD and
    finding the rejecting rule at `spec.ui`.
2. No stored declaration remains. A complete read of every `DataProduct` in all namespaces of each
    consuming cluster finds no object with `spec.ui`, and the deployed GitOps revision declares
    none. A failed, partial or filtered read is not a zero.
3. The compatibility kit session runtime
    ([#437](https://github.com/devantler-tech/khoist/issues/437)) and the
    registry UI surfaces ([#438](https://github.com/devantler-tech/khoist/issues/438))
    are removed in a release those clusters run, so nothing reads the field.
4. Disposable acceptance installs the last signed Stage 1 release, upgrades to the candidate,
    performs a real Helm rollback and restores the candidate. Product UIDs, non-UI specifications,
    status and registry readiness are preserved at every step.
5. The admission checkpoint passes against the new schema on Kubernetes 1.35.0 and 1.37.0. An
    object written before the removal keeps its UID, specification and status, and a write carrying
    `spec.ui` does not persist it.
6. The release is marked as a major release, and its notes name the explicit CRD apply step.

If the second precondition is never met, the field stays as inert ratcheted metadata. Removal
waits rather than discarding a declaration.

## Rejected alternatives

- **Serve a new version without `spec.ui`.** `None` conversion can serve both schemas, but it
  can prune `spec.ui` from objects handled through the new version, so keeping the field across
  round trips needs a conversion webhook. That adds a TLS service to the path of every request
  that needs version conversion, with its own certificate rotation and availability, a storage migration
  that rewrites every object before the old version can be dropped, and an annotation to carry the
  field through lossy round trips. Every GitOps declaration would also change its `apiVersion`.
  That machinery would protect a field that, by the time it is removed, holds no stored data.
- **Drop the field in place now.** It would silently discard live declarations that a rollback to a
  UI-capable controller still reads. The same removal is safe only after the second precondition.
- **Keep the field indefinitely.** It leaves a deprecated surface in the served contract with no
  remaining reader once the kit and registry surfaces are gone.

## Consequences

- Publishers that still write `spec.ui` receive admission errors from Stage 1 and must remove it.
- Each stage rolls back to the previous signed release. Because each CRD change only narrows what
  can be written, older controllers keep reading every stored object.
- The migration adds no conversion webhook or storage-version migration to operate.
- Production adoption, inventory evidence and the consumer switch remain Platform's
  ([platform#4890](https://github.com/devantler-tech/platform/issues/4890)); source merge alone
  does not establish them.
