---
title: Consume a product
description: Discover public metadata and call an API from a consumer-owned application.
---

An application uses KHoist to discover a product's named outputs and contracts.
It then calls the product's API directly. The application owns its UI,
authentication, authorisation, transport and error handling.

## Discover the output

Discovery is an optional operator-enabled capability. With it enabled, request a
product by namespace and Kubernetes name:

```bash
curl --fail --max-time 10 --max-filesize 65536 --proto '=https' \
  --output descriptor.json \
  'https://registry.example.com/api/v2/products/products/harbour-observations'
```

Replace the hostname and product reference. This URL describes a product;
it is not the query endpoint. The [registry API](/reference/registry-api/) describes
available routes and the discovery gate.

## Approve the endpoint independently

Select an output by name and read its standard contract. Obtain the approved query
endpoint and any credentials from your own application's configuration or the owner.
A URL in public metadata grants no permission to send credentials or requests.
Check that the selected output matches the approved destination before using it.

Keep publisher text inert. Do not load publisher scripts or frame a product UI.
Readiness metadata is an observation and cannot guarantee a later request will succeed.

## Run the independent Harbour consumer

The source checkout contains a standard-library Go consumer for the Harbour
OpenAPI/JSON profile. It accepts an independently approved literal HTTPS endpoint:

```bash
go run ./examples/api-consumer \
  --descriptor descriptor.json \
  --output observations \
  --approved-endpoint 'https://harbour.example.com/api/observations' \
  --station nordhavn
```

Replace the endpoint with your approved published Harbour service. This example
sends no credentials and rejects redirects. It requires HTTPS, so the local HTTP
demo in the quickstart is not an accepted endpoint.

To exercise it without an existing deployment:

```bash
go test ./examples/api-consumer -run TestConsumesNamedHarbourOutput -count=1 -v
```

This runs the real registry and Harbour handlers against a temporary local TLS
fixture and verifies the Nordhavn record. It does not prove production adoption.
See the [consumer reference](https://github.com/devantler-tech/khoist/blob/main/docs/api-consumer.md)
for its exact profile, limits and transport behaviour.
