# Independent EDC import acceptance

This test uses Eclipse EDC Connector **0.18.1**, source
`3a22ff4aa90213a564309b5a05851e6d7f27f9ff`, to import an unchanged DSP export.
It runs real Titanium JSON-LD expansion and the DSP 2025 catalog and ODRL
transformers. It does not boot a Connector or implement IAM, negotiation or transfer.

The independently authored `expected.json` compares catalog, participant,
dataset, offer and assigner identities; metadata; every supported rule category;
string operands including `007` and `true`; formats; services and endpoint links.
It also compares expanded distribution IDs independently of the typed model.
Two Harbour outputs intentionally share format and service while retaining
different IDs. The projection keeps their multiplicity.

EDC's typed `Distribution` model has no identity member. The observed receipt
therefore says `compatibility: incompatible` and names `typed-distribution-id`.
A successful test confirms this measured limitation; it is **not** a full
interoperability approval. Changing an output ID or collapsing equal signatures
must fail the oracle. Neither #389 nor #126 is satisfied by this checkpoint alone.

## Execution and provenance

`tests/source/catalog.sh` installs the two synthetic products in its existing
disposable cluster, reads the actual enabled controller through an explicitly
scoped Kubernetes service proxy, then runs `tests/source/edc-import.sh`.
The runner verifies the signed v4.0.0 image, chart, source and publisher tuple
before executing that image's packaged exporter with networking disabled.
The consumer reads the exact export bytes and records their SHA-256.

The runner uses one immutable official Maven 3.10.0 / Temurin 17 linux/amd64
toolchain for three separate, nonroot and resource-bounded container phases.
Only dependency resolution has network access; it uses public Maven Central,
strict checksum checking and a fresh repository without host Maven settings.
The complete set and bytes of 54 runtime JARs must match `dependencies.sha256`.
Compilation then runs without networking, with the locked JARs read-only and
only its output directory writable. The importer runs offline with both JARs
and compiled classes read-only. No custom image or scanner exception is needed,
and no Java dependency is added to the controller image or its runtime.

The final receipt binds the toolchain reference and observed image ID, importer
source, POM, settings, dependency lock and complete compiled-class manifest.
Artifact fault tests reject missing, changed, extra and special files and
incomplete hash reads before accepting that evidence. The classes and JARs
are checked again after import controls.
Hash verification and receipt generation share the same absolute deadline as
the container phases. Offline delayed-reader controls must time out without
emitting a receipt, and an expired allowance refuses final publication.

The import runs as UID 65532 without networking, capabilities or ambient
credentials, with bounded memory, CPU, processes and one shared deadline.
Only the two existing hash-pinned official contexts can load. Nested and foreign
HTTP(S), file and jar contexts are rejected before invoking a document loader.
Closed-profile checks reject extra scalar values, unsupported graph keywords
and duplicate offer identities before EDC could silently reduce them.
The runner also exercises semantic corruption, real expansion and transformation
failures, malformed/oversized/deep input, special files and symlinks.
A negative control requires importer exit 1 and no receipt; a failed container
launch or timeout cannot pass as a rejected document.

For a local producer fixture from the repository root:

```sh
go run ./tests/dataspace/producer > /tmp/edc-controller-catalog.json
bash tests/source/edc-import.sh /tmp/edc-controller-catalog.json
```

The standalone runner requires Docker, Cosign, Helm, yq, jq, timeout and the
release verifier's normal prerequisites. The local generator uses the real
DCAT handler with an **in-memory Kubernetes reader**. That local path proves
the supplied fixture's producer/export/import behavior, not a live controller
or production rollout. Only the hosted cluster phase supplies the live-snapshot
receipt. Neither path negotiates an offer or accesses data.

When upgrading EDC, resolve the complete runtime graph with the exact Maven
dependency-plugin version in the runner, regenerate the checksum lock from
that graph, and check the published dependency/source metadata. Reassess typed
identity losses and the complete independent oracle rather than accepting new
output automatically. The copied official contexts retain their Apache 2.0
license; dependency JARs retain their own embedded notices.
