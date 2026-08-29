# ADR 0001: Portable data-product control plane

- Status: Accepted
- Date: 2026-08-29

## Context

A data product owns a useful data capability, not only a dataset. It may provision a new source or connect to an existing one, process and govern the source, publish machine-readable query interfaces, and become an input to another data product. Producers must be able to deploy products independently, while consumers need a consistent way to discover and use them.

The completed thesis demonstrated schema-driven products with inputs, outputs, REST and GraphQL APIs, ownership, telemetry, catalog integration, and a dashboard. A controller-based product needs to preserve those capabilities without coupling every product to one runtime or central user interface.

Open Data Mesh's Data Product Descriptor Specification provides a technology-neutral vocabulary for product components. W3C DCAT 3 describes datasets, distributions, catalogs, and data services. OpenAPI and AsyncAPI describe request/response and event-driven interfaces. The Eclipse Dataspace Protocol builds on DCAT and ODRL for interoperable catalog, agreement, and transfer flows.

Kubernetes custom resources are appropriate for desired state and lifecycle metadata. They are not an application-data store, credential store, or query plane.

## Decision

The controller owns `data.devantler.tech/v1alpha1` resources. The first API contains a namespaced `DataProduct` resource with:

- stable identity, display metadata, semantic version, ownership, and documentation;
- input ports that can reference a named output port on another `DataProduct`;
- output ports that link to standard machine-readable contracts and runtime endpoints;
- one optional, independently deployed UI entrypoint;
- Kubernetes conditions and observed-generation status.

The v1alpha1 shape is a deliberately small DPDS-aligned profile, not a claim of full DPDS conformance. Protocol-specific schemas remain in OpenAPI, AsyncAPI, GraphQL, DCAT, or future adapter documents rather than being copied into the CRD.

Composition is reference-based. A product stays unready when a referenced product or output port is unavailable. The controller reports that dependency explicitly and watches referenced products so recovery is automatic. Product data is never copied through the Kubernetes API.

Credentials live in Kubernetes Secrets and are consumed only by connector or provisioner workloads. A `DataProduct` status and registry descriptor must never contain Secret values.

The controller exposes a read-only JSON registry derived from ready and unready resources. The registry is a convenience client of the public contract, not the owner of a product's interaction model.

Each product may publish an absolute HTTPS UI URL. The reference registry loads it in a sandboxed iframe without `allow-same-origin`, passes no credentials, and does not import product JavaScript. Product UIs are therefore independently deployed and can be embedded by any compatible host. A versioned capability and messaging contract will be added before hosts exchange state or credentials.

The registry UI is a release feature behind the repository's OpenFeature-based `registry-ui` flag. It is off by default in the application and tested in both states. A deployment may enable it only after the API and trust boundary are verified.

The controller ships as an OCI image and a Helm chart. The chart installs the CRD, least-privilege RBAC, one controller Deployment, one ClusterIP Service, and optional HTTPRoute resources. Platform configuration pins immutable released artifacts and owns public routing, policy, and production rollout.

## Consequences

- Data products can evolve independently from the reference registry and from one another.
- The Kubernetes API remains a control plane; data, query execution, and credentials stay in purpose-built services.
- Standard interface documents can evolve without expanding the CRD for every protocol.
- Initial provisioning and connector adapters are separate roadmap slices; the foundation reports and exposes contracts but does not pretend to provision every source.
- Sandboxed iframes provide a strong first isolation boundary at the cost of a more explicit future messaging protocol.
- v1alpha1 may change incompatibly while the first real provisioned, integrated, and composed products validate the model.

## References

- [Open Data Mesh Data Product Descriptor Specification](https://dpds.opendatamesh.org/)
- [W3C Data Catalog Vocabulary 3](https://www.w3.org/TR/vocab-dcat-3/)
- [OpenAPI Specification](https://spec.openapis.org/oas/)
- [AsyncAPI Specification](https://www.asyncapi.com/docs/reference/specification/v3.0.0)
- [Eclipse Dataspace Protocol](https://projects.eclipse.org/projects/technology.dataspace-protocol-base)
- [Kubernetes custom resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)
