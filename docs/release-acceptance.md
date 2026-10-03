# Released artifact acceptance

`scripts/verify-release-artifacts.sh` verifies a declared public controller release
before it can be used as an acceptance baseline. Supply immutable image/chart
digests, the release tag, full source revision, reviewed publisher workflow commit,
and target platform (`linux/amd64` or `linux/arm64`). The output directory must be
new; its parent must already exist.

```bash
bash scripts/verify-release-artifacts.sh \
  --tag v1.12.0 \
  --source-sha ff7ddcf264f8ce4813ccdb6675a45b4d1e6d1b78 \
  --image-digest sha256:62350d68a853708357f463b74ea9ff847d94100d042125b4f2d0c6f25da54886 \
  --chart-digest sha256:9d57e4c7b26664a560a2b72cb8f89dfee4c1466223081a039579cc9ffa3d8575 \
  --publisher-sha df7fd4f83edade31c121a9de563d9a7b9b1f900d \
  --platform linux/amd64 --timeout 180 --output-dir /tmp/dpc-release-evidence
```

Requires Cosign, Docker with Buildx, Helm, jq, yq and GNU `timeout` (or `gtimeout`).
The repositories and signing identities are fixed to the owned controller/image
and chart publishers. Signatures must bind the supplied digest, owned caller
repository, release tag and source revision. The platform index must select exactly
one matching Linux manifest, the pulled image must carry the expected OCI source
revision, and the real packaged chart metadata must match the declared version.
The command rejects incomplete verification, mutable references, platform ambiguity,
revision/version drift, reused evidence directories and an exhausted shared deadline.

Public registry reads use a temporary empty credential configuration, with no
registry writes, release credentials or changes to the operator's Docker/Helm
settings. Evidence is private (`0700` directory, `0600` files). Only success writes
`release.json` with `complete:true` and the verified runtime digest, alongside
`release-chart.tgz`. Stdout reports fixed failure classes or public artifact identity;
diagnostic output remains in the private directory.

The required source integration job verifies this baseline against the real public
registry and installs it through Helm in its existing disposable cluster. It explicitly
updates the CRD before the candidate upgrade, then exercises a real rollback,
query recovery and retained independent source/credential identities. Its candidate
is a local digest built from the PR; candidate results do not prove publication or
Platform deployment. Production rollout and release-flag retirement require their
separate reviewed deployment and readback evidence.
