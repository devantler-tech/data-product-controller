# Offline publisher report review

Status: Accepted

## Context

Publishers need to inspect saved bundle reports, locate findings, compare deployment
requirements, and follow named dependencies before publishing a product. Public previews
describe declarations and carry no observed readiness.

## Decision

The independent UI host serves an offline workspace at /publisher-review behind the
existing default-off publisher-preflight gate. It requires no product UI permission,
Kubernetes client, network connection, or application framework.

The browser accepts only bounded v2 reports. It checks duplicate decoded keys, integral
numbers, closed shapes, source positions, retained diagnostic counts, feature-set unions,
zero-generation previews, and exhaustive producer-first edges. It reuses the inert public
descriptor validator. Inspection does not replay CRD admission, original bundle validation,
compatibility evaluation, runtime feature settings, or production readiness.

All report text is rendered as text nodes. Declared links remain inert. The page prohibits
connections and frames, exposes no upload endpoint, and withdraws previous results before
replacement reads. Clear, rejection and page exit revoke downloads; late file reads cannot
restore results.

## Consequences

Complete reports support searchable product previews, provenance-based requirements,
dependency navigation and bounded descriptor downloads. Incomplete reports retain global
findings and omissions, show numeric locations and witnesses, and expose no fabricated product
identities or execution plan. Filtering does not alter the report outcome.

System, Light and Dark themes use orange accents with keyboard focus and a narrow-screen
layout. Storage failure leaves theme controls usable. Actual independent publisher adoption
and gate retirement remain separate outcomes in issue #205.
