# Check a product before publishing

`product-check` checks a selected local YAML or JSON file before it enters GitOps.
It validates declarations and creates public descriptor previews. It never
contacts Kubernetes, follows a URL, reads a Secret, or applies a resource.

The command is included at `/product-check` in the released controller image.
It is default-off under the `publisher-preflight` OpenFeature gate. Set
`PUBLISHER_PREFLIGHT_ENABLED=true` explicitly to try it. The disabled command
exits before opening the selected file.

For repository development:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file docs/examples/composition.yaml
```

For a released image, replace the digest with an image verified against the
trusted publisher described in the [README](../README.md):

```bash
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --user 65532:65532 \
  --memory 256m --cpus 1 --pids-limit 64 \
  --env PUBLISHER_PREFLIGHT_ENABLED=true \
  --mount "type=bind,src=$PWD/docs/examples,dst=/input,readonly" \
  --entrypoint /product-check \
  ghcr.io/devantler-tech/data-product-controller@sha256:<verified-image-digest> \
  --file /input/composition.yaml --format json
```

The three-product example resolves both supplied producer outputs and checks
their declared protocol and version requirements. Its report requires the
`composition` operational feature. That requirement does not enable the feature
or establish live readiness.

## Results

| Exit | Meaning                                                                     | Public previews             |
| ---- | --------------------------------------------------------------------------- | --------------------------- |
| `0`  | Declarations passed; supplied dependencies were resolved                    | Present                     |
| `1`  | Invalid declarations, exceeded limits, disabled feature, or command failure | Absent on an invalid report |
| `2`  | Declarations passed, but a producer is absent from the local bundle         | Absent                      |

The default text output gives document numbers and bounded finding codes.
`--format json` returns `data-product-preflight/v1` with `valid`, `complete`,
`products`, `requiredFeatures`, `diagnostics`, and, only for complete valid
bundles, `descriptors`. Parsing and validation errors contain fixed public
wording rather than rejected field names, values, local paths or provider errors.

Document order controls preview order. Diagnostics never partially publish a
bundle. A preview uses `data-product-descriptor/v1`; its `ready` value is false,
both generation values are zero, and applicable health dimensions are
`unobserved`. Submitted status and controller generations cannot become evidence.
Source, Secret and workload references are omitted from the public projection.

A preview still contains publisher-declared public names and HTTPS endpoints.
Review that public metadata before sharing it. Do not put credentials or private
information in those declarations.

## What is checked

The command embeds the generated CRD shipped with the image and evaluates its
OpenAPI schema, CEL rules and list uniqueness, plus Kubernetes object metadata
rules. It uses the create-time declaration profile. It does not evaluate cluster
admission policies, authorization, update ratcheting, existing resources or the
installed cluster's CRD version. See Kubernetes's [custom resource validation
documentation](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation).

Additional publication checks use the registry's public URI, HTTPS, field and
encoded descriptor profile. UI declarations must satisfy the portable manifest,
exact host-origin and capability rules. Contract probes must select an output
declared by the same product. Required operational features are listed for
sources, providers, connectors, probes, composition, UI grants and opted-in DCAT
datasets.

Each product needs an explicit namespace. Use `--namespace products` only to
fill documents that omit `metadata.namespace`; it never overrides a declared
namespace. Namespace/name pairs and stable product IDs must be unique within
the bundle.

Composition resolves only supplied products in the consumer's namespace. A
missing producer is unresolved, not absent from Kubernetes. A supplied producer
must declare the selected output and satisfy the controller's stable-version
and protocol rules. Cycles and excessive depth fail. Contract contents, query
results, reachability, credentials, access grants and live readiness require
independent evaluation after deployment.

## Limits and rollout

Input is one caller-selected regular file: YAML documents or JSON objects with
the exact `data.devantler.tech/v1alpha1` DataProduct kind. Kubernetes Lists,
unknown fields, duplicate keys, aliases, anchors, custom tags and trailing
malformed content are rejected. No document chooses another input path.

Bounds are 2 MiB of input, 256 products, 64 document nesting levels, 1,024 total
composition inputs, and fewer than 64 composition edges on any supplied path.
Validation has a five-second context and a shared CEL cost budget. Reports retain
at most 128 diagnostics. Public fields are limited to 16 KiB, each encoded
descriptor to 64 KiB, and a complete encoded report to 2 MiB. Select a smaller
bundle if a bound is exceeded.

Source integration runs the real packaged command with networking disabled.
The normal release workflow repeats that smoke test after verifying the
published image's signature, source revision and digest. Those checks prove the
released command works; production adoption remains separate.

Independent publisher evaluation and retirement of the temporary gate are
tracked in [#205](https://github.com/devantler-tech/data-product-controller/issues/205).
