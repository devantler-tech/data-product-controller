# ADR 0021: Public KSail release data product

## Status

Accepted.

## Context

The catalogue needs a useful live portfolio dataset alongside its fixed reference data.
KSail already publishes release metadata through a public GitHub API. Republishing private
credentials or introducing a data-plane reader into the controller is unnecessary for this source.

## Decision

The independent reference-product workload optionally serves a KSail release browser and a
publisher-authored OpenAPI contract. Its browser reads exactly the public KSail releases endpoint
without credentials, redirects or caller-selected destinations. The process and controller do not
read, proxy or retain release records.

The view reads one default API page with a ten-second deadline, 1-MiB streamed UTF-8 budget,
30-record bound and validated display fields. Version and channel filters work on that page.
Refresh, navigation and failed reads withdraw older results. No release data enters a host message,
DataProduct declaration or status.

The surface shares the reference workload's publisher-approved presentation protocol and theme.
It retains the opaque iframe origin and limits production connect policy to its own configuration
endpoint and the fixed public API path. Hosted metadata advertises the actual external query
endpoint and the independently served contract and UI.

The default-off `ksail-releases` OpenFeature release gate controls surface and asset publication.
The chart exposes an optional publication setting. Normal signed Platform GitOps owns activation
and source-adoption evidence; flag retirement requires a separate reviewed acceptance decision.

## Consequences

Consumers can inspect real portfolio versions without a new workload, credentials or source
access grant. GitHub controls the dataset, default-page ordering, availability and anonymous
request allowance. The UI represents those limits explicitly and makes no background polls.
Workload readiness is not proof of GitHub availability; each browser read reports its own outcome.

This public source does not validate the private credential-backed HTTP connector, external
database providers, or their rollout gates.
