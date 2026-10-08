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

For an acceptance trial with intentionally expected readiness failure, replace
that type's `--condition` with `--expected-condition TYPE:STATUS:REASON`:

```bash
# Add these selectors to an invocation with the same explicit resource identities.
--expected-condition Ready:False:ConnectorAccessDenied \
--expected-condition ConnectorReady:False:ConnectorAccessDenied
```

This option accepts the same five condition types and the exact statuses `True`,
`False` and `Unknown`. The reason must match exactly and use the API's ASCII
Reason grammar: 1–1,024 characters, beginning with a letter and ending with a
letter, digit or underscore; internal letters, digits, underscores, commas and
colons are permitted. Only the first two colons separate the selector fields.
The combined selectors must still include `Ready` and must name each type once;
conflicting or repeated selectors fail before any cluster read. Expectations
apply to every selected product.

An explicit expectation never changes workload requirements. Selected Deployments,
ReplicaSets and Pods must still have complete positive readiness and verified
runtime identity. Use this option for a product condition failure while those
selected workloads remain healthy. It does not observe a zero-capacity or partial
workload phase, grant access, induce a fault, or establish its cause. Aggregate
`Ready` can reflect an earlier source or dependency failure, so choose each
condition's expected reason independently from the reviewed trial.

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

Success requires exactly one condition of each selected type at the current
product generation. A `--condition` selector requires `True`; an explicit
expectation requires its exact status and reason in both snapshots.
Every Deployment must have a current observed generation,
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
identity, generation, revision, ownership, replica counts, condition statuses,
container image and runtime identity. Explicit reason expectations retain only a
match boolean; raw reasons and the caller's requested reason are not written.
Product specs, condition prose, unrelated
annotations, environment, volumes and Secret values are excluded before writing.
Keep these files private. A legacy invocation's standard output contains only
completeness, product, Deployment and Pod counts, plus a fixed failure code on
failure. Exit zero means
complete; other exits fail closed. An opt-in invocation additionally reports
`"expectation":"conditions"` and `healthy`, which is false when a selected
expected status is `False` or `Unknown`. Its `complete:true` means the requested
state was observed, including an intentional failure; it is not healthy rollout
or adoption evidence. Invocations using only `--condition` keep the original
summary shape. The evidence is a bounded observation across
several reads, not an atomic Kubernetes snapshot or a guarantee of later health.

Run the synthetic boundary tests with:

```bash
bash scripts/observe-rollout.test.sh
```

They invoke the real observer and constrain the external kubectl boundary to
explicitly scoped, read-only calls. They cover stale and duplicate conditions,
zero or partial replicas, current and old ownership chains, runtime identity,
terminating and foreign-owned Pods, denied or incomplete reads, deadline
enforcement, and changes during the final read. Explicit expectation cases also
cover false, unknown and recovered conditions, reason parsing and privacy,
contradictory observations and final status or reason changes.
