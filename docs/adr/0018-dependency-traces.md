# ADR 0018: On-demand public dependency traces

Status: Accepted

Consumers need to identify the upstream product that blocks an input chain. The
registry provides a read-only, exact-root trace behind the default-off
`registry-lineage` OpenFeature gate, alongside `registry-discovery`.

The trace follows declared same-namespace inputs through unhealthy producers.
Each product is read once, including failed lookups. Public product observations
and declared contract compatibility are separate fields; compatibility never
establishes readiness, access, or schema-content compatibility. The composition
controller supplies its canonical compatibility evaluator through an explicit
handler option. A missing evaluator disables the trace capability.

Sequential exact-name reads share a five-second deadline. One trace runs per
registry instance, with bounds of 256 read attempts, 1,024 edges, 64 product levels,
1 MiB retained public node metadata and 2 MiB encoded output. Missing, invalid,
unavailable, foreign, cyclic and limited branches remain explicit. Completeness
means that the permitted graph was inspected; it does not mean products are ready.
Reads are observations over time, not an atomic Kubernetes snapshot.

Only the canonical portable publication profile is accepted. Nodes retain public
identity, owner, version and generation-bound health. Edges retain declared port
references and requirements. No source configuration, Secrets, provider messages,
workload identities, product records, endpoint probes or status-lineage redirects
are included. Deleting producers have a separate unusable state.

The workspace offers an on-demand table, retry and JSON download. Selection and
refresh cancel the current trace and revoke its export. Trace links navigate
catalog identities only. A bundled schema and an independent offline example
consumer make the format usable without importing controller packages.
