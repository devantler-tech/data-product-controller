# AGENTS.md

## Project overview

`devantler-tech/data-product-controller` is the cloud-native control plane for portable, composable data products. Its API describes ownership, standard query interfaces, independently deployed interaction surfaces, and dependencies between products. The controller observes that metadata; it never stores product data or credentials.

The minimum Go version is declared only in `go.mod`. The public roadmap is GitHub issue [#1](https://github.com/devantler-tech/data-product-controller/issues/1).

## Repository structure

- `api/v1alpha1/` — versioned Kubernetes API types and generated deep-copy code.
- `internal/controller/` — dependency-aware `DataProduct` reconciliation.
- `internal/provisioner/v1/` — versioned, read-only provisioner observation contract.
- `internal/provider/v1/` — versioned engine dispatch and read-only CloudNativePG, Percona MongoDB and ArangoDB observation.
- `internal/connector/v1/` — versioned Deployment observation with bounded, uncached, exact-name reads.
- `internal/registry/` — read-only descriptor API and reference registry UI.
- `internal/catalog/` — default-off DCAT 3 JSON-LD projection of publisher-declared datasets.
- `internal/dataspace/` and `cmd/dsp-catalog/` — default-off offline DSP catalog export from explicit public provider bindings; no network or Kubernetes access.
- `internal/preflight/` and `cmd/product-check/` — default-off local publisher validation using the embedded delivered CRD and bounded public descriptor preview.
- `web/` and `cmd/ui-kit/` — portable UI protocol library and independent, default-off compatibility host.
- `internal/demoproduct/` and `cmd/demo-product/` — independently served example product, API contract, and UI.
- `internal/httpsource/` and `cmd/http-source/` — default-off, Secret-configured read-only HTTPS JSON export connector with separate query and management listeners.
- `internal/contractprobe/` and `cmd/contract-probe/` — independent credential-free HTTPS contract reachability with bounded requests and management-only endpoints.
- `pkg/featureflag/` — OpenFeature boundary; provisioned sources, connector readiness, contract readiness, composition, DCAT publication, and portable UI grants are default-off. The registry workspace is always available.
- `config/crd/bases/` and `config/rbac/` — generated Kubernetes manifests.
- `charts/data-product-controller/` — installable controller, CRD, routing, and demo product.
- `deploy/` — signed controller manifest artifact published with each release.
- `docs/adr/` — architectural decisions.
- `main.go` — controller manager and registry process.

`CLAUDE.md` and `GEMINI.md` each contain a Markdown heading and a single `@AGENTS.md` include.
Do not copy instructions into them.

## Architecture boundaries

- `DataProduct` is control-plane metadata. Do not put records, payloads, passwords, tokens, or connection strings in spec or status.
- Products publish standard external contracts. Protocol-specific schemas stay in OpenAPI, AsyncAPI, GraphQL, DCAT, or adapter documents rather than expanding the CRD for each technology.
- Composition references a producer's named output. The controller reports missing or unready dependencies and automatically requeues consumers when producers change.
- The default-off `composition` flag checks cycles and declared protocol/version requirements, records direct
  lineage, and requeues transitive consumers. Reject every cross-namespace edge before reading its producer; a
  reference grants no permission to copy producer metadata. Preserve the five-second, 256-product,
  1,024-input, 64-level and 64-KiB lineage bounds. Compatibility is publisher-declared; never fetch schemas or
  product data. See `docs/composition.md`.
- Provisioned sources remain owned by external controllers. The `crossplane/v1` adapter observes same-namespace resource readiness and Secret metadata with explicit scoped RBAC; it never provisions, adopts, deletes, or reads connection values. See `docs/provisioned-sources.md` for the publication and ownership contract.
- Engine selection uses `engine-provider/v1` and the default-off `engine-providers` gate alongside
  `provisioned-sources`. Keep admission and runtime dispatch aligned. SQL/native uses `cnpg/v1` with
  a same-namespace Cluster and its generated application Secret owned by the current UID; custom
  bootstrap credentials and unsupported engine combinations are rejected. Document/native uses
  `percona-mongodb/v1`, the declared Percona 1.23.0 single managed unsharded replica-set profile,
  and an explicitly referenced custom-user password Secret with only declared read roles and current
  source ownership. The password publisher is independent; never assume automatic operator ownership.
  Graph/native uses `arangodb/v1`, the pinned operator 1.4.5 typed API and official ArangoDB
  3.12.12 Single profile with explicit count one, its original tag or verified official
  release index, and matching accepted/current/member image declarations and the actual official binary edition marker. Hash the live spec without defaults;
  require matching accepted/applied versions, runtime readiness and successful bootstrap. Graph
  publication is independently declared through bounded read-only application metadata and current
  source ownership; root/JWT/operator publications are unsupported. Document and Graph/cnpg-hybrid
  use `cnpg-hybrid/v1`, a same-namespace CNPG Cluster, an exact supported immutable image matching
  its reported image, and a dedicated reader publication bound to the current UID and generation.
  Graph additionally requires declared `age` in CNPG's `shared_preload_libraries`. Reserved
  operator credentials are rejected before reads. Effective extension loading and read-only grants
  belong to the independent publisher and real acceptance, not metadata observation. No database
  client is instantiated.
  Preserve exact-name
  uncached reads, metadata-only Secret negotiation, the five-second observation bound, 30-second
  polling and independent `SourceReady`. See `docs/engine-providers.md`.
- The HTTP source connector is a separate data-plane workload. It reads its own projected Secret, exposes only a fixed GET export, and never gives the controller source credentials or data. Preserve TLS verification, redirect/proxy rejection, bounded reads, and the explicit consumer/egress policy. It does not register products or report Kubernetes conditions; see `docs/http-source.md`.
- Connector observation supports only named same-namespace `apps/v1` Deployments through `deployment/v1`. Preserve exact-name read-only RBAC, full current-generation replica readiness, independent `ConnectorReady` refresh, and bounded polling. Never add workload ownership, Secret reads, or data-plane URL probes; see `docs/connector-readiness.md`.
- Contract checks bind selected outputs to independently owned probe Deployments. Preserve literal URL binding, the shared observation deadline, independent `ContractsReady` refresh, and the separation between controller reads and probe network traffic; see `docs/contract-readiness.md`.
- A product UI is independently deployed. The registry may sandbox it, but must not import its JavaScript, pass credentials, or become its runtime owner.
- The default-off `ui-contract` feature permits bounded status and resize hints under v1. V2 adds only a Light/Dark appearance hint behind the additional default-off `ui-appearance` gate. Preserve opaque iframe origins, exact source/session/shape checks, publisher-owned host approval, grant intersection, navigation revocation, message bounds and timeout cleanup. See `docs/ui-contract.md`.
- The JSON registry is a convenience projection of Kubernetes resources, not a second source of truth.
- Discovery browser clients validate the closed public descriptor before display or export.
  Keep bounded streamed UTF-8 parsing and navigation cancellation; metadata inspection must allow
  unhealthy and UI-less products without granting permission to mount a published surface.
- The default-off `registry-lineage` gate also requires `registry-discovery` and the canonical
  declared-compatibility evaluator. Trace declared same-namespace inputs only, verify returned
  identities, cache failed reads, and keep public health separate from compatibility. Preserve
  one concurrent trace, the shared five-second deadline, 256 read attempts, 1,024 edges,
  64 product levels, 1-MiB retained public metadata and 2-MiB encoded response bounds.
  Incomplete branches stay explicit. No schema, endpoint or data requests occur. Navigation
  revokes browser work and export. See `docs/dependency-traces.md`.
- DCAT publication requires the explicit `data.devantler.tech/dcat-type: Dataset` annotation and
  the default-off `dcat-catalog` gate. It projects public metadata only; a listing grants no access
  and asserts no readiness. Preserve the configured catalog identity, stable output identities,
  one concurrent request, 16-object pages, 256-product/17-request/five-second scan bounds,
  1,024 outputs, 16-KiB fields, 1-MiB retained public metadata and 2-MiB encoded responses.
  Never fetch contexts, contracts or data in the controller. See `docs/dcat-catalog.md`.
- `v1alpha1` is intentionally small and may change while real provisioned, integrated, and composed products validate the model. Never claim unimplemented roadmap capabilities.
- DSP export uses the `dsp-catalog-export` OpenFeature gate. Preserve explicit provider/assigner
  identity, target-free offers and rule semantics, exact dataset/output selection, distinct DSP
  service/distribution identities, strict contexts and input/output bounds. Never infer permissions,
  transfer formats or connector services from query metadata. Compatibility covers the catalog
  data model only; see `docs/dsp-catalog.md`.
- Publisher preflight uses the default-off `publisher-preflight` gate. Keep input caller-selected,
  regular-file-only and bounded; never fetch references, instantiate a Kubernetes client, read
  credentials or apply resources. Validate the embedded delivered create-time schema, CEL and
  metadata rules, and reuse canonical public projection and declared compatibility rules.
  Imported status never establishes readiness. Missing local producers remain unresolved;
  invalid or incomplete bundles publish no descriptor previews. See `docs/publisher-preflight.md`.
  Repeatable `--file` shares one byte/product/document/graph/CEL/context budget across selections.
  Preserve default v1 output; explicit `--report-version v2` adds numeric provenance, safe known
  paths, bounded witnesses/counts and a static plan only for complete valid bundles. Raw declarations
  never remain in public reports. Keep the report schema offline and the example reader independent.

## Validation

Run before every PR:

```bash
golangci-lint fmt
go build ./...
go test ./...
go test -tags=browser ./internal/browser
sh scripts/chart.test.sh
sh scripts/release.test.sh
sh scripts/age-release.test.sh
sh scripts/scanner-suppressions.test.sh
sh scripts/toolchain.test.sh
helm lint charts/data-product-controller
golangci-lint run
(cd tests/provider/fixture && go test -race -count=1 ./... && go vet ./...)
```

Workflow changes also require `actionlint` and `zizmor`.

The required AGE image job builds `images/postgresql-age`, starts PostgreSQL 17.11 with AGE
preloaded, and runs `tests/provider/age-image-bootstrap.sh` as UID 26 against a read-only
container root. Repeat the same test after changing its base, archive, compiler or extension
pins. Keep the Apache checksum and detached signature verification, CNPG executables, synthetic
acceptance credentials, and denial checks. See `docs/postgresql-age-image.md`.

The required CI source-integration matrix runs `bash tests/source/run.sh` in four isolated
ephemeral KSail clusters with enforced NetworkPolicy. It requires Docker and several
gigabytes of free disk space; hosted execution supplies the real-cluster evidence
when the local environment cannot run it. See `docs/source-integration-tests.md`.

The required native Document job separately runs `bash tests/provider/percona.sh`
against the real pinned Percona operator. Its independent database-client fixture
has a separate Go module and must be tested explicitly. See `docs/real-document-acceptance.md`.

The required native Graph job runs `bash tests/provider/arango.sh` against the real
pinned ArangoDB operator. It requires authenticated traversal, specific authorization denials
and retained member recovery with operator-written readiness. See `docs/real-graph-acceptance.md`.

The required PostgreSQL model job runs `bash tests/provider/postgres.sh` with the pinned
CloudNativePG operator and signed owned AGE image. It exercises native SQL, hybrid JSONB and
hybrid Graph queries, exact grant denials, password rotation and retained database lifecycle.
See `docs/real-postgresql-acceptance.md`.

API type or marker changes require deep-copy code, CRDs, and RBAC to be regenerated with controller-tools v0.21.0. Distribute the generated CRD to the chart and release artifact; all three copies must remain identical:

```bash
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 \
  object rbac:roleName=manager-role crd paths=./... \
  output:crd:artifacts:config=config/crd/bases \
  output:rbac:artifacts:config=config/rbac
cp config/crd/bases/data.devantler.tech_dataproducts.yaml \
  charts/data-product-controller/crds/data.devantler.tech_dataproducts.yaml
cp config/crd/bases/data.devantler.tech_dataproducts.yaml deploy/data.devantler.tech_dataproducts.yaml
cmp config/crd/bases/data.devantler.tech_dataproducts.yaml \
  charts/data-product-controller/crds/data.devantler.tech_dataproducts.yaml
cmp config/crd/bases/data.devantler.tech_dataproducts.yaml deploy/data.devantler.tech_dataproducts.yaml
```

## Development rules

- Follow test-driven development for behavior: prove RED, make the smallest implementation GREEN, then refactor.
- Keep new release behavior behind an OpenFeature flag, default-off, with both states tested. Remove short-lived flags after rollout; long-lived operational gates need an explicit rationale.
- Treat product descriptors, contract documents, and embedded product UIs as untrusted input.
- Preserve HTTPS-only public interface validation and the iframe sandbox boundary.
- Prefer Kubernetes conditions with actionable reasons and stable messages. Avoid status-only reconciliation loops.
- Generated files carry generated provenance and are never edited manually.
- ADRs live in `docs/adr/`. Documentation states present behavior, constraints, and rationale.

## Maintenance

The shared devantler-tech portfolio contract applies: use an isolated per-run working copy, capture issue-driven work before implementation, push only `codex/*` branches from the Codex lane, checkpoint in draft PRs, use Conventional Commit PR titles, and never self-approve. Begin generated issues, PRs, and comments with `> 🤖 Generated by the Agentic Engineer`.

GitHub Copilot reviews and agent workflows are disabled for this repository. Do not request Copilot as a reviewer or add a `copilot-setup-steps` workflow; use the repository's permitted review path instead.

Operate before advance: repair failing default-head CI and actionable trusted PRs before roadmap work. For product advancement, work oldest-actionable-first within the current roadmap milestone. Keep PRs draft until exact-head hosted checks, substantive current-head review, and real-path behavior evidence are terminal.

Production deployment is owned by `devantler-tech/platform`. This repository publishes the controller image and Helm chart; platform configuration pins immutable artifacts and owns Gateway, policy, namespace, and rollout details. Never mutate a live cluster as a deployment shortcut.
