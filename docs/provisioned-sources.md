# Use a provisioner-owned source

A product can wait for a namespaced Crossplane resource and its published connection Secret before
becoming ready. The provisioner creates and operates the source; this controller observes its
readiness. Product workloads read credentials directly from the Secret.

Enable observation with Helm's `--set provisionedSources.enabled=true`, or set
`PROVISIONED_SOURCES_ENABLED=true` when running the manager directly. The OpenFeature flag
`provisioned-sources` defaults off. A product declaring a source stays unready while it is off;
products without a source retain ordinary dependency readiness.

## Reference contract

The `crossplane/v1` adapter accepts a namespaced provisioner resource that:

- reports exactly one `Ready=True` and one `Synced=True` condition;
- names its connection Secret through `spec.writeConnectionSecretToRef.name`;
- uses the product namespace for that Secret, either explicitly or by omission;
- owns the connection Secret through an owner reference matching its UID, API version, kind, and name.

An explicit `observedGeneration` on the status or either condition must match the resource's
generation. Crossplane resources that omit this optional field are evaluated from their reported
conditions; the controller cannot establish generation freshness when the provisioner omits it.
Resources publishing credentials through another mechanism need a separate versioned adapter.

The example below assumes an operator-defined namespaced `Database` API managed by Crossplane.
Its Composition, engine parameters, and connection-key schema belong to that provisioner's
installation. It illustrates the reference contract; it does not install a database operator.

```yaml
apiVersion: database.example.org/v1alpha1
kind: Database
metadata:
  name: warehouse
  namespace: products
spec:
  writeConnectionSecretToRef:
    name: warehouse-connection
---
apiVersion: data.devantler.tech/v1alpha1
kind: DataProduct
metadata:
  name: warehouse-product
  namespace: products
spec:
  id: urn:example:warehouse
  name: Warehouse
  description: Curated warehouse queries.
  version: v1.0.0
  owner:
    name: warehouse-team
  source:
    adapter: crossplane/v1
    resourceRef:
      apiVersion: database.example.org/v1alpha1
      kind: Database
      name: warehouse
    connectionSecretRef:
      name: warehouse-connection
  outputs:
    - name: query
      protocol: OpenAPI
      url: https://warehouse.example.com/query
      contractUrl: https://warehouse.example.com/openapi.json
```

## Authorize the observer

The chart does not grant access to provisioners or Secrets. Add a Role and RoleBinding in each
product namespace, scoped to the selected names. Replace the controller service-account name and
namespace below if the Helm release uses different values.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: warehouse-observer
  namespace: products
rules:
  - apiGroups: [database.example.org]
    resources: [databases]
    resourceNames: [warehouse]
    verbs: [get]
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [warehouse-connection]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: warehouse-observer
  namespace: products
subjects:
  - kind: ServiceAccount
    name: data-product-controller
    namespace: data-product-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: warehouse-observer
```

Kubernetes authorizes a Secret `get` even when the client requests metadata only; RBAC does not
restrict the response to metadata. Keep these grants narrow. The controller uses uncached metadata
requests and publishes neither Secret references nor credential values through the public registry.

## Readiness and lifecycle

Observation runs every 30 seconds. Missing APIs, denied reads, missing or deleting sources,
unready conditions, absent Secrets, publication-reference mismatches, and stale Secret ownership
produce unready conditions with a next step. Detailed provider errors stay on the provider resource.
Ordinary product inputs must also be ready.

Credential rotation at the same Secret name needs no product change. Secret contents are never
inspected or copied into product status, so rotating them does not cause status updates. Recreating
a source gives it a new UID; an old connection Secret cannot satisfy readiness until the provisioner
publishes one owned by the new resource.

Deleting a DataProduct does not delete, mutate, or adopt the source or Secret. Configure retention,
backups, credential rotation, and deprovisioning in the provisioner. Removing `spec.source` removes
the readiness dependency and leaves source lifecycle unchanged.

The rollout gate remains until [#41](https://github.com/devantler-tech/data-product-controller/issues/41)
records real provisioner lifecycle evidence. Local API and browser tests do not establish production
database availability.
