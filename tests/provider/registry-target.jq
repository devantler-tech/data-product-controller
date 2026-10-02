# Select only the current Deployment revision's live Ready pod and named registry port.
$deployment[0] as $d |
if ($d.metadata.uid | type) != "string" or ($d.metadata.annotations["deployment.kubernetes.io/revision"] | type) != "string" then error("current Deployment revision unavailable") else . end |
[$replicasets[0].items[] | select(.metadata.deletionTimestamp == null) |
  select(.metadata.annotations["deployment.kubernetes.io/revision"] == $d.metadata.annotations["deployment.kubernetes.io/revision"]) |
  select(any(.metadata.ownerReferences[]?; .controller == true and .kind == "Deployment" and .uid == $d.metadata.uid))] as $rs |
(if ($rs | length) != 1 then error("current registry ReplicaSet unavailable") else $rs[0] end) as $r |
[$d.spec.template.spec.containers[] | select(.name == "controller") | .ports[] | select(.name == "registry") | .containerPort] as $ports |
[$pods[0].items[] | select(.metadata.deletionTimestamp == null) |
  select(any(.metadata.ownerReferences[]?; .controller == true and .kind == "ReplicaSet" and .uid == $r.metadata.uid)) |
  select(any(.status.conditions[]?; .type == "Ready" and .status == "True")) |
  select(any(.spec.containers[]?; .name == "controller" and .image == ([$d.spec.template.spec.containers[] | select(.name == "controller") | .image][0])))] as $current |
if ($ports | length) == 1 and ($ports[0] | type) == "number" and $ports[0] > 0 and $ports[0] <= 65535 and
  ($current | length) == 1 and ($current[0].metadata.name | test("^[a-z0-9][a-z0-9-]*$"))
then {pod:$current[0].metadata.name, port:$ports[0]}
else error("current ready registry target unavailable") end
