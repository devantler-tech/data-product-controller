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
