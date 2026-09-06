#!/bin/sh
set -eu

# Only the ephemeral fixture uses this generated CA. Production chart defaults
# and TLS certificate/hostname verification remain intact.
yq eval 'with(select(.kind == "Deployment" and .metadata.labels."app.kubernetes.io/component" == "http-source");
  .spec.template.spec.volumes += [{"name": "test-ca", "configMap": {"name": "synthetic-source-ca"}}] |
  .spec.template.spec.containers[0].env += [{"name": "SSL_CERT_FILE", "value": "/test-ca/ca.crt"}] |
  .spec.template.spec.containers[0].volumeMounts += [{"name": "test-ca", "mountPath": "/test-ca", "readOnly": true}]
)' -
