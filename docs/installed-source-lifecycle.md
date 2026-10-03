# Accept an installed source lifecycle

The source acceptance coordinator installs the selected controller artifacts in its
own disposable Kubernetes cluster. Three modules check the export, connector and
independent contract probe against the exact selected registry product. Their evidence
describes the controlled fixture; it does not establish Platform adoption or retire
the release gates in #46, #49 or #101.

## Coordinator hooks

Source `tests/source/source-lifecycle.sh`, `connector-matrix.sh` and `contract-matrix.sh`
as function definitions after establishing the owned fixture and common helpers. The
HTTP source must be enabled, its product declared, and its exact-name observer Role
installed before the first module. The allowed consumer and outsider Pods remain
independently deployed in the `products` namespace.

Run these functions against the installed candidate:

```text
source_lifecycle_run
contract_matrix_run
connector_matrix_run
```

The contract matrix leaves its selected check enabled and healthy, so the connector
matrix can verify that `ContractsReady` stays true while connector and aggregate
readiness fail. Each matrix restores healthy state. The source restores the initial
synthetic credential pair after demonstrating rotation without Pod replacement.
The public query listener must return 404 for `/metrics`, `/readyz` and `/healthz`,
while the unauthorized outsider must fail at transport when reaching source
management metrics. An HTTP error response cannot satisfy that transport denial.

Both source and contract modules capture independently owned resource identities.
Prepare a baseline Helm revision with the same operational source/probe settings and
existing Secret before upgrading the candidate; roll back to that revision. A baseline
without the probe cannot establish probe continuity: Helm may delete it. After the
actual rollback, call:

```text
source_lifecycle_rollback_check
connector_matrix_rollback_check
contract_matrix_rollback_check
```

Restore the candidate when the coordinator requires it, check readiness, then delete
the selected product once. Confirm its exact registry descriptor disappears and call
`source_lifecycle_retention_check` and `contract_matrix_retention_check`. These reject
replaced, deleting or product-owned resources and confirm the export still works.
The coordinator retains responsibility for artifact verification, Helm revisions,
CRD installation, product removal and cluster cleanup.

The modules consume the coordinator's `kube`, `probe`, `wait_for`, `install_chart`,
`source_secret`, `source_pod`, `disabled_export`, `readiness` and `contract_readiness`
functions. Registry helpers must select `products/existing-export` semantically;
another product's readiness cannot satisfy an assertion. All mutations stay inside
the generated kubeconfig, namespace and source container belonging to this run.

## Freshness and limits

Dispatch the fixture's `metrics` command to `metrics(ctx, args)`. An allowed consumer
uses it without credentials:

```text
/fixture metrics --url http://dpc-http-source-metrics:8081/metrics \
  --kind http-source --ready 1 --since <phase-start-unix-second> --timeout 10s
```

Use `--kind contract-probe` for the independent probe's management Service. The
command requires the matching readiness gauge and completed-observation timestamp,
with the timestamp strictly newer than the phase start. Missing, duplicate, labeled,
nonfinite, future or stale observations fail. Response reads are limited to 256 KiB
and each metric line to 8 KiB. Requests reject redirects, ambient proxies, embedded
credentials and query parameters; HTTPS retains certificate verification. Neither
metrics collection nor controller observation fetches product records.

Each module has an eight-minute budget. The three modules and rollback callbacks
share an eighteen-minute budget so the existing composition, catalog and provider
assertions retain time within the hosted job's fifty-minute ceiling. Per-assertion
deadlines are clamped to the remaining budget. The coordinator must invoke rollback
callbacks before its remaining independent suites.

Once the connector reaches actual zero capacity or the required partial/full
Deployment state, its current-generation conditions and exact selected registry
product must converge within 45 seconds. This acceptance allowance includes the
30-second polling interval, a bounded five-second observation and ten seconds for
transport and scheduling. Exact-name observer RBAC revocation/restoration has the
same allowance while the controller API is available. Deployment establishment and
recovery retain separate 180-second limits (120 seconds for zero capacity).

Changing the controller's feature flag replaces its Pods. Those cases allow 180
seconds for the controller rollout and a separate 180 seconds for readiness,
including leader election and registry startup. Source health probes, Secret
projection and installed rollback also have their own convergence limits. Thirty
seconds is not a universal end-to-end guarantee. Local snapshot tests validate the
accepted states; only the hosted fixture determines whether real transitions meet
these timing allowances.

The connector matrix checks actual zero capacity and a new observed Deployment
generation with an old ready replica still serving. Full capacity, exact-name RBAC
and both observation flag states determine readiness independently of that successful
old-replica query. The contract matrix checks outage/recovery with a healthy export,
literal URL binding, controller/probe flag states, monitor isolation and retained
probe ownership. The source fixture rejects any Authorization or Cookie header,
including an empty header, on its contract publication. Successful hosted contract
observations therefore require credential-free requests. The disabled probe is
tested directly by Pod address so an old ready replica cannot conceal its disabled
behavior.
