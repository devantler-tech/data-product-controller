# ADR 0016: Owned PostgreSQL image with Apache AGE

- Status: Accepted
- Date: 2026-10-02

## Context

Hybrid Graph sources require a PostgreSQL extension that is loaded and queryable. A mutable external
image cannot establish the artifact, executable or signing contract needed by CloudNativePG.
The controller remains a metadata observer and does not install extensions or hold database credentials.

## Decision

Build Apache AGE 1.7.0 for PostgreSQL 17 from the Apache release archive. The build checks the fixed
SHA-512 checksum and detached release signature against the reviewed release public key. Its
PostgreSQL base is the immutable CloudNativePG 17.11 minimal Trixie image. Separate build and
runtime stages preserve the base's PostgreSQL executables, locale and non-root UID 26.
Only extension files and applicable Apache notices are copied from the build stage. Exact Debian
security updates for OpenSSL and PCRE are installed in the runtime without compiler packages.
Runtime vulnerability scans retain inherited findings; unavailable fixes are not reported as resolved.

Publish the owned image from tagged releases with source-revision metadata and keyless signing.
Release readback checks the published digest, expected source revision and exact signing workflow.
PR acceptance starts PostgreSQL with AGE in shared_preload_libraries, creates the extension and
executes a two-hop graph traversal over an authenticated connection using a dedicated read-only role.
Successful reads bracket mutation and privilege-denial checks.

CloudNativePG owns initialization, supported PostgreSQL configuration, restarts and persistent storage.
Deployment pins the published digest in `spec.imageName`. The hybrid observer requires this
direct declaration; it does not resolve image catalogs or other mutable image indirection.
Operators activate AGE through the supported shared_preload_libraries configuration and create
the extension in the selected database. Existing clusters require a planned restart and database
initialization; installation alone does not establish capability.

## Consequences

- The artifact does not activate a controller feature or install a production database.
- Base and security-package pins require regular maintenance. The standalone query test does not
  establish that every inherited dependency is free of vulnerabilities.
- Hybrid admission and observation remain separate work under #162, behind the existing
  default-off engine-providers and provisioned-sources gates.
- Standalone image acceptance proves extension and role behavior. Real CloudNativePG startup,
  publication, rotation and retained lifecycle remain provider acceptance under #38.
- The initial release targets Linux amd64. Other architectures require their own build and
  behavior evidence before being advertised.
