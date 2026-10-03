# Read-only rollout observation

`scripts/observe-rollout.sh` observes explicitly named DataProducts and Deployments
after a reviewed deployment. It needs Bash, jq, kubectl and an existing kubeconfig.
The caller supplies the context and namespace; the script never changes either.

```bash
bash scripts/observe-rollout.sh \
  --kubeconfig /private/operator/kubeconfig --context acceptance \
  --namespace products --product existing-export \
  --deployment dpc:controller \
  --deployment dpc-http-source:http-source \
  --deployment dpc-contract-probe:contract-probe \
  --condition Ready --condition ConnectorReady --condition ContractsReady \
  --image ghcr.io/devantler-tech/data-product-controller@sha256:<verified-index> \
  --runtime-digest sha256:<verified-platform-manifest> \
  --timeout 120 --evidence-dir /private/operator/new-observation
```

Replace the digest placeholders with complete lowercase SHA-256 digests. The
image must be immutable. The caller verifies its provenance and supplies only
permitted runtime digests from that image's index and selected target platform.
The index in `--image` is also accepted as a runtime digest. The observer makes
no registry requests and does not establish artifact provenance itself.

Repeat `--product` and `--deployment NAME:CONTAINER` for each exact identity.
Each selected Deployment must use the supplied image for the named container.
`--condition` applies to every selected product and must include `Ready`.
Supported additional conditions are `SourceReady`, `ConnectorReady`,
`ContractsReady` and `CompositionReady`. Use separate invocations when products
require different condition sets or workloads use different images.

The timeout is required and accepts 1–600 seconds. There are at most 64 products,
64 Deployments and eight additional runtime digests per invocation. Namespace
inventories exceeding 4,096 ReplicaSets or Pods are incomplete. These bounds keep
the observer suitable for a dedicated acceptance namespace.

The script performs only named GETs of `dataproducts.data.devantler.tech` and
`deployments.apps`, followed by namespace-scoped lists of `replicasets.apps` and
`pods`. Every call carries the supplied kubeconfig, context and namespace and an
HTTP request timeout of at most five seconds, reduced to the remaining invocation
budget. A process-group watchdog bounds Kubernetes calls and metadata evaluation,
including a stalled credential plugin. An unreadable, forbidden, empty or malformed response
fails the observation. Incomplete readiness retries within the same absolute
deadline; a deadline can never produce success.

Success requires exactly one true condition of each required type at the current
product generation. Every Deployment must have a current observed generation,
positive desired replicas, and all desired replicas updated, ready and available.
Its current revision must identify exactly one live ReplicaSet owned by the exact
Deployment UID, with current observed generation and full replica readiness.
Every Pod owned by that Deployment's ReplicaSets must belong to that current
ReplicaSet, be live and Ready, and run the named container with the exact image
and a permitted runtime digest. Old, terminating or partially ready replicas
cannot supply readiness. Unrelated namespace workloads do not contribute to the
result. Final reads refresh the named resources and namespace workload inventories,
recheck full readiness and runtime identity, and bind the product UID/generation,
Deployment UID/generation/revision and current ReplicaSet UID/generation to the
original snapshot. A healthy replacement Pod under that same ReplicaSet is valid.

The caller needs those read permissions through its existing identity. The
observer requests no Secrets, executes no container commands, and never mutates
cluster resources or identity permissions. Platform owns production rollout and
readback authorization; this script can also observe the disposable hosted source
acceptance cluster.

The evidence directory must be a new absolute path. It is created with mode
`0700`; files use `0600`. The retained JSON is a metadata projection: resource
identity, generation, revision, ownership, replica counts, condition booleans,
container image and runtime identity. Product specs, condition prose, unrelated
annotations, environment, volumes and Secret values are excluded before writing.
Keep these files private. Standard output contains only completeness, product,
Deployment and Pod counts, plus a fixed failure code on failure. Exit zero means
complete; other exits fail closed. The evidence is a bounded observation across
several reads, not an atomic Kubernetes snapshot or a guarantee of later health.

Run the synthetic boundary tests with:

```bash
bash scripts/observe-rollout.test.sh
```

They invoke the real observer and constrain the external kubectl boundary to
explicitly scoped, read-only calls. They cover stale and duplicate conditions,
zero or partial replicas, current and old ownership chains, runtime identity,
terminating and foreign-owned Pods, denied or incomplete reads, deadline
enforcement, and changes during the final read.
