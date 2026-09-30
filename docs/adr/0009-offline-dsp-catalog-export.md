# 0009: Offline Dataspace Protocol catalog export

Date: 2026-09-30

Status: Accepted

## Context

DCAT discovery alone does not establish DSP offers or transfer capabilities. A
query URL is not a negotiation service, a media type is not a transfer format,
and product ownership does not establish an ODRL assigner. Publishing invented
values would make a catalog misleading to independent consumers.

## Decision

Ship a standalone `dsp-catalog` command in the release image, behind the
default-off OpenFeature `dsp-catalog-export` flag. It accepts local regular files:
a snapshot of this controller's DCAT profile and versioned provider bindings.
It has no listener, Kubernetes client, network loader, credentials or connector
management API. All validation precedes output; a failed export writes no JSON.

Bindings explicitly select dataset and source distribution IDs. The provider
supplies a separate DSP catalog ID, participant ID, connector service ID and
HTTPS base address, distinct DSP distribution IDs, transfer-format IRIs and
target-free ODRL Offers. Dataset IDs are preserved. Every offer's explicit
assigner must match the participant. Rules are preserved, never inferred or
weakened. Unknown constructions fail instead of being silently discarded.

The initial policy profile supports permissions, prohibitions and obligations
with an explicit action, plus atomic string-valued constraints. Compound
constraints, duties, assignees, custom contexts and other extensions require a
future version; they are rejected. Only `use` may abbreviate an action; other
actions and left operands use full vocabulary IRIs. This deliberately bounded
subset avoids silently losing conditions during JSON-LD expansion.

The output uses the DSP 2025-1 context and the 2025-1-err2 catalog model.
An empty selection omits `dataset` and retains the required DataService. Only
selected datasets and outputs appear; stale bindings fail. Source descriptive
title, description and version are preserved. Source query services and owner
metadata do not become DSP service or policy claims.

Both inputs are strict JSON with duplicate-key, unknown-field, nested-context,
UTF-8, depth and size checks. Inputs are bounded to 2 MiB catalog / 1 MiB bindings,
256 datasets, 1,024 source and selected distributions, 16 KiB strings, 32 nesting
levels, 16 offers per dataset, 32 rules per rule category and 16 constraints per
rule. Encoded output is capped at 2 MiB, including its newline.

## Consequences

The compatibility claim covers this catalog data-model projection only. It does
not assert full DSP protocol conformance, offer enforcement, service reachability,
authorization, successful negotiation or data transfer. Providers must own those
capabilities and review their public binding files. No generic import or connector
administration API is assumed. Production controller behavior is unchanged.

Offline tests use unmodified pinned official schemas and contexts, an independent
JSON Schema validator and JSON-LD expansion. Documentation includes runnable
examples and [adoption/flag-retirement issue #126](https://github.com/devantler-tech/data-product-controller/issues/126), reviewed on 2026-10-30.

## References

- [DSP 2025-1-err2](https://eclipse-dataspace-protocol-base.github.io/DataspaceProtocol/2025-1-err2/)
- [ODRL Offer](https://www.w3.org/TR/odrl-model/#policy-offer)
- [Implementation issue #125](https://github.com/devantler-tech/data-product-controller/issues/125)
