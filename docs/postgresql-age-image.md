# PostgreSQL with Apache AGE

The owned image combines PostgreSQL 17.11 and Apache AGE 1.7.0. It preserves CloudNativePG's
PostgreSQL executables and UID 26. The image is an external database artifact; building or selecting
it does not enable the controller's default-off hybrid provider.

## Artifact and release verification

Tagged repository releases publish
`ghcr.io/devantler-tech/data-product-controller-postgresql-age:17.11-age1.7.0-dpc<VERSION>`.
Use the immutable digest from the successful **Publish PostgreSQL with Apache AGE** job summary.
The reusable publisher verifies its own immutable workflow identity, signs that digest, checks
the release source revision and repeats the authenticated graph query after pulling the artifact.
Readback first removes the job's registry login, requiring anonymous artifact and signature access.
Both its initial build and release readback must pass before the artifact is considered delivered.

For independent verification, obtain the release tag, source commit and publisher commit from
the tagged repository source. The publisher commit is the full SHA after `@` in
`.github/workflows/cd.yaml`'s `publish-age` job. Require all three expected values; accepting any
signer or any repository release is insufficient.

```bash
: "${AGE_IMAGE:?Set the published repository@sha256 digest}"
: "${RELEASE_TAG:?Set the exact v-prefixed release tag}"
: "${SOURCE_SHA:?Set its full source commit}"
: "${PUBLISHER_SHA:?Set the full immutable publish-age workflow commit}"
[[ "$AGE_IMAGE" =~ ^ghcr.io/devantler-tech/data-product-controller-postgresql-age@sha256:[a-f0-9]{64}$ ]]
[[ "$SOURCE_SHA" =~ ^[a-f0-9]{40}$ && "$PUBLISHER_SHA" =~ ^[a-f0-9]{40}$ ]]
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]
cosign verify \
  --certificate-identity "https://github.com/devantler-tech/data-product-controller/.github/workflows/publish-age.yaml@$PUBLISHER_SHA" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository devantler-tech/data-product-controller \
  --certificate-github-workflow-ref "refs/tags/$RELEASE_TAG" \
  --certificate-github-workflow-sha "$SOURCE_SHA" "$AGE_IMAGE"
docker pull "$AGE_IMAGE"
[[ $(docker image inspect "$AGE_IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}') == "$SOURCE_SHA" ]]
```

## CloudNativePG configuration

Set the verified immutable digest directly in the Cluster's `spec.imageName`. The hybrid
controller adapter observes this direct image declaration and rejects `imageCatalogRef` because
its bounded exact-name reads do not resolve catalog indirection. Keep image changes in reviewed
GitOps configuration. Independently operated CloudNativePG clusters may use
[image catalogs](https://cloudnative-pg.io/docs/1.30/image_catalog/); that configuration does not
satisfy the hybrid observation contract.

The Cluster's `spec.postgresql.shared_preload_libraries` must include `age`. For a new database,
use its `bootstrap.initdb.postInitApplicationSQL` to execute `CREATE EXTENSION age;`. Existing
databases need deliberate extension initialization and a planned restart for preload changes.
Do not install packages in a running database pod or use `ALTER SYSTEM` to bypass its operator.
See [PostgreSQL configuration](https://cloudnative-pg.io/docs/1.30/postgresql_conf/) and
[database bootstrap](https://cloudnative-pg.io/docs/1.30/bootstrap/).

Create graph objects with a separate writer. A reader needs connection access, schema usage and
SELECT privileges on its graph tables. Its session loads `$libdir/plugins/age` and includes
`ag_catalog` in the search path. The plugin copy permits session loading without granting
superuser, database creation or role management. Granting the database's bootstrap owner to a
consumer does not establish a read-only publication.

## Acceptance and maintenance

The required CI image job starts an actual PostgreSQL process with AGE preloaded, checks the
extension version and executes a two-hop Cypher traversal through an authenticated reader.
SQL and Cypher mutation denials require SQLSTATE `42501`; successful reads bracket those denials.
The independent owner checks the effective server preload and installed extension separately.
Acceptance then restarts without preload and requires that check to fail while a reader's
connection-local load and Cypher query still succeed. Readers gain no server-settings privilege.
The container runs as UID 26 with a read-only root, bounded memory/CPU, temporary storage and no
external network. The complete fixture is in `tests/provider/age-image-bootstrap.sh`.

The build verifies the Apache release archive's fixed SHA-512 checksum and detached signature
using its reviewed public release key. The compiler and PostgreSQL development headers stay in
the build stage. Runtime package pins apply available OpenSSL and PCRE security updates.
Rebuilding must repeat the query tests and review the actual runtime vulnerability scan;
inherited findings without available fixes remain visible and are not described as resolved.

Standalone image acceptance establishes image behavior. CloudNativePG startup, connection
publication, password rotation, retained storage and controller observation require the separate
real-provider matrix. Production deployment remains owned by the platform repository.
