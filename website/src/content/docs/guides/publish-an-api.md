---
title: Publish an API
description: Register the metadata of an independently operated HTTPS API.
---

Use this guide when your team already operates an API. KHoist will describe that
API; the service keeps its data, authentication, deployment and lifecycle.

## Prepare the description

Download the [example declaration](/examples/harbour-product.yaml). Replace its
`example.com` identities and URLs with values your team controls, and set the
owner and contract version. Keep `metadata.namespace: products` for the commands
below. If you choose another namespace, replace `products` in every command as
well as in the declaration. The example URLs are placeholders.

```yaml
outputs:
  - name: observations
    protocol: OpenAPI
    url: https://api.example.com/observations
    contractUrl: https://api.example.com/openapi.json
    mediaType: application/json
```

Give each output a stable name. Clients select that name, so changing it changes
the product contract. Output and contract links must use HTTPS.
Never include passwords, access tokens, connection strings or records in the resource.

## Validate before applying

From a source checkout, after saving your edited declaration as `product.yaml`:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check --file product.yaml
```

Correct any errors. A pass validates public declarations, not endpoint access.
See [check declarations](/guides/check-declarations/) for reports and exit codes.

## Publish to Kubernetes

With the controller installed and your intended context selected:

```bash
kubectl create namespace products
kubectl apply -f product.yaml
kubectl --namespace products get dataproduct harbour-observations
```

Create the namespace only if it does not already exist. Adjust the lookup to your
chosen product name. Inspect the resource's conditions and its registry entry.

Without an independent connector or contract observer, readiness is a metadata
observation. It does not mean KHoist queried the service successfully.
Use the [readiness troubleshooting guide](/troubleshooting/) to interpret conditions.

## Update or withdraw

Update the declaration through your normal reviewed deployment workflow.
Version the product contract deliberately when changing output semantics.
To withdraw this example's metadata:

```bash
kubectl --namespace products delete dataproduct harbour-observations
```

This does not delete the independently operated API or its data.
