# Compose products with declared contracts

A consumer selects each producer by namespace, product name and output name. The
producer's `spec.version` versions its public product contract, including its
outputs. An input may require a contract family and a minimum stable version:

```yaml
inputs:
  - name: observations
    productRef:
      name: harbour
      namespace: products
      output: query
    contract:
      protocol: OpenAPI
      minimumVersion: v1.1.0
```

The namespace defaults to the consumer's namespace. A required OpenAPI contract
at `v1.1.0` accepts `v1.1.0`, `v1.2.0` and later stable versions within major 1.
It rejects older versions, major 2, pre-releases and build metadata. Major-zero
contracts require an exact match because their compatibility is not stable.
The producer's selected output must declare the same protocol.

This checks the publisher's version declaration. It does not download schemas,
prove semantic compatibility, execute queries, or move data. Product workloads
implement the actual composition and their own authentication and access policy.
An unversioned input still requires an existing named output on a Ready product.

## Enable observation

Apply the release's generated CRD before upgrading an existing installation; Helm
does not upgrade CRDs in `crds/`. Set `composition.enabled=true` in the chart, or
`COMPOSITION_ENABLED=true` for the manager process, to enable the default-off
OpenFeature `composition` flag. The registry UI has its separate
`registryUI.enabled` flag. Invalid flag values stop startup.

A versioned input fails with `CompositionFeatureDisabled` while observation is
off. Unversioned inputs retain ordinary dependency readiness. Turning observation
off clears recorded lineage; removing all inputs clears `CompositionReady` and
lineage together. Restarting the controller re-evaluates existing products.

## Read readiness and lineage

`CompositionReady` records graph and declared contract compatibility independently
of source, connector and contract-probe conditions. Any failed capability prevents
aggregate `Ready`. The registry exposes the composition reason and message along
with a `lineage` array containing each direct input's observed producer identity,
namespace, generation, version, owner, output and readiness. The UI shows these
observations under **Inputs and lineage**, including requirements and failures.

| Reason | Action |
| --- | --- |
| `CompositionFeatureDisabled` | Enable observation before using required input contracts. |
| `DependencyCycle` | Remove the circular input reference named in the message. |
| `DependencyNotFound` | Publish the referenced product or correct its namespace/name. |
| `OutputNotFound` | Select a published output on the producer. |
| `ContractIncompatible` | Choose a compatible producer version and protocol, or deliberately update the consumer requirement. |
| `DependencyNotReady` | Resolve producer readiness and wait for its current generation. |
| `DependencyUnavailable` | Restore Kubernetes API availability or controller access. |
| `CompositionLimitExceeded` | Split a graph exceeding 256 products, 1,024 inputs, or 64 levels. |

Checks share a five-second deadline. Recorded lineage is limited to 64 KiB of
encoded JSON; excessive metadata reports `CompositionLimitExceeded` and clears
lineage instead of attempting an oversized status write. Product changes enqueue transitive consumers;
observed compositions also retry after 30 seconds. Unchanged observations do not
write status. The controller requests no additional permissions, credentials or
network access. Source and connector lifecycle stays independent.

Lineage is an eventually consistent observation, not record-level provenance or
an atomic snapshot. The registry withholds old lineage when the consumer's
generation has changed. Producer generations identify the revisions observed;
clients can follow the direct references through the descriptor collection.
When traversal stops early, remaining inputs report `DependencyUnobserved` rather
than claiming readiness. A graph failure can coexist with ready individual edges.

## Try three products

The [three-product example](examples/composition.yaml) describes harbour
observations, coastal weather and a coastal summary consuming both. Its
`example.com` endpoints are illustrative metadata; applying it does not deploy
those services or prove their network reachability.

In a disposable cluster with composition and registry UI enabled:

```bash
kubectl create namespace products
kubectl apply -f docs/examples/composition.yaml
kubectl wait -n products --for=condition=Ready dataproduct/coastal-summary --timeout=120s
kubectl get dataproduct -n products coastal-summary -o yaml
```

Read `/api/v1/products` and select **Coastal summary** in the registry. Both
producers appear with their contract versions and owners. Change harbour's
version to `v2.0.0`: the summary reports `ContractIncompatible`. Restore it to
`v1.3.0` to recover. Adding an input from weather back to coastal-summary produces
`DependencyCycle`; removing that input recovers the graph.

The browser acceptance test runs the same example through real reconciliation,
the HTTP registry and Chromium. The required Kubernetes suite checks installed
CRD persistence, both flag states, upgrades, cycles, missing ports, deletion and
recovery. Production rollout and flag retirement remain tracked in
[issue #113](https://github.com/devantler-tech/data-product-controller/issues/113).
