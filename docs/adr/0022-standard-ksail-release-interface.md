# ADR 0022: Standard KSail release interface

## Status

Accepted for the reference workload. Supersedes ADR 0021's temporary release-gate decision.

## Context

The first public source has completed normal Platform adoption with signed artifacts and real
browser reads, local filters, explicit refresh, appearance changes and fresh history restoration.
The failure and cancellation controls also passed against the deployed source revision.
Acceptance evidence is recorded in [#373](https://github.com/devantler-tech/kweft/issues/373).
Keeping a first-use switch adds configuration without an ongoing operational requirement.

## Decision

The reference workload always serves its fixed KSail release UI, contract, configuration and
route-specific asset. Remove the `ksail-releases` OpenFeature flag, its
`KSAIL_RELEASES_ENABLED` parser, handler option and chart environment wiring. Do not replace it
with another runtime switch.

`demoProduct.ksailReleases.enabled` remains a default-off choice to publish the optional
DataProduct resource. Disabling publication removes the catalogue entry; it does not withdraw
the workload's direct interfaces. The chart still requires an independent workload and a public
base URL before publishing that entry.

Keep the fixed credential-free GitHub source, streamed UTF-8/size/record/time bounds, redirect
rejection, plain-text projection, local filters, cancellation and history rules unchanged.
Publisher-approved presentation gates, opaque sandboxing and exact host/session checks remain
separate. Neither the controller nor workload reads or proxies source records.

## Consequences

This changes the documented withdrawal behavior and requires a breaking release. Existing
deployments remove the obsolete environment setting when adopting the compatible chart.
To withdraw direct interfaces, restore a previously verified release that still has the gate
through normal GitOps; unlisting the optional product alone is insufficient.

Platform's rollout verifier currently checks the legacy gate. It must gain a reviewed,
artifact-bound retired-gate profile before adopting the new release. Source issue
[#385](https://github.com/devantler-tech/kweft/issues/385) covers this repository;
parent #373 stays open until compatible Platform adoption and live readback complete.

GitHub availability and anonymous allowance remain external. Public-source acceptance establishes
no private HTTP connector or database-provider adoption.
