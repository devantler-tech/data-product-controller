# Source integration acceptance

`tests/source/run.sh` exercises the real controller, HTTP export connector, contract probe, and
registry together in an ephemeral KSail cluster. It uses Vanilla Kubernetes on
Kind/Docker with Cilium enforcing NetworkPolicy. Its synthetic source serves TLS
with a generated one-day certificate and test-only credentials.

The source runs in a separate container on Kind's private Docker network, exposed
to the connector through a headless Service and EndpointSlice. It has no published
host port. This exercises the chart's external-source CIDR rule: Cilium's default
[CIDR policy behavior](https://docs.cilium.io/en/stable/security/policy/layer3/)
does not match Cilium-managed Pod identities. The suite keeps that default intact.

Run it on a machine with Docker (including Buildx), KSail 7.182.6, kubectl,
Helm 3.12 or newer within major version 3, Cosign, GNU timeout, jq, yq, and OpenSSL:

```bash
bash tests/source/run.sh
```

The command creates a uniquely named local cluster, temporary kubeconfig, and local
registry. It never selects the operator's current Kubernetes context. Cleanup
deletes the source container, that cluster, and its storage on normal exit or
handled termination. Allow several gigabytes of free disk space for Kubernetes and image
builds. CI runs the same command on a disposable hosted runner, verifies the KSail
download checksum, grants only repository read access, and limits the job to
50 minutes.

The harness does not use KSail's `--ttl`: that mode keeps the create command in the
foreground until automatic destruction, which would prevent the assertions from
running. Hosted-runner disposal is the backstop for uncatchable termination in CI;
after an uncatchable local termination, remove the named cluster and source
container manually.

The candidate and independent fixture images are built from the checked-out source
and pushed to the local registry by digest. Acceptance first verifies the published
v1.12.0 controller and chart against their signatures, release source revision and
actual Linux/amd64 image identity. It installs that packaged chart through Helm,
queries the export, explicitly updates the retained CRD, and upgrades to a locally
packaged candidate. A real Helm rollback restores the released baseline; query,
current rollout and independent source/credential UIDs must recover before the
candidate is restored. Helm rollback retains the newer CRD intentionally.

Each install/upgrade uses an explicit disposable kubeconfig/context and a test-only
CA post-renderer. Certificate and hostname verification stay enabled. No production
Helm command runs. See [release acceptance](release-acceptance.md) and
[rollout observation](rollout-observation.md) for the artifact and observation boundaries.

The harness installs Kind's documented [local registry alias](https://kind.sigs.k8s.io/docs/user/local-registry/)
inside only its own nodes. This maps the host's `localhost:5055` image references
to the registry container on the private Docker network. A node-runtime pull of
the fixture digest verifies that mapping before workloads start.

The consumer has loopback health probes and denies incoming network traffic.
Its checked-in local image tag is replaced with the actual pushed digest before
apply. Two artifact-scoped scanner exceptions describe that dynamic digest and
private test registry; the suppression contract pins both exact rule/path pairs.
Production scanner exceptions are unchanged.

The fixture build retains the repository's Go module version so its HTTP routing
semantics match unit tests. The harness passes the generated Kind configuration
explicitly and rejects a cluster that also installed Kind's default CNI.

## Observed behavior

The controller runs with two replicas and leader election enabled. The allowed
consumer addresses every current ready controller Pod directly and reads its descriptor
API, requiring the current Deployment generation and ReplicaSet ownership. This
checks the non-leader endpoint as well as the leader; one successful
Service request cannot hide a replica that does not serve the registry.

The suite checks the workload-absent HTTP default and both connector-observation
flag states before granting exactly one named Deployment GET. It follows source
outage, recovery, revoked Kubernetes permissions, and projected bearer-token
rotation through `ConnectorReady`, aggregate `Ready`, and the registry response.
Credential rotation must retain the connector Pod UID. Management metrics must
remain reachable while the data service is unready.

An allowed consumer reads the real export and its OpenAPI document. A consumer
without the declared label must receive a transport failure, with successful
allowed requests before and after that check. This proves policy enforcement
rather than relying on rendered YAML alone. The same denied Pod must then succeed
when granted the allowed label and fail again when that label is removed, tying
the result to policy identity instead of an unrelated routing failure.

Disabling the HTTP runtime flag creates an unready replica under the chart's
RollingUpdate strategy. The test probes that replica directly, because Kubernetes
can retain an older ready replica until its replacement is ready. The disabled
replica must refuse data and readiness, and the incomplete rollout must make the
product unavailable. Re-enabling the flag restores full rollout readiness.

Deleting the product must remove its registry entry while retaining the source
container, connector Deployment, and credential Secret. The suite checks that the
Deployment and Secret have no product ownership reference before deletion, retain
their UIDs afterward, and have no pending deletion. A final export request checks
that the retained connector still works.

## Bounds and evidence

Each assertion has a deadline and reports its phase. Waits share a 45-minute
acceptance deadline, reserving time inside the hosted 50-minute job for cleanup.
Source failure detection
includes Kubernetes probe thresholds before the controller's polling interval;
Secret projection has an independent propagation delay. There is no universal
30-second end-to-end convergence claim. The script reports total elapsed runtime
and selected status/events/controller logs on failure, without dumping Secret
values, generated keys, or kubeconfig contents.

The independent TLS contract fixture supports publication outage/recovery while
the authenticated export stays healthy. Contract checks cover disabled observation,
exact-resource RBAC and revocation, changed output URLs, disabled probe execution,
monitor network isolation, metrics during outages, and reference removal without
workload deletion. `ContractsReady` and aggregate registry readiness follow the
contract while `ConnectorReady` stays true.

The same cluster applies the documented three-product composition example. It
checks both release-flag states, API lineage, breaking and compatible upgrades,
cycle diagnosis without status-write loops, missing ports, producer deletion and
recreation, and clearing lineage when disabled. A namespace-scoped reader verifies
that a denied cross-namespace reference cannot disclose producer metadata through
consumer status. The example describes interfaces;
it does not run their illustrative endpoints or transfer records.

The same run sources `tests/source/catalog.sh` to exercise DCAT publication through
the installed controller and chart. It verifies the default 404, explicit activation
with a stable catalog ID, dataset/service/contract relationships, annotation withdrawal,
invalid-profile and duplicate-identity rejection, recovery, and rollback to 404.
Its [dataset example](examples/dcat-product.yaml) publishes illustrative URLs; the
Kubernetes catalog check does not query those endpoints. The separate
`TestCatalogIndependentConsumer` Go test expands the catalog HTTP response with an
independent JSON-LD processor whose document loader rejects network access, then
discovers and queries the actual local TLS demo product. See the
[DCAT catalog guide](dcat-catalog.md) for the profile and limits.

The hosted result is a controlled integration proxy. Platform rollout acceptance
and release-flag retirement remain in issues #46, #49, #101, #113 and
[#123](https://github.com/devantler-tech/data-product-controller/issues/123).
The DCAT activation/retirement review is due 2026-10-30. The contract probe
uses a real TLS endpoint on the private test network; this does not prove production
public-route reachability. The controller never fetches its data-plane URLs.

The engine modules exercise SQL/native, Document/native and Graph/native admission and observation with synthetic
external CRDs. Document acceptance uses the documented Percona 1.23.0 fields, exact-name RBAC,
both default-off gates, missing source/publication, replica loss/recovery, privileged-user rejection,
permission revocation, source recreation, independent password rebinding/rotation and retention
after product deletion. Graph acceptance uses frozen checksums derived from the pinned ArangoDB
1.4.5 typed API. It checks both default-off gates, real API-server admission, exact-name grants,
missing source/publication, stale status after a specification change, current-spec recovery,
permission revocation, source recreation, independent password ownership rebinding/rotation,
rollback and deletion retention. The versioned publication describes synthetic application
intent; the test does not authenticate to ArangoDB.

Each engine module has a shared eight-minute deadline. The hosted job has a 50-minute ceiling;
the source, composition, catalog and SQL checks retain their assertions and the Document and
Graph modules each retain an independent eight-minute budget. These tests run the
real controller and Kubernetes API; they do not install CloudNativePG, Percona or ArangoDB, run databases,
verify actual credentials or prove operator-owned lifecycle. That acceptance remains in
[#38](https://github.com/devantler-tech/data-product-controller/issues/38); engine gate rollout and
retirement remain in [#128](https://github.com/devantler-tech/data-product-controller/issues/128).
Real Graph queries and effective application grants remain in
[#157](https://github.com/devantler-tech/data-product-controller/issues/157).
