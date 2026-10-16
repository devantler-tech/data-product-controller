# Released artifact acceptance

`scripts/verify-release-artifacts.sh` verifies a declared public controller release
before it can be used as an acceptance baseline. Supply immutable image/chart
digests, the release tag, full source revision, reviewed publisher workflow commit,
and target platform (`linux/amd64` or `linux/arm64`). The output directory must be
new; its parent must already exist.

`--profile data-product-controller` is the default historical identity. Explicit
`--profile khoist` selects the KHoist caller repository, controller image, chart package,
chart signing identity and chart metadata name together. Only these two fixed profiles
are accepted; an unknown or repeated selection fails before registry or signer access.
A failed verification never retries another profile. Choose the profile of the tagged
release, rather than inferring it from the repository's current name or a redirect.
The shared image publisher commit remains independently supplied.

```bash
bash scripts/verify-release-artifacts.sh \
  --profile data-product-controller \
  --tag v1.12.0 \
  --source-sha ff7ddcf264f8ce4813ccdb6675a45b4d1e6d1b78 \
  --image-digest sha256:62350d68a853708357f463b74ea9ff847d94100d042125b4f2d0c6f25da54886 \
  --chart-digest sha256:9d57e4c7b26664a560a2b72cb8f89dfee4c1466223081a039579cc9ffa3d8575 \
  --publisher-sha df7fd4f83edade31c121a9de563d9a7b9b1f900d \
  --platform linux/amd64 --timeout 180 --output-dir /tmp/dpc-release-evidence
```

Requires Cosign, Docker with Buildx, Helm, jq, yq and GNU `timeout` (or `gtimeout`).
The repositories and signing identities are fixed by the selected profile to the owned
controller/image and chart publishers. Signatures must bind the supplied digest, owned caller
repository, release tag and source revision. A platform index must select exactly
one matching Linux manifest; a single-platform manifest uses its signed digest
directly. The pulled image must report the requested OS and architecture and carry
the expected OCI source revision. The real packaged chart metadata must match the
declared version.
The command rejects incomplete verification, mutable references, platform ambiguity,
revision/version drift, reused evidence directories and an exhausted shared deadline.

Public registry reads use a temporary empty credential configuration, with no
registry writes, release credentials or changes to the operator's Docker/Helm
settings. Evidence is private (`0700` directory, `0600` files). Only success writes
`release.json` with `complete:true`, the explicit `releaseProfile`, fully qualified
`imageReference` and `chartReference`, and the verified runtime digest, alongside
`release-chart.tgz`. Stdout reports fixed failure classes or public artifact identity;
diagnostic output remains in the private directory.

The required verifier fixtures exercise both profiles with controlled signer and registry
responses and real Helm archives. They reject mixed identities, partial failures and missing
receipt bindings; they do not prove real KHoist publication or deployment. Historical lifecycle
checks explicitly require the original profile, including the signed v5 rollback receipt
and the signed v4 exporter used by the offline EDC importer. These consumers reject
missing or mixed profile/reference receipts before requesting images or installing charts.
The independently versioned AGE image family and its verification are separate from this selector.

The required source integration job verifies this baseline against the real public
registry and installs it through Helm in its existing disposable cluster. It explicitly
updates the CRD before the candidate upgrade, then exercises a real rollback,
query recovery and retained independent source/credential identities. Its candidate
is a local digest built from the PR; candidate results do not prove publication or
Platform deployment. Production rollout and release-flag retirement require their
separate reviewed deployment and readback evidence.

The API-only rollback profile additionally verifies signed v5.0.0, then installs
its workload chart and its two UI-free product declarations in the same owned
cluster. Candidate upgrade, actual rollback to each release's saved revision and
candidate restoration must retain exact chart/image identities, stored manifests,
live API access and independent resource identities. Legacy v1.12.0 CRD-upgrade
and v4.0.0 UI-metadata rollback controls remain separate. This disposable installed
proof does not establish a production rollback baseline or permit stricter
admission before coordinated Platform adoption.
