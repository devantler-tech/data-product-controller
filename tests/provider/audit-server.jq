.items | length == 1 and all(.[];
  ([.spec.containers[] | select(.name == "kube-apiserver") |
    select((.command | index("--audit-policy-file=/audit/policy.yaml")) != null) |
    select((.command | index("--audit-log-path=/audit/log.json")) != null) |
    select(any(.volumeMounts[]?; .name == "audit" and .mountPath == "/audit" and .readOnly != true))] | length == 1) and
  any(.spec.volumes[]?; .name == "audit" and .hostPath.path == "/audit" and .hostPath.type == "Directory"))
