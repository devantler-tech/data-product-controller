# Offline Dataspace Protocol catalog export

`dsp-catalog` combines a local controller DCAT snapshot with public provider
bindings and writes a DSP 2025-1 catalog JSON-LD document to standard output.
The command is packaged at `/dsp-catalog` in the controller release image.
It is independent of the running controller and makes no network or Kubernetes
requests. It does not read credentials or product records.

This release supports the **catalog data model** in
[DSP 2025-1-err2](https://eclipse-dataspace-protocol-base.github.io/DataspaceProtocol/2025-1-err2/).
It does not serve the DSP HTTP protocol or implement contract negotiation,
authorization, policy enforcement, transfer, federation or connector import.
A provider owns those capabilities and must verify its supplied bindings.
Listing an offer does not grant access or prove that a service is available.

## Run the example

The OpenFeature `dsp-catalog-export` flag defaults off. Set
`DSP_CATALOG_EXPORT_ENABLED=true` explicitly for each invocation. Unset or `false`
refuses the export before opening either input. Other values are errors.

From a checkout:

```bash
go build -o /tmp/dsp-catalog ./cmd/dsp-catalog
DSP_CATALOG_EXPORT_ENABLED=true /tmp/dsp-catalog \
  --catalog docs/examples/dsp-catalog/catalog.json \
  --bindings docs/examples/dsp-catalog/bindings.json > /tmp/dsp-catalog.json
```

For the release image, replace `RELEASE_DIGEST` with its verified immutable digest:

```bash
docker run --rm --network none --read-only \
  -e DSP_CATALOG_EXPORT_ENABLED=true \
  -v "$PWD/docs/examples/dsp-catalog:/input:ro" \
  --entrypoint /dsp-catalog \
  ghcr.io/devantler-tech/data-product-controller@RELEASE_DIGEST \
  --catalog /input/catalog.json --bindings /input/bindings.json
```

Both arguments must name readable local regular files. Stdin, URLs and streamed
inputs are unsupported. All validation completes before output starts; an input
failure exits nonzero, writes a value-free diagnostic to stderr and writes no
catalog bytes. A downstream output-device failure can still interrupt writing;
check the exit status before publishing the file. Shell redirection truncates its
destination before execution, so write to a new temporary file and move it into
place only after success when replacing a published catalog.

## Prepare real inputs

Obtain a snapshot of the controller's enabled `/api/v1/catalog` endpoint through
your normal authorized tooling; see the [DCAT guide](dcat-catalog.md). The exporter
accepts that specific profile with its exact embedded context, not arbitrary
JSON-LD or remote contexts. Editing the context, adding extension fields, changing
types or leaving dangling source service relationships fails validation.

Use the [example binding file](examples/dsp-catalog/bindings.json) as a starting
point. Its example addresses, offer IDs and format are placeholders, not an
existing provider or a standardized transfer format.

| Binding                             | Meaning                                                                                                                                   |
|-------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `version`                           | Exactly `dsp-catalog/v1`.                                                                                                                 |
| `catalogId`                         | Stable identity of this DSP catalog, distinct from the source catalog.                                                                    |
| `participantId`                     | Provider identity; must match every offer's explicit assigner.                                                                            |
| `service.id`, `service.endpointURL` | Stable service identity and HTTPS base of the provider's DSP 2025-1 negotiation/transfer service. No user information, query or fragment. |
| `datasets[].id`                     | Exact dataset `@id` from the source snapshot.                                                                                             |
| `distributions[].sourceId`          | Exact source distribution `@id` within that selected dataset.                                                                             |
| `distributions[].id`                | Distinct stable identity for the DSP representation. Do not reuse the direct-query distribution ID.                                       |
| `distributions[].format`            | Explicit absolute IRI of a transfer format supported by the provider. A source media type such as `application/json` is insufficient.     |
| `offers`                            | Provider-known ODRL Offers with distinct `@id`, explicit `@type: Offer`, matching `assigner` and supported rules.                         |

All selected datasets need at least one distribution and one offer. Unbound
datasets and outputs are omitted. A stale or repeated selection fails the whole
export. An explicit empty `datasets: []` emits the provider catalog and required
service, with no `dataset` property. Omission of `datasets` is an error.

Dataset identity, title, description and version survive the projection. Only
explicit provider bindings supply DSP services, distributions and policies.
Ownership metadata, query URLs and source media types are not translated into
authority, negotiation endpoints or transfer support. Source and emitted resource
IDs must be globally distinct except for intentionally preserved dataset IDs;
the participant identity cannot collide with resources.

IDs use HTTPS or the restricted URN profile described in the DCAT guide. Actions,
left operands and formats may also use HTTP vocabulary IRIs, such as the ODRL
namespace. No IRI is fetched. Whitespace, control characters and invalid raw URI
characters are rejected.

## Supported policy profile

Each Offer requires at least one nonempty `permission` or `prohibition` array.
An `obligation` array may accompany those rules. Each rule has an `action` equal
to `use` or a full vocabulary IRI. Optional `constraint` arrays contain only
atomic constraints: an absolute `leftOperand`, a supported compact `operator`,
and a nonempty **string** `rightOperand`. Operators are `eq`, `gt`, `gteq`, `lt`,
`lteq`, `neq`, `isA`, `hasPart`, `isPartOf`, `isAllOf`, `isAnyOf`, `isNoneOf`.
Strings remain strings; the exporter neither evaluates nor coerces operands.

Policies and all supported rules are preserved. Embedded targets, nested
contexts, profiles, assignees, duties, compound constraints, typed/object/array
right operands, unknown actions in compact form, and other extension fields are
rejected rather than dropped. Providers needing those constructs must use a
different exporter or wait for a separately reviewed profile extension.

## Limits and validation

| Resource        | Limit                                                                                                             |
|-----------------|-------------------------------------------------------------------------------------------------------------------|
| Source snapshot | 2 MiB; 256 datasets; 1,024 distributions and 1,024 services.                                                      |
| Binding file    | 1 MiB; 256 selected datasets; 1,024 distributions in total.                                                       |
| Policy          | 16 offers per dataset; 32 rules per category per offer; 16 constraints per rule.                                  |
| JSON            | 16 KiB per decoded string; 32 nesting levels; one value; no duplicate keys, case aliases, nulls or lossy Unicode. |
| Encoded result  | 2 MiB including trailing newline.                                                                                 |

Offline tests validate exports against the unmodified official schemas and use
an independent JSON-LD processor with pinned contexts to check RDF relationships.
The producer-to-exporter integration tests use the actual DCAT handler. A
reference product with multiple outputs and an unselected dataset is exported
through explicit provider bindings, reconstructed from RDF, and compared with
a hand-written complete graph. This checks identity, metadata, selected service
relationships, every supported rule category and string-valued constraints;
negative controls detect lost policies and operand coercion. None of these
checks establishes a provider's runtime interoperability or enforcement.

The hosted Kubernetes integration job also runs `tests/source/dsp-catalog.sh`
against the exact image it builds, with networking disabled. This checks the
packaged command, default-off behavior, enabled example and empty output on
invalid bindings before the controller's cluster lifecycle tests proceed.
After publication, the release workflow verifies the image signature against the
pinned publisher and this repository's exact release tag and commit, checks the
image's source revision, and runs the same smoke test against its immutable digest.

[ADR 0009](adr/0009-offline-dsp-catalog-export.md) records the design.
[Adoption and flag retirement #126](https://github.com/devantler-tech/data-product-controller/issues/126)
has an owner and a review on **2026-10-30**. Enabling or removing the flag requires
that follow-up's provider/consumer evidence; the date itself changes no behavior.
