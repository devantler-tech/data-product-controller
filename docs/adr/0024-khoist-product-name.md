# 0024: KHoist product name

Date: 2026-10-14

Status: Accepted

## Decision

The product is **KHoist**. Its canonical repository and Go module are
`devantler-tech/khoist` and `github.com/devantler-tech/khoist`.
The name combines the cloud-native k prefix with hoist, the active nautical verb
for raising a sail or lifting cargo. It reflects the product's direction: make
application data storage easier to set up and use through reusable data products.

The registry, publisher review, chart, controller image and current documentation
use the product name. New controller and chart artifacts are published as
`ghcr.io/devantler-tech/khoist` and `ghcr.io/devantler-tech/charts/khoist`.
The repository is renamed in place, preserving its numeric identity, issues,
pull requests, history and existing permissions. Consumer repository pointers
and product navigation are migrated in their owning repositories.

## Compatibility

The name does not change `DataProduct`, `data.devantler.tech/v1alpha1`, schema
URNs, descriptor/discovery/report protocols, annotations, published product IDs
or input/output names. Existing browser preferences retain their storage key.

Existing Kubernetes namespaces, release names, resource names, chart workload
selectors and service accounts remain stable. The controller retains the same
leader-election lease ID so old and new instances coordinate during an upgrade.
The standalone deployment's container is named `khoist` to match the shared
publisher's required digest-pinning target; its Deployment identity stays stable.

Historical image/chart digests and signing identities remain unchanged. Release
verification defaults to the KHoist profile; `--release-profile legacy` explicitly
selects the old artifact and signing names for a historical baseline. Verification
never falls back between profiles. Receipts state the verified profile. Helm
lifecycle acceptance distinguishes old baseline and new candidate chart names
while preserving deployed object identities and testing recovery.

The separately versioned, capability-bound PostgreSQL/AGE image family keeps its
existing `data-product-controller-postgresql-age` repository and tag format.
The controller's admitted tuple remains the previously verified immutable
artifact. A product name does not re-sign it or establish another image's
database capabilities.

## Publication and rollout

The AGE publisher requires the existing numeric GitHub repository ID and the
explicit old/new names. Its OIDC claims must match that identity and its callee
must be bound to the same repository name at an immutable commit. A different
repository using the old name is rejected. The caller uses the reviewed new
callee commit, and is changed to the canonical repository URL during the rename.
Signing, exact released-source binding, anonymous readback and bootstrap checks
remain required.

The repository rename, source merge, signed publication and Platform adoption
are separate evidence. Platform changes use immutable verified artifacts and
normal GitOps, preserving the existing deployment and recovery path. The complete
coordinated rename is tracked in
[#399](https://github.com/devantler-tech/khoist/issues/399), with its release
prerequisite in [#400](https://github.com/devantler-tech/khoist/issues/400).
