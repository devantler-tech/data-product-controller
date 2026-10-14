# 0023: API-only product integration

Date: 2026-10-14

Status: Accepted

## Context

Products publish named output endpoints and standard contracts. Their consumers
include other products and independently built applications. A product UI
entrypoint and presentation messaging protocol create another integration
surface without defining data access or helping consumers compose APIs.

The maintainer has chosen API-only data-product integration. Applications own
their interfaces, including accessibility, navigation, appearance and the way
results from several products are combined.

## Decision

Standard published APIs are the product integration contract. Named input/output
references, declared compatibility, ownership and readiness remain public
control-plane metadata. OpenAPI, GraphQL, AsyncAPI and other supported standard
contracts describe their own protocols outside the CRD.

The registry is an API catalog workspace. It displays public metadata and
explicit API/contract links. Selecting a product does not fetch its data or
contract, mount its UI, run publisher code or establish a presentation session.
Its Content Security Policy denies frames and allows connections only to its
own origin. System, Light and Dark are local workspace choices.

Consumer applications select a published output and use their own authorised
transport. Any client library is optional and independent of rendering; APIs
must remain usable without it. A library may help with supported contracts,
bounded reads, cancellation and safe errors. It must not dynamically load
publisher JavaScript or become a general server-driven UI runtime.

Product data, credentials, queries and authenticated proxies stay outside the
controller. A catalog listing, compatible declaration or readiness observation
does not grant data access. Direct browser requests require an intentional
access and cross-origin policy; privileged proxying belongs to the consuming
application or a separate data-plane service.

## Compatibility boundary

Legacy `v1alpha1` UI declarations and descriptor fields remain accepted as
bounded metadata. They grant no execution capability in the registry. The
registry's legacy presentation configuration keys are always false, regardless
of old flag settings. Metadata validation remains strict, including the shared
literal public HTTPS and closed declaration profile.

The independent embedding compatibility kit and standalone reference pages are
deprecated compatibility surfaces. Their existing isolation and protocol
controls remain enforced while they exist. The independent publisher-review
application remains useful and must be preserved when the compatibility
runtime is separated or retired.

Removing those public fields, protocols, flags and workloads requires a
versioned migration, generated schemas, signed release artifacts and coordinated
Platform GitOps adoption. That work is tracked in
[#390](https://github.com/devantler-tech/data-product-controller/issues/390),
with contract/runtime retirement in
[#392](https://github.com/devantler-tech/data-product-controller/issues/392) and
production adoption in
[platform#4890](https://github.com/devantler-tech/platform/issues/4890).
The registry change alone does not establish complete migration or production
acceptance.

## Consequences

- Consumers can combine APIs and render consistent application interfaces without
  adopting a publisher's UI or coordinating presentation grants.
- The registry preserves discovery, public health, descriptor export, input
  navigation and bounded dependency traces. It observes no product records.
- Specialised interactions belong to consumer applications and need their own
  domain-specific design; a machine-readable API does not provide that UX.
- The registry has no product UI fallback. Existing applications needing the
  deprecated compatibility kit remain a separate, explicitly bounded migration.
- Source validation, publication, installed rollout and independent consumer use
  remain distinct evidence. Normal signed immutable GitOps rollback is the
  production recovery path.
