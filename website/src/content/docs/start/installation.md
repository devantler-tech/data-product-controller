---
title: Install the controller
description: Install the signed Helm distribution in an existing Kubernetes cluster.
---

Use a cluster you are authorised to change. You need Helm, kubectl, access to the
container registry and permission to install the DataProduct CRD and controller RBAC.
The current chart requires Kubernetes **1.35 or newer**. Check the selected release's
`kubeVersion` before installing; individual source operators have their own supported versions.

## Select and verify a release

Choose a published version from
[release tags](https://github.com/devantler-tech/khoist/tags).
From v6.0.2 the chart and controller image are published under the `khoist` package name.
Releases up to v6.0.1 keep the earlier `data-product-controller` name.

The commands below contain placeholders. Replace `<version>` with the chart version
and `<verified-image-digest>` with the complete controller digest verified for that
release, including its `sha256:` prefix.

```bash
helm show chart oci://ghcr.io/devantler-tech/charts/khoist \
  --version '<version>'
```

Use the [released artifact verification procedure](https://github.com/devantler-tech/khoist/blob/main/docs/release-acceptance.md)
before selecting an image digest. Controller images and charts have different signing
identities; verifying one does not verify the other.

## Install without public routing

```bash
helm upgrade --install data-product-controller \
  oci://ghcr.io/devantler-tech/charts/khoist \
  --version '<version>' \
  --namespace data-product-system --create-namespace \
  --set-string image.digest='<verified-image-digest>' \
  --wait --timeout 5m
```

The registry is available by default. Optional observation and discovery capabilities
remain off unless you explicitly enable them. See [configuration](/reference/configuration/).

## Open the registry locally

```bash
kubectl --namespace data-product-system port-forward \
  service/data-product-controller 8081:80
```

Open `http://127.0.0.1:8081`. A successful port-forward and page response establish
that you can reach the registry; they do not establish the health of any product API.
Stop the port-forward with `Ctrl+C`.

Public routing is optional. If you enable `route.enabled`, configure its Gateway
listener, hostname, authentication and access policy through your platform.
KHoist's registry is cluster-wide and is not a tenant-filtered catalogue.

## Plan upgrades and removal

Read [upgrade an installation](/guides/upgrade/) before changing versions:
Helm does not upgrade an existing CRD from a chart's `crds/` directory.
Uninstalling the release removes its chart-owned workloads. Helm retains the CRD;
deleting that CRD yourself also deletes its stored custom resources.
