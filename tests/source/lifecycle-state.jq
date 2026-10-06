# Only current independently owned resources in the disposable products namespace are accepted.
$ARGS.named as $args |
if $args.mode == "ownership" then
  (.items // [.]) as $items |
  if ($args.product_uid | type) != "string" or $args.product_uid == "" or
    ($args.wanted | type) != "array" or ($args.wanted | length) == 0 or
    ($items | length) != ($args.wanted | length) or
    ([ $args.wanted[] | .kind + "/" + .name ] | unique | length) != ($args.wanted | length) or
    any($items[]; .metadata.namespace != "products" or
      (.metadata.uid | type) != "string" or .metadata.uid == "" or .metadata.deletionTimestamp != null or
      any(.metadata.ownerReferences[]?; .uid == $args.product_uid)) or
    any($args.wanted[]; . as $wanted |
      ([ $items[] | select(.kind == $wanted.kind and .metadata.name == $wanted.name) ] | length) != 1)
  then error("independent resource identity unavailable")
  else [ $items[] | {key:(.kind + "/" + .metadata.name),value:.metadata.uid} ] | from_entries end
elif $args.mode == "source-pods" then
  (if $args | has("healthy") then $args.healthy else true end) as $healthy |
  def owned_by($kind; $uid):
    [.metadata.ownerReferences[]? | select(.apiVersion == "apps/v1" and .kind == $kind and
      .uid == $uid and .controller == true)] | length == 1;
  [.items[] | select(.kind == "Deployment")] as $deployments |
  [.items[] | select(.kind == "Pod")] as $pods |
  $deployments[0] as $d |
  $d.metadata.annotations["deployment.kubernetes.io/revision"] as $revision |
  [.items[] | select(.kind == "ReplicaSet" and .metadata.deletionTimestamp == null and
    .metadata.annotations["deployment.kubernetes.io/revision"] == $revision) |
    select(owned_by("Deployment"; $d.metadata.uid))] as $sets |
  $sets[0] as $rs |
  if ($healthy | type) != "boolean" or ($args.count | type) != "number" or $args.count < 1 or $args.count != ($args.count | floor) or
    ($deployments | length) != 1 or $d.metadata.name != "dpc-http-source" or
    $d.metadata.namespace != "products" or $d.metadata.deletionTimestamp != null or
    ($d.metadata.uid | type) != "string" or $d.metadata.uid == "" or
    ($revision | type) != "string" or ($revision | test("^[1-9][0-9]*$") | not) or
    $d.metadata.generation <= 0 or $d.status.observedGeneration != $d.metadata.generation or
    $d.spec.replicas != $args.count or $d.status.replicas != $args.count or
    $d.status.updatedReplicas != $args.count or
    ($healthy and ($d.status.readyReplicas != $args.count or $d.status.availableReplicas != $args.count or
      ($d.status.unavailableReplicas // 0) != 0)) or
    ($sets | length) != 1 or $rs.metadata.namespace != "products" or
    ($rs.metadata.uid | type) != "string" or $rs.metadata.uid == "" or
    $rs.metadata.generation <= 0 or $rs.status.observedGeneration != $rs.metadata.generation or
    $rs.spec.replicas != $args.count or $rs.status.replicas != $args.count or
    ($healthy and ($rs.status.readyReplicas != $args.count or $rs.status.availableReplicas != $args.count)) or
    ($pods | length) != $args.count or
    ([$pods[].metadata.uid] | unique | length) != $args.count or
    ([$pods[].status.podIP] | unique | length) != $args.count or
    any($pods[]; .metadata.namespace != "products" or .metadata.deletionTimestamp != null or
      (.metadata.uid | type) != "string" or .metadata.uid == "" or
      (.status.podIP | type) != "string" or .status.podIP == "" or
      (owned_by("ReplicaSet"; $rs.metadata.uid) | not) or
      ([.status.conditions[]? | select(.type == "Ready")] | length) != 1 or
      ($healthy and any(.status.conditions[]?; .type == "Ready" and .status != "True")) or
      (.status.containerStatuses | length) != 1 or .status.containerStatuses[0].name != "http-source" or
      ($healthy and .status.containerStatuses[0].ready != true) or
      (.status.containerStatuses[0].restartCount | type) != "number")
  then error("expected complete current HTTP source endpoints")
  else [$pods[] | {name:.metadata.name,uid:.metadata.uid,ip:.status.podIP,
    restarts:.status.containerStatuses[0].restartCount}] | sort_by(.uid) end
elif $args.mode == "full" or $args.mode == "partial" or $args.mode == "zero" then
  (.spec.replicas // 1) as $desired |
  if .kind != "Deployment" or .metadata.namespace != "products" or .metadata.name != $args.name or
    .metadata.deletionTimestamp != null or (.metadata.generation | type) != "number" or
    .metadata.generation <= 0 or .status.observedGeneration != .metadata.generation
  then false
  elif $args.mode == "zero" then
    $desired == 0 and (.status.replicas // 0) == 0 and (.status.readyReplicas // 0) == 0 and
      (.status.availableReplicas // 0) == 0
  elif $args.mode == "partial" then
    $desired > 0 and .metadata.generation > $args.previous and .status.replicas > $desired and
      .status.readyReplicas > 0 and .status.availableReplicas > 0
  else
    $desired > 0 and .status.replicas == $desired and .status.updatedReplicas == $desired and
      .status.readyReplicas == $desired and .status.availableReplicas == $desired and
      (.status.unavailableReplicas // 0) == 0
  end
else error("unsupported lifecycle observation") end
