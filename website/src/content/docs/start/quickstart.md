---
title: Try a data product
description: Query the Harbour example and validate a declaration without a cluster.
---

This tutorial uses Harbour observations: two fixed, synthetic measurements.
You will run its API, read the records and validate a product description.
The example does not connect to a real harbour sensor.

## Before you start

Install Git, curl and Go at the version declared in the
[repository's go.mod](https://github.com/devantler-tech/data-product-controller/blob/main/go.mod)
or newer. The first build downloads Go dependencies.

## Run the example API

In a terminal:

```bash
git clone https://github.com/devantler-tech/data-product-controller.git
cd data-product-controller
go run ./cmd/demo-product --listen-address 127.0.0.1:8080
```

Keep this terminal open. The loopback address limits the example to your machine.
If port 8080 is occupied, choose another port and update the requests below.

## Read the observations

In a second terminal:

```bash
curl --fail http://127.0.0.1:8080/api/observations
```

The response has an `items` array with stations `nordhavn` and `refshaleoen`.
Nordhavn has temperature `16.8` and salinity `14.2`. These values are examples.

Request one station:

```bash
curl --fail 'http://127.0.0.1:8080/api/observations?station=nordhavn'
curl --fail http://127.0.0.1:8080/openapi.json
```

The first response contains only Nordhavn. The second is the API contract.
The product also has its own page at `http://127.0.0.1:8080/ui`; consumer applications
can use the API directly.

## Check a product declaration

From the cloned repository in the second terminal:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file website/public/examples/harbour-product.yaml
```

Expect `Validated 1 product(s)`. This checks the declaration, including its
placeholder HTTPS URLs. It does not request those URLs, publish the product or
establish live readiness.

## Stop and continue

Press `Ctrl+C` in the API terminal. The example writes no application data.
Go retains its dependency and build caches.

You have exercised a product API and checked its metadata. Next,
[publish an independently running API](/guides/publish-an-api/) or
[install the controller](/start/installation/) to make descriptions discoverable.
