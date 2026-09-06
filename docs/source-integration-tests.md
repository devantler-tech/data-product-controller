# Source integration acceptance

`tests/source/run.sh` exercises the real controller, HTTP export connector, and
registry together in an ephemeral KSail cluster. It uses Vanilla Kubernetes on
Kind/Docker with Cilium enforcing NetworkPolicy. Its synthetic source serves TLS
with a generated one-day certificate and test-only credentials.

The source runs in a separate container on Kind's private Docker network, exposed
to the connector through a headless Service and EndpointSlice. It has no published
host port. This exercises the chart's external-source CIDR rule: Cilium's default
[CIDR policy behavior](https://docs.cilium.io/en/stable/security/policy/layer3/)
does not match Cilium-managed Pod identities. The suite keeps that default intact.

Run it on a machine with Docker, KSail 7.182.6, kubectl, Helm, jq, yq, and OpenSSL:

```bash
bash tests/source/run.sh
```

The command creates a uniquely named local cluster, temporary kubeconfig, and local
registry. It never selects the operator's current Kubernetes context. Cleanup
deletes the source container, that cluster, and its storage on normal exit or
handled termination. Allow several gigabytes of free disk space for Kubernetes and image
builds. CI runs the same command on a disposable hosted runner, verifies the KSail
download checksum, grants only repository read access, and limits the job to
30 minutes.

The harness does not use KSail's `--ttl`: that mode keeps the create command in the
foreground until automatic destruction, which would prevent the assertions from
running. Hosted-runner disposal is the backstop for uncatchable termination in CI;
after an uncatchable local termination, remove the named cluster and source
container manually.

Both images are built from the checked-out source and pushed to the local
registry. The chart uses the resulting immutable image digest. The harness renders
the chart, mounts the generated CA through a test-only manifest filter, and applies
the resources. It does not create a Helm release. Certificate and hostname
verification stay enabled. Production chart defaults and deployment configuration
are unchanged.

The consumer has loopback health probes and denies incoming network traffic.
Its checked-in local image tag is replaced with the actual pushed digest before
apply. Two artifact-scoped scanner exceptions describe that dynamic digest and
private test registry; the suppression contract pins both exact rule/path pairs.
Production scanner exceptions are unchanged.

The fixture build retains the repository's Go module version so its HTTP routing
semantics match unit tests. The harness passes the generated Kind configuration
explicitly and rejects a cluster that also installed Kind's default CNI.

## Observed behavior

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

Each assertion has a deadline and reports its phase. Source failure detection
includes Kubernetes probe thresholds before the controller's polling interval;
Secret projection has an independent propagation delay. There is no universal
30-second end-to-end convergence claim. The script reports total elapsed runtime
and selected status/events/controller logs on failure, without dumping Secret
values, generated keys, or kubeconfig contents.

The hosted result is a controlled integration proxy. Platform rollout acceptance
and release-flag retirement remain in issues #46 and #49. Public-route contract
reachability is outside this test's claim: the contract is fetched over the actual
in-cluster connector Service, and the controller never fetches its data-plane URL.
