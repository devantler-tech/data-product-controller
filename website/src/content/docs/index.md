---
title: KHoist documentation
description: Publish, discover and compose reusable data products through standard APIs.
---

KHoist describes data products as Kubernetes resources. It records who owns a product,
the APIs it exposes and the products it depends on. A controller reports readiness;
a read-only registry makes those descriptions available to people and applications.

Your services keep their data, credentials and query APIs. Your applications own their
user interfaces and access decisions.

## Get a first result

[Try a data product](/start/quickstart/) on your machine. You will query two sample
observations and check a product declaration. No cluster or credentials are needed.

For an existing Kubernetes environment, [install the controller](/start/installation/),
then [publish an API](/guides/publish-an-api/) that your team already operates.

## Work with products

| I want to…                                 | Start with                                            |
|--------------------------------------------|-------------------------------------------------------|
| Describe a service my team owns            | [Publish an API](/guides/publish-an-api/)             |
| Catch declaration errors before deployment | [Check declarations](/guides/check-declarations/)     |
| Use a product in an application            | [Consume a product](/guides/consume-a-product/)       |
| Declare dependencies between products      | [Compose products](/guides/compose-products/)         |
| Diagnose a product that is not ready       | [Troubleshooting](/troubleshooting/)                  |
| Understand what KHoist manages             | [Architecture and ownership](/concepts/architecture/) |

## Choose a supported path

KHoist is evolving. Its Kubernetes API is `v1alpha1`; optional capabilities have
explicit release gates. Read [capabilities and maturity](/concepts/capabilities/)
before choosing a provider or enabling an integration.

The product is named KHoist. Its current repository, chart and image retain the
`data-product-controller` name. Use the identifiers in these guides when installing.
The documentation follows the repository's main branch; check your selected release
before applying an example to an older installation.
