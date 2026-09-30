# ADR 0010: Versioned engine-provider observation

- Status: Accepted
- Date: 2026-09-30

## Context

The multi-engine roadmap needs a stable interface for SQL, document and graph providers. Product
authors must identify supported engine strategies without putting storage configuration or
credentials into the product descriptor. ADR 0002 establishes external source lifecycle ownership.

## Decision

Keep delegated ownership. `internal/provider/v1.Provider` observes creation, readiness and connection
publication; it never creates, adopts, modifies or deletes resources. The versioned registry selects
one implementation from the declared engine type, provider and adapter. Reconciliation depends on
this interface and contains no database-specific logic.

An optional `spec.source.engine` uses `engine-provider/v1`. Kubernetes CEL admission and the runtime
resolver enforce the same supported matrix. Untyped `crossplane/v1` references preserve ADR 0002.
`sql/native` selects `cnpg/v1`, references a same-namespace CloudNativePG Cluster and requires its
generated application Secret. Other combinations are rejected until their adapters are implemented.

The CNPG adapter observes readiness conditions, requested/observed/ready instance counts and primary
agreement. Explicit observed generations must be current; absent generations cannot establish spec
freshness. Uncached metadata-only Secret requests verify publication ownership against the current
Cluster UID. Custom bootstrap credentials require a separate publication contract. Neither provider
messages nor credential values are copied to product status or the registry.

Typed `SourceReady` refreshes independently of composition and other dependencies. Each source
observation has a five-second deadline and a 30-second poll. The separate `engine-providers`
OpenFeature gate is default-off alongside `provisioned-sources`; both disabled states prevent reads.
Its rollout and retirement are tracked in #128.

## Consequences

- #3 remains the delivered, foundational provisioner-reference contract; it is not superseded by
  in-controller database provisioning. Storage sizing, creation, backups, rotation and deletion
  stay with external operators. DataProduct deletion retains sources and connection publications.
- #34 supplies dispatch and admission for the first supported engine. #35–#37 add additional
  observation adapters and explicit supported combinations, without altering reconciliation.
- #38 proves real operator behavior and engine capabilities. Synthetic Kubernetes acceptance
  proves the controller's admission and observation paths, not database authentication or queries.
- Operators explicitly install and authorize providers. The chart adds no database or Secret grants.
- A declaration of document or graph capability needs its own provider/extension evidence; SQL
  Cluster readiness cannot establish JSONB or AGE support.
