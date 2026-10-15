# API-only admission checkpoint

The `admission`-tagged acceptance runs the delivered DataProduct schema on an
owned, disposable Kubernetes API server, then installs a test-only candidate
policy. The candidate retains the typed `spec.ui` property and its existing
validation and adds a rejecting CEL rule at that field. Unchanged legacy UI
metadata can ratchet through unrelated edits; new or changed UI is rejected.
The delivered CRD, served/storage version, controller and feature defaults are
unchanged by this checkpoint.

Required CI exercises checksum-pinned Kubernetes **1.34.0** and **1.37.0** envtest
assets, covering the chart's minimum version and the lifecycle suite's current
version. It verifies actual persisted objects after create, replacement, JSON
and merge patch, server-side apply, metadata and status updates. Rejected
requests must have the expected validation cause and preserve stored state;
timeouts, transport failures and apply conflicts do not count as rejection.

Removal checks preserve product UIDs, non-UI specifications, labels, annotations
and status. Apply cases inspect actual field ownership: sole-owner omission
removes UI; a non-owner's omission retains it; shared ownership retains it until
the final owner omits the field. Reintroduction is then rejected. Explicit null
is checked through a fresh GET and must be absent. A whole-specification-rule
negative control demonstrates why that broader placement blocks unrelated
edits. Restoration of the original server schema must preserve the CRD identity
and accept legacy creation and UI edits again.

The same tagged invocation checks the existing three-product publication example
through both offline API-only report formats. Normal chart and browser checks
retain the historical compatibility and independent publisher-review contracts.
Offline create validation does not predict update ratcheting or grant data access.

For a local run, download the official controller-tools envtest archive for the
chosen version and host architecture, verify its published checksum, and extract
it. Point the test at its absolute `controller-tools/envtest` directory:

```sh
ADMISSION_TEST_ASSETS=/absolute/path/controller-tools/envtest \
  ADMISSION_TEST_VERSION=v1.37.0 \
  go test -tags=admission -count=1 -timeout=600s -v ./tests/admission
```

Supported checkpoint versions are `v1.34.0` and `v1.37.0`. Missing assets or a
different running version fail the invocation. The fixture explicitly disables
existing-cluster mode, overrides ambient binary selection, and uses only the
configuration returned by its own API server. It never loads a user's kubeconfig.
Startup retries and shutdown use explicit process timeouts. The operation
deadline reserves time before the Go test alarm for schema recovery and shutdown;
normal test failures restore the owned original schema and stop the server.
Envtest also performs untimed setup, and its child processes have their own
process groups. Hosted-runner disposal is the backstop for a test alarm, an
unresponsive bootstrap binary or uncatchable process termination; the outer
command timeout alone does not prove child-process cleanup.

This is admission-migration evidence, not production activation, a versioned
contract change, compatibility-runtime retirement or independent adoption.
Those decisions and signed Platform rollout remain in
[#392](https://github.com/devantler-tech/data-product-controller/issues/392) and
[platform#4890](https://github.com/devantler-tech/platform/issues/4890).
