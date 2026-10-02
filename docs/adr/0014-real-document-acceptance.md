# ADR 0014: Real Document query and lifecycle acceptance

Status: Accepted

## Context

Operator status observation cannot establish that an application's declared credential returns
documents or rejects writes. Native Document acceptance needs a real operator, persistent
database and independently served query interface, with bounded failure and recovery checks.

## Decision

A dedicated required hosted job installs Percona Operator for MongoDB 1.23.0 and its supported
8.0.26-11 server in a disposable KSail Kubernetes 1.34 cluster. Packages and images are pinned by
checksum or immutable digest, and startup checks the amd64 CPU requirements. One unsharded
replica-set member is permitted only for this test through the operator's unsafe replica-count
option. Authentication, TLS certificate verification and persistent storage remain enabled.

An independent test publisher creates separate writer and reader password publications. Only
the writer seeds the synthetic record. A small independently built query workload consumes the
reader publication, verifies the database certificate and serves one fixed, bounded GET through
an HTTPS OpenAPI interface. The database client dependency is confined to that workload's
separate Go module; the controller continues to read only source and Secret metadata.

The job requires operator-written readiness, a working query and specific authorization-denial
errors for writes. A transport failure or bad password cannot stand in for a denied-write result.
It checks publication ownership, outage/recovery, password rotation, source recreation, current
publication rebinding, both controller gates and product deletion retaining external identities
and queryable data. No synthetic source status is written in this job.

The outer job deadline is 40 minutes. The harness allows 33 minutes for assertions and reserves
three minutes for cleanup; tool setup uses the remaining job budget. Per-phase waits use the
smaller remaining bound. Cleanup affects only the
run's generated cluster and storage. A timeout, failed assertion or failed cleanup is a failure.

## Consequences

The existing synthetic observer suite remains a fast control-plane regression test. The real
query job supplies the native Document row of the provider matrix; it does not prove Graph or
hybrid support, backup availability, certified platform support or production deployment.
Production activation and release-gate retirement retain their separate rollout criteria.

## References

- [Native Document observation](0012-native-document-observation.md)
- [Percona system requirements](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/System-Requirements.html)
- [Percona custom application users](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/app-users.html)
- [Percona pause and resume](https://docs.percona.com/percona-operator-for-mongodb/1.23.0/pause.html)
