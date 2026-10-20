---
title: Check declarations
description: Validate one or more product manifests locally before publishing.
---

`product-check` reads explicitly named YAML or JSON files. It uses the delivered CRD
to check declarations and can produce public descriptor previews and dependency findings.
It does not contact Kubernetes, follow links, read Secrets or apply resources.

## Check files

From a source checkout:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file website/public/examples/harbour-product.yaml
```

The command is default-off. Set `PUBLISHER_PREFLIGHT_ENABLED=true` explicitly.
For more than one file, repeat `--file`; this is useful when producers and their
consumer are stored separately.

## Check a composition

The repository includes three products: two producers and a consumer with named inputs.

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file docs/examples/composition.yaml
```

Expect three validated products and a required operational feature of `composition`.
The report tells the operator what is needed. It does not enable that feature.

## Use a report

Add `--format json` for a machine-readable report. A valid report does not establish
current readiness, credentials, endpoint access or a complete deployment.
Keep the command's exit status with its output; partial or failed output is not a pass.

The [publisher reference](https://github.com/devantler-tech/khoist/blob/main/docs/publisher-preflight.md)
documents exit codes, bounds, namespace handling and the independent browser report viewer.
The released image also contains `/product-check`; use a verified digest and the
documented read-only container invocation when you do not want to build from source.
