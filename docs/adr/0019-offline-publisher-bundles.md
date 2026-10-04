# Offline publisher bundles

Status: Accepted

## Context

Publishers prepare related products in separate files and need actionable findings
before submitting them through GitOps. Review tools need an explicit saved report
contract without receiving private resource references or gaining runtime access.

## Decision

One offline validation engine checks every explicitly selected regular file under
shared byte, document, product, graph, CEL and context limits. Files retain separate
YAML boundaries and Kubernetes scalar semantics. Declarations cannot select further
files, fetch references or contact a cluster or provider.

The default v1 adapter retains its report contract. Explicit v2 reports contain
numeric source/document positions, sanitized schema paths, bounded graph witnesses,
diagnostic totals and per-product operational requirements. Only valid, complete
reports include public descriptor previews and a deterministic static dependency
plan. Imported status never supplies an observation, and raw declarations never
remain in a public report.

The closed v2 schema references the canonical descriptor schema through a locally
registered resource. Unresolved references are denied. A standard-library example
checks a bounded report/preview profile and its count, membership and dependency
relationships; full schema and API rules remain a separate validation requirement.

## Consequences

Publishers can review a complete local composition without enabling operational
features. Unresolved local references remain unverified. Static acceptance does
not establish live readiness, permission, query results or production adoption.
The existing publisher-preflight gate remains default off until independent
adoption and retirement acceptance are complete.
