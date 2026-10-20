---
title: Upgrade an installation
description: Update CRDs and immutable workloads while preserving product resources.
---

Select and verify the new release before changing the installation. Record the current
chart version, verified image digest and operator-owned values so you have a recovery path.
Review release notes for Kubernetes API and retained compatibility changes.
The current chart requires Kubernetes **1.35 or newer**. Confirm the selected release's
`kubeVersion` against your cluster before applying its CRD or upgrading workloads.

## Apply the delivered CRD

Helm installs files in `crds/` on first installation. It does not upgrade or delete
an existing CRD when you upgrade the chart.

After reviewing the selected release's CRD, replace `<version>` and apply it before
upgrading workloads:

```bash
helm show crds oci://ghcr.io/devantler-tech/charts/khoist \
  --version '<version>' | kubectl apply -f -
```

The API is `v1alpha1`. Do not remove fields, downgrade a schema or delete the CRD
as a troubleshooting shortcut. A workload rollback does not roll back the CRD.

## Upgrade workloads

Use the selected new version and verified digest with your reviewed operator values:

```bash
helm upgrade '<existing-release>' oci://ghcr.io/devantler-tech/charts/khoist \
  --namespace '<existing-namespace>' \
  --version '<version>' \
  --values '<reviewed-operator-values.yaml>' \
  --set-string image.digest='<verified-image-digest>'
```

An installation of a release up to v6.0.1 was made from the earlier chart address,
`oci://ghcr.io/devantler-tech/charts/data-product-controller`. It upgrades in place
from the address above. Workload names come from the release name, so they do not
change.

Keep the existing release name and namespace. Preserve routing, feature switches,
resource settings and access policy in the values file. Replace any saved image
override deliberately; review the effective values before applying the upgrade.

Check rollout, stored product identities and conditions. An unchanged pod count or
a successful Helm command alone does not prove that public APIs work.

## Verify and recover

Read the registry through the intended access path and query a product from its actual
consumer. Check that descriptions, ownership and non-UI fields remain intact.
Verify any optional feature your deployment enables.

For a failed workload upgrade, restore the recorded chart, digest and values through
your platform's deployment workflow. Handle schema recovery separately after reviewing
the old release's supported fields. The
[installed lifecycle reference](https://github.com/devantler-tech/khoist/blob/main/docs/installed-source-lifecycle.md)
explains the repository's controlled upgrade, rollback and retained-source evidence.
