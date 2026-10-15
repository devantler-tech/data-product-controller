# KHoist

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A data product is a dataset or data service that a team publishes for others to use, together with
a description of who owns it, how to query it and what it depends on. This project lets teams
describe their data products as Kubernetes resources. A controller checks that each product's
dependencies exist and reports whether the product is ready, and a read-only registry lists the
products so that people and tools can find them.

The controller handles descriptions only. Product data and credentials stay with the team that
owns the product.

- **Try it:** [run the example product locally](#try-it-locally), with no cluster and no credentials.
- **Documentation:** [KHoist user guides](website/README.md) cover first use, publishing and consuming APIs, installation and troubleshooting.
- **Use it:** [install the controller on a cluster](#install-on-a-cluster).
- **Integrate it:** [run an independent API consumer](docs/api-consumer.md).
- **Change it:** read the [contribution guide](CONTRIBUTING.md).

## Try it locally

The repository includes an example data product, *Harbour observations*, that serves two fixed,
made-up measurements, the description of its API, and its own web page. The measurements are built
into the program; it reads no external source. Running it shows what a product publishes. It does not run the controller or the registry, and it needs no Kubernetes
cluster, account or credentials.

You need Git, `curl`, and Go at the version in [`go.mod`](go.mod) or newer.

```bash
git clone https://github.com/devantler-tech/data-product-controller.git
cd data-product-controller
go run ./cmd/demo-product --listen-address 127.0.0.1:8080
```

The first run downloads the Go dependencies and can take a few minutes. The example then prints a
line ending in:

```text
starting demo data product address=127.0.0.1:8080
```

It prints that line before it opens the port, so the line alone does not show that the example is
running. It is running when the command stays in the foreground and the request below is answered.
If the command exits with an error instead, port 8080 is probably in use: choose another port and
use it in the addresses below.

The address `127.0.0.1` keeps the example reachable from your own machine only. Without
`--listen-address` it listens on port 8080 on every network interface.

In a second terminal, ask the example for its data:

```bash
curl http://127.0.0.1:8080/api/observations
```

It answers with the two sample measurements:

```json
{"items":[{"station":"nordhavn","temperatureCelsius":16.8,"salinityPsu":14.2,"observedAt":"2026-08-29T12:00:00Z"},{"station":"refshaleoen","temperatureCelsius":17.1,"salinityPsu":13.9,"observedAt":"2026-08-29T12:05:00Z"}]}
```

Then open `http://127.0.0.1:8080/ui` in a browser. The page shows the same two rows in a table,
and **Load observations** filters them by station. The API description that other tools read is at
`http://127.0.0.1:8080/openapi.json`.

Press `Ctrl+C` in the first terminal to stop the example. It writes no files; Go keeps the
downloaded dependencies and build results in its usual caches.

### Check a product description

You can also check product descriptions before any cluster is involved. This command reads one
example file with three products, where the third uses an output of each of the other two:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file docs/examples/composition.yaml
```

It prints:

```text
Validated 3 product(s): declarations only; live readiness and access remain unverified.
Required operational features: composition
```

The checker itself reads only the file you name and contacts no cluster and no network address;
`go run` may still download Go dependencies to build it. A pass therefore says the descriptions are
well formed and fit together, not that the products are running or reachable. The [publisher guide](docs/publisher-preflight.md) covers the released command,
namespace selection and exit codes.

## Data product contract

```yaml
apiVersion: data.devantler.tech/v1alpha1
kind: DataProduct
metadata:
  name: harbour-observations
spec:
  id: https://data-products.example.com/products/harbour
  name: Harbour observations
  description: Queryable temperature and salinity observations.
  version: v1.0.0
  owner:
    name: data-platform-team
  outputs:
    - name: observations
      protocol: OpenAPI
      url: https://data-products.example.com/products/harbour/api/observations
      contractUrl: https://data-products.example.com/products/harbour/openapi.json
      mediaType: application/json
```

Another product composes this output without copying its data:

```yaml
spec:
  inputs:
    - name: harbour
      productRef:
        name: harbour-observations
        output: observations
```

The custom resource is control-plane metadata. Product data and credentials do not belong in the Kubernetes API. Credentials remain in Secrets consumed directly by provisioner or connector workloads.

To check a description before publishing it, see [Check a product description](#check-a-product-description).
When checking this example, choose its target namespace explicitly with `--namespace products`.

## API-only workspace

Products publish named API endpoints and standard contracts. Consuming applications
own their UIs; the registry does not embed product pages, execute publisher code or
query published endpoints automatically. See [the architecture decision](docs/adr/0023-api-only-product-integration.md).

Search products by name, description, owner or namespace, and filter by controller-reported
readiness. Select a product to inspect its owner, version, published data interfaces and input
lineage. **Open API** and **View contract** open the publisher's HTTPS endpoints in a separate
tab. A readiness indication describes the controller's observation; it does not grant data access
or promise that an endpoint is reachable. **Refresh products** withdraws the old
descriptor and reloads the inventory. With discovery enabled, an explicit product
link is reread before its current metadata is shown.

The Harbour example labels its fixed synthetic records and displays station, observation time in
UTC, temperature and salinity. Choose a station and use **Load observations** to filter the data.
The latest requested station owns the result, even if an earlier request finishes later.
Use the workspace's **Open API** and **View contract** links to open the published
interfaces explicitly. Standalone reference pages remain deprecated examples of
API consumption; they are not embedded by the registry.

The optional [KSail release product](docs/ksail-releases.md) displays real published versions,
UTC dates and channels from GitHub. Its filters use the loaded public API page, and **Refresh
releases** makes one credential-free source read. It shares the independent reference workload;
the controller receives no release records. The workload serves its release interfaces without
an opt-in switch. Optional catalogue publication is default-off and activated through
deployment-owned chart values; unlisting the product does not withdraw its direct interfaces.

The registry and Harbour pages use content-addressed scripts and styles. Their HTML is not
cached, so upgrades load the matching assets even when a browser or CDN retains an older release.

The sample's `PUBLIC_BASE_URL` declares the HTTPS address under which its endpoints are published.
The deprecated kit permits credential-free reads of only the observation and
UI-configuration paths when embedded, including in Safari. The chart derives this address from its shared-host route, or accepts
`demoProduct.publicBaseURL` for a separate host. Values contain an exact HTTPS origin and an optional
path prefix of letters, digits, underscores and hyphens, without a trailing slash. Request and
forwarded host headers never select the allowed address. Without this setting, standalone use
retains the same-origin policy.

The registry validates public metadata and exposes only explicit, credential-free
HTTPS interface links. Its browser policy denies frames and permits connections
only to its own API. It passes no bearer token, Secret or Kubernetes identity to
publishers, and never imports their JavaScript.

The chart and declaration above publish API interfaces without `spec.ui`. This is
a breaking change for consumers that used chart-authored UI metadata: select a
named output and its standard contract instead. Direct reference pages remain
available. Existing hand-authored legacy `spec.ui` and descriptor fields remain
bounded compatibility metadata.
The registry grants no product presentation capabilities, regardless of legacy
flag settings. The independent compatibility kit remains deprecated and retains
its restrictive sandbox and protocol controls; [its guide](docs/ui-contract.md)
describes that existing runtime. Versioned schema/runtime retirement and normal
Platform adoption are tracked in [#390](https://github.com/devantler-tech/data-product-controller/issues/390).

The workspace offers System, Light and Dark appearance with an orange accent.
An explicit choice stays in browser storage when available; denied storage does
not prevent discovery or inspection. Workspace appearance does not send product
messages, contact publishers or change selected metadata. Consumer applications
own their own appearance and data-access policy.

The sample product and standalone UI kit handle SIGTERM and interrupt by closing
their listeners and waiting up to ten seconds for active responses to finish.
They close remaining connections on timeout and exit unsuccessfully if shutdown
fails.

## What is included

Features marked default-off make no reads and publish nothing until an operator switches them on.
That is not the same as being ignored: a product that declares something a switched-off feature
would check, such as a source or a connector, stays not ready until the feature is on. The guides
linked in this section give each switch and its effect.

The current foundation provides:

- a namespaced `DataProduct` CRD with guarded HTTPS interfaces and stable URI identity;
- composition through named output references, with dependency-aware readiness conditions;
- default-off contract version checks, bounded cycle detection, and direct input lineage within a namespace in the API and UI;
- a default-off `provisioned-sources` feature that observes a provisioner-owned resource and its published connection Secret metadata;
- default-off, versioned engine selection with native CloudNativePG SQL, Percona Document and ArangoDB Graph observation, plus PostgreSQL JSONB and Apache AGE hybrid profiles;
- default-off connector Deployment observation, with full current-generation availability included in product and registry readiness;
- default-off contract reachability through independent probes, with URL-bound `ContractsReady` conditions and bounded network checks;
- a portable JSON descriptor registry at `/api/v1/products`;
- default-off [bounded discovery](docs/registry-discovery.md) with a versioned descriptor schema, exact product lookup, namespace-scoped pages, public health dimensions, shared product links and offline descriptor handoff;
- default-off [dependency traces](docs/dependency-traces.md) with transitive input diagnosis, separate contract and readiness states, upstream navigation and portable JSON export;
- a default-off DCAT 3 JSON-LD catalog at `/api/v1/catalog`, with explicit publisher opt-in and stable dataset, distribution, and service identities;
- a default-off [offline DSP catalog exporter](docs/dsp-catalog.md) that combines DCAT snapshots with explicit provider services, transfer formats and offers;
- a default-off [publisher preflight command](docs/publisher-preflight.md) that validates local manifests and previews public descriptors before GitOps publication;
- an API-only registry workspace that inspects public descriptors and exposes explicit standard interface links;
- a default-off, versioned UI manifest and capability protocol, with an independent compatibility kit;
- an independently deployed harbour-observations example with its own OpenAPI contract, query API, and UI;
- a live [KSail release browser](docs/ksail-releases.md) with optional catalogue publication, its external public query contract and bounded, credential-free reads;
- an opt-in, Secret-backed HTTPS JSON export connector with a read-only API, OpenAPI contract, probes, and metrics;
- a Helm chart containing CRDs, least-privilege RBAC, hardened workloads, services, and optional Gateway API routing. Leader election uses release-local Leases and core Event create/patch permissions; it grants no cluster-wide Event access.

Provisioned sources use delegated provisioning: an external controller owns infrastructure and credentials. The versioned `crossplane/v1` observer checks readiness and connection publication without creating resources or reading Secret values. See the [provisioned-source guide](docs/provisioned-sources.md) for its contract, scoped access, enablement, and limitations.

The [engine-provider guide](docs/engine-providers.md) explains typed source selection, the
application publication contracts, independent `SourceReady` status, scoped permissions and
rollout gates. Database lifecycle remains externally owned. Required disposable-cluster acceptance
exercises SQL/native, Document/native, Graph/native, Document/hybrid and Graph/hybrid queries,
read-only grants, credential rotation, recovery and retained data. PostgreSQL hybrid products use
JSONB and AGE query contracts rather than MongoDB or ArangoDB protocols; production activation
remains a separate operator decision.

Connector workloads remain independently owned. The `deployment/v1` observer reads one named Deployment in the product's namespace and publishes `ConnectorReady` alongside aggregate readiness. See the [connector-readiness guide](docs/connector-readiness.md) for flag enablement, narrowly scoped RBAC, an authored product example, and rollout semantics.

The [HTTP source guide](docs/http-source.md) describes the independently deployed reference connector,
its credential and network boundaries, and its default-off release gate. It does not register a
DataProduct or imply controller-observed connector health.

The [contract-readiness guide](docs/contract-readiness.md) describes independent contract probes,
selected output checks, scoped observation, metrics, and failure recovery. Network fetching stays
outside the controller; probe configuration must match each selected output's current contract URL.

The [composition guide](docs/composition.md) covers declared version compatibility,
cycle diagnosis, observed lineage, and a three-product example. Composition is
control-plane observation; independently operated workloads perform data queries
and transformations.

The [DCAT catalog guide](docs/dcat-catalog.md) describes publication for independent
catalog consumers. Set the `data.devantler.tech/dcat-type: Dataset` annotation only
when a product describes one logical dataset and every named output provides
access to a representation of it. Enable the separate `dcatCatalog.enabled` chart
flag and supply `dcatCatalog.id` to publish the catalog. Discovery grants no access
to data and makes no availability claim. Production activation and flag retirement
are tracked in [#123](https://github.com/devantler-tech/data-product-controller/issues/123).

Engine-specific provisioning, additional source adapters, schema-content compatibility, and data-space exchange remain [roadmap work](https://github.com/devantler-tech/data-product-controller/issues/1).

## Install on a cluster

This part is for operators. It changes a Kubernetes cluster, so use one you are allowed to change.
You need Helm, `kubectl` access to the cluster, and a Gateway API listener if you enable the route.
Verifying a signature additionally needs Cosign.
The chart's `kubeVersion` field states the Kubernetes versions it supports.

The release workflows are set up to publish, for each release tag, the Helm chart plus a controller
image and manifest artifact signed by the portfolio's trusted keyless release workflow. That setup
does not prove that a particular version is available. Before you depend on one, confirm that the
registry serves it:

```bash
helm show chart oci://ghcr.io/devantler-tech/charts/data-product-controller --version '<version>'
```

The command prints the chart's metadata when that version exists and fails when it does not.
Platform deployments should pin the released chart and an immutable image digest.

### Verify a released chart

The chart publication job signs the digest returned by Helm and verifies its signature before
succeeding. Chart signatures use the issuer `https://token.actions.githubusercontent.com` and the
identity `https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/<tag>`.
Verify a released chart using its published digest and tag:

```bash
cosign verify \
  --certificate-identity 'https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/<tag>' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  'ghcr.io/devantler-tech/charts/data-product-controller@sha256:<digest>'
```

Deployment policy must match this chart identity separately from the shared controller-image and
manifest publisher. Platform configuration owns signature enforcement and rollout.

The controller image is signed by that shared publisher, not by the chart workflow above. To obtain
a verified image digest, follow the [released artifact acceptance guide](docs/release-acceptance.md):
its script checks the image and chart signatures of one release against the release tag and source
revision you supply.

### Install the chart

Install the chart with an existing Gateway API listener.
Replace `<version>` and `<verified-image-digest>` with the release's chart version and the controller
image digest verified against the trusted publisher:

```bash
helm upgrade --install data-product-controller \
  oci://ghcr.io/devantler-tech/charts/data-product-controller \
  --version '<version>' \
  --namespace data-product-system \
  --create-namespace \
  --set-string image.digest='sha256:<verified-image-digest>' \
  --set route.enabled=true \
  --set route.host=data-products.example.com
```

An empty `image.tag` selects the packaged chart's `appVersion` without its optional `v` prefix.
An explicit tag overrides that default; `image.digest` takes precedence over either tag for all
workloads. Pin a verified digest for production. The optional HTTP source workload requires a digest
even outside production, as does the optional contract probe. When reusing saved Helm values, replace any old image override explicitly.

Updating the CRD is a manual step. Helm installs the chart's `crds/` directory on first install only
and never updates or removes it on `helm upgrade`, so a release that changes the `DataProduct` schema
does not reach clusters that already have the chart. Apply the new CRD before upgrading:

```bash
helm show crds oci://ghcr.io/devantler-tech/charts/data-product-controller --version <version> \
  | kubectl apply -f -
```

`v1alpha1` is deliberately small and expected to change, so plan for this on schema-changing releases.

The registry workspace is available by default on the registry listener. Public routing remains
opt-in; the platform owns authentication and access policy.

## Contributing

The [contribution guide](CONTRIBUTING.md) describes a first documentation change, the checks you
can run without a cluster, the checks only hosted CI runs, and how to regenerate the generated
files. [AGENTS.md](AGENTS.md) holds the detailed architecture boundaries.

## Design

[ADR 0001](docs/adr/0001-portable-data-product-control-plane.md) records why the Kubernetes resource stays a small control-plane profile and how products remain portable. [ADR 0002](docs/adr/0002-delegated-provisioned-sources.md) defines delegated source ownership and observation.
[ADR 0003](docs/adr/0003-read-only-http-source-connector.md) defines the reference connector's data-plane and credential boundaries.
[ADR 0008](docs/adr/0008-dcat-catalog-projection.md) defines the opt-in DCAT projection and its bounded, metadata-only publication contract.

[ADR 0009](docs/adr/0009-offline-dsp-catalog-export.md) defines the offline DSP catalog profile. It exports provider-supplied policies without implementing negotiation, enforcement or transfer.

[ADR 0010](docs/adr/0010-versioned-engine-provider-observation.md) defines engine dispatch and admission while preserving delegated source ownership.

[ADR 0017](docs/adr/0017-postgresql-model-publications.md) defines dedicated JSONB and AGE
publications and the required real PostgreSQL lifecycle acceptance.

The vocabulary is informed by the [Open Data Mesh Data Product Descriptor Specification](https://dpds.opendatamesh.org/), [W3C DCAT 3](https://www.w3.org/TR/vocab-dcat-3/), [OpenAPI](https://spec.openapis.org/oas/), [AsyncAPI](https://www.asyncapi.com/docs/reference/specification/v3.0.0), and the [Eclipse Dataspace Protocol](https://projects.eclipse.org/projects/technology.dataspace-protocol-base).
This release does not claim full conformance with those standards.
