# Consume live KSail releases

The standard query output is the fixed public GitHub release API. Consumer
applications own its presentation and transport. The API-only registry displays
that output and its OpenAPI contract without loading the reference page or
querying GitHub. Standalone reference pages and the independent embedding kit
remain deprecated compatibility; see
[ADR 0023](adr/0023-api-only-product-integration.md).

The optional KSail release product shows published versions, UTC publication times and release
channels from the existing public GitHub API. Version and channel filters use the loaded page.
**Refresh releases** reads GitHub again. The view contains live metadata, rather than fixed sample
observations.

The independent reference-product workload serves the UI and its OpenAPI contract. The browser
reads exactly `https://api.github.com/repos/devantler-tech/ksail/releases` with credentials omitted,
no referrer and redirects rejected. Neither that workload nor the controller reads or proxies
release records. Records never enter DataProduct spec/status or parent messages.

## Publish the product

Opt in using deployment-owned chart values:

```yaml
demoProduct:
  enabled: true
  publicBaseURL: https://harbour.example.test
  ksailReleases:
    enabled: true
```

An enabled shared chart route can supply the public base URL instead. The chart publishes a
`ksail-releases` DataProduct with the actual external query endpoint, the independent
`/ksail-releases/openapi.json` contract, with no `spec.ui` member. Its identity is
`https://github.com/devantler-tech/ksail/releases`; its `v1.0.0` describes the product contract,
not the current KSail tool version.

No source Secret, Kubernetes API token, extra workload or workload source-egress permission is
required. Public interface metadata grants no additional access. Removing chart-authored
UI metadata is a breaking publication change; consumers
select the named API output and contract. The direct reference page remains
available. The chart's `uiContract` and `uiAppearance` settings still control the
retained compatibility runtime, independently of catalogue publication;
light/dark display preserves filters and does not request source data.

The workload always serves `/ksail-releases/ui`, `/ksail-releases/openapi.json`,
`/ksail-releases/ui-contract-config` and its route-specific assets. The temporary
`KSAIL_RELEASES_ENABLED` / `ksail-releases` runtime gate has been removed after live acceptance.
The chart no longer emits that environment setting; remove it from hand-authored deployments
when adopting this breaking release. The remaining chart publication setting is independent:
false or unset removes the optional catalogue entry but leaves direct interfaces available.

To withdraw the direct surface, restore a previously verified release with the gate through
normal GitOps. Platform must adopt a compatible artifact-bound rollout verifier before upgrading;
its legacy verifier still expects the removed environment setting. See
[ADR 0022](adr/0022-standard-ksail-release-interface.md) and acceptance/consumer adoption in
[#373](https://github.com/devantler-tech/data-product-controller/issues/373).

## Request and failure boundaries

The view reads one default API page, up to 30 releases, with a ten-second request deadline and
1-MiB streamed decoded-byte limit. It validates complete UTF-8 JSON and displayed fields before
rendering any row. Empty, malformed, oversized, redirected or unavailable responses receive distinct
empty/failure treatment; an older page never substitutes for a failed refresh.

GitHub controls availability, default-page ordering and anonymous request allowance. Each initial
view and explicit refresh makes one source request; filters, theme changes and idle viewing make
none. There are no background polls or automatic retries. A source limit appears as an unavailable
read with an explicit retry action. Workload readiness means the interface is serving, not that
GitHub currently responds.

Navigation aborts pending reads and clears the retained page. Persisted history restoration rereads
the public configuration and source; responses from an earlier refresh or page lifetime cannot
replace the current view. If the deprecated kit is used, its opaque iframe sandbox
and exact publisher-origin/session checks remain required. That host receives
bounded status and resize hints, never release records. The registry grants no
presentation capabilities and runs no product interface.

Platform owns immutable signed adoption through normal GitOps. This public source does not
establish the private credential-backed HTTP connector's rollout or authorization criteria.

Run the focused controls:

```bash
go test ./internal/demoproduct ./internal/config
go test -tags=browser ./internal/browser -run TestKSailReleaseBrowser
sh scripts/ksail-releases-chart.test.sh
```
