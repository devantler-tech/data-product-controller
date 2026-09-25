# 0006: Bounded composition and declared contract compatibility

Date: 2026-09-25

Status: Accepted

## Context

Consumers select named outputs from independently operated products. A Ready producer alone does not establish that its contract still satisfies a consumer, and cyclic references cannot become healthy through reconciliation ordering.

## Decision

The default-off `composition` OpenFeature flag enables graph checks and direct-input lineage. An optional input `contract` declares a protocol and minimum stable product contract version. Versions at or above that minimum within the same major version are compatible; major-zero versions must match exactly. Pre-release and build-metadata versions do not satisfy this stable contract. Compatibility is the publisher's semantic-version declaration, not a comparison of remote schemas. The controller never downloads contracts or product data.

Existing inputs without a requirement retain their named-output contract. Inputs with a requirement fail closed while composition is disabled. With composition enabled, the controller traverses dependencies before trusting their Ready conditions, detecting cycles even when every participant is unready. Each reconciliation observes at most 256 products, 1,024 input edges and 64 levels under a shared five-second deadline. Exceeding any bound reports a stable failure and retries after 30 seconds; it never publishes an incomplete graph as healthy.

The controller records each direct input's producer identity, generation, contract version, owner, selected output and observation result. The registry exposes these edges only when their composition condition matches the consumer's current generation. Clients can follow the named edges through the existing descriptor collection. This is observed declarative lineage, not record-level provenance or data movement. Removed inputs and disabled observation clear old lineage.

Changes to a producer enqueue its transitive consumers as well as direct consumers. Status writes occur only when observations change. Independent connector and contract-reachability conditions still refresh when composition is blocked.

## Consequences

Authors can diagnose missing products, missing ports, incompatible contracts and cycles without inspecting controller logs. The API remains metadata-only and adds no Kubernetes permissions. Large graphs fail explicitly instead of creating unbounded traversal. Readiness and lineage describe an eventually consistent observation; producer generations are included so clients can identify the revision observed. The registry does not claim an atomic snapshot across products.

Production adoption and flag retirement require separate rollout evidence.

Recorded lineage is limited to 64 KiB of encoded JSON. Oversized producer metadata clears lineage and reports `CompositionLimitExceeded` rather than amplifying status into an API write that cannot succeed.
