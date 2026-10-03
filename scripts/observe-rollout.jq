# Projections deliberately exclude spec/status payloads, conditions' prose,
# annotations other than revision, environment, volumes and credentials.
$ARGS.named.namespace as $namespace |
$ARGS.named.image as $image |
$ARGS.named.required_conditions as $required_conditions |
$ARGS.named.accepted_digests as $accepted_digests |
$ARGS.named.product_names as $product_names |
$ARGS.named.deployment_identities as $deployment_identities |
$ARGS.named.products as $products |
$ARGS.named.deployments as $deployments |
$ARGS.named.replicasets as $replicasets |
$ARGS.named.pods as $pods |
$ARGS.named.final_products as $final_products |
$ARGS.named.final_deployments as $final_deployments |
$ARGS.named.final_replicasets as $final_replicasets |
$ARGS.named.final_pods as $final_pods |
def metadata:
  {name, namespace, uid, generation, deletionTimestamp,
   revision: .annotations["deployment.kubernetes.io/revision"],
   ownerReferences: [.ownerReferences[]? | {apiVersion,kind,name,uid,controller}]};
def containers: [.[]? | {name,image}];
def product:
  {apiVersion,kind,metadata:(.metadata | metadata),
   status:{conditions:[.status.conditions[]? | {type,status,observedGeneration}]}};
def deployment:
  {apiVersion,kind,metadata:(.metadata | metadata),
   spec:{replicas:.spec.replicas,containers:(.spec.template.spec.containers | containers)},
   status:(.status | {observedGeneration,replicas,updatedReplicas,readyReplicas,availableReplicas})};
def replicaset:
  {apiVersion,kind,metadata:(.metadata | metadata),
   spec:{replicas:.spec.replicas,containers:(.spec.template.spec.containers | containers)},
   status:(.status | {observedGeneration,replicas,readyReplicas,availableReplicas})};
def pod:
  {apiVersion,kind,metadata:(.metadata | metadata),
   spec:{containers:(.spec.containers | containers)},
   status:{phase:.status.phase,conditions:[.status.conditions[]? | {type,status}],
           containerStatuses:[.status.containerStatuses[]? | {name,ready,imageID,
             state:{running:(.state.running | type == "object"),waiting:(.state.waiting != null),terminated:(.state.terminated != null)}}]}};
def integer: type == "number" and . == floor;
def positive: integer and . > 0;
def identity($name; $kind; $version):
  .kind == $kind and .apiVersion == $version and .metadata.name == $name and
  (.metadata.name | type == "string" and length > 0) and
  .metadata.namespace == $namespace and (.metadata.uid | type == "string" and length > 0) and
  (.metadata.generation | positive) and .metadata.deletionTimestamp == null;
def conditions_ready:
  . as $p | all($required_conditions[];
    . as $type | [$p.status.conditions[] | select(.type == $type)] as $matching |
    ($matching | length) == 1 and $matching[0].status == "True" and
    $matching[0].observedGeneration == $p.metadata.generation);
def product_ready($name): identity($name; "DataProduct"; "data.devantler.tech/v1alpha1") and conditions_ready;
def deployment_ready($name; $container):
  . as $d | identity($name; "Deployment"; "apps/v1") and
  (.metadata.revision | type == "string" and test("^[1-9][0-9]*$")) and
  (.spec.replicas | positive) and .status.observedGeneration == .metadata.generation and
  all([.status.replicas,.status.updatedReplicas,.status.readyReplicas,.status.availableReplicas][]; . == $d.spec.replicas) and
  ([.spec.containers[] | select(.name == $container)] | length) == 1 and
  any(.spec.containers[]; .name == $container and .image == $image);
def owned($kind; $name; $uid):
  [.metadata.ownerReferences[] | select(.controller == true)] as $owners |
  ($owners | length) == 1 and $owners[0].apiVersion == "apps/v1" and
  $owners[0].kind == $kind and $owners[0].name == $name and $owners[0].uid == $uid;
def runtime_allowed:
  . as $id | ($image | split("@")[0]) as $repository |
  any($accepted_digests[]; . as $digest |
    $id == $digest or $id == ("containerd://" + $digest) or
    $id == ("docker://" + $digest) or $id == ("cri-o://" + $digest) or
    $id == ($repository + "@" + $digest) or
    $id == ("docker-pullable://" + $repository + "@" + $digest));
def pod_ready($rs; $container):
  . as $p | .kind == "Pod" and .apiVersion == "v1" and .metadata.namespace == $namespace and
  (.metadata.uid | type == "string" and length > 0) and .metadata.deletionTimestamp == null and
  owned("ReplicaSet"; $rs.metadata.name; $rs.metadata.uid) and .status.phase == "Running" and
  ([.status.conditions[] | select(.type == "Ready")] | length) == 1 and
  any(.status.conditions[]; .type == "Ready" and .status == "True") and
  ([.spec.containers[] | select(.name == $container)] | length) == 1 and
  any(.spec.containers[]; .name == $container and .image == $image) and
  ([.status.containerStatuses[] | select(.name == $container)] | length) == 1 and
  any(.status.containerStatuses[]; .name == $container and .ready == true and
    .state.running == true and .state.waiting == false and .state.terminated == false and
    (.imageID | runtime_allowed));
# Validate current ownership, complete readiness and runtime identity in this inventory pair.
def workload_ready($d; $container; $replica_inventory; $pod_inventory):
  [$replica_inventory.items[] | select(owned("Deployment"; $d.metadata.name; $d.metadata.uid))] as $owned |
  [$owned[] | select(.metadata.revision == $d.metadata.revision)] as $current |
  ($current | length) == 1 and
  ($current[0] | identity(.metadata.name; "ReplicaSet"; "apps/v1")) and
  $current[0].status.observedGeneration == $current[0].metadata.generation and
  $current[0].spec.replicas == $d.spec.replicas and
  all([$current[0].status.replicas,$current[0].status.readyReplicas,$current[0].status.availableReplicas][]; . == $d.spec.replicas) and
  ([$current[0].spec.containers[] | select(.name == $container)] | length) == 1 and
  any($current[0].spec.containers[]; .name == $container and .image == $image) and
  ([$pod_inventory.items[] | . as $p | select(any($owned[]; . as $r | $p | owned("ReplicaSet"; $r.metadata.name; $r.metadata.uid)))] as $owned_pods |
    ($owned_pods | length) == $d.spec.replicas and all($owned_pods[]; pod_ready($current[0]; $container)));
def inventory_valid($items):
  ($items | type == "array" and length <= 4096) and
  all($items[]; .metadata.namespace == $namespace and (.metadata.uid | type == "string" and length > 0)) and
  ([$items[].metadata.uid] | length) == ([$items[].metadata.uid] | unique | length);
def snapshot_ready:
  ($products[0] | length) == ($product_names | length) and
  ($deployments[0] | length) == ($deployment_identities | length) and
  inventory_valid($replicasets[0].items) and inventory_valid($pods[0].items) and
  all(range(0; $product_names | length); . as $i | $products[0][$i] | product_ready($product_names[$i])) and
  all(range(0; $deployment_identities | length); . as $i | $deployment_identities[$i] as $identity |
    $deployments[0][$i] as $d | ($d | deployment_ready($identity.name; $identity.container)) and
    workload_ready($d; $identity.container; $replicasets[0]; $pods[0]));
def current_replicasets($d; $inventory):
  [$inventory.items[] | select(owned("Deployment"; $d.metadata.name; $d.metadata.uid) and
    .metadata.revision == $d.metadata.revision)];
# Require both reads to describe the same current ReplicaSet and spec generation.
def current_replicaset_stable($before; $after):
  current_replicasets($before; $replicasets[0]) as $initial |
  current_replicasets($after; $final_replicasets[0]) as $final |
  ($initial | length) == 1 and ($final | length) == 1 and
  $final[0].metadata.uid == $initial[0].metadata.uid and
  $final[0].metadata.generation == $initial[0].metadata.generation;
def final_stable:
  ($final_products[0] | length) == ($product_names | length) and
  ($final_deployments[0] | length) == ($deployment_identities | length) and
  inventory_valid($final_replicasets[0].items) and inventory_valid($final_pods[0].items) and
  all(range(0; $product_names | length); . as $i | $final_products[0][$i] as $after |
    ($after | product_ready($product_names[$i])) and
    $after.metadata.uid == $products[0][$i].metadata.uid and $after.metadata.generation == $products[0][$i].metadata.generation) and
  all(range(0; $deployment_identities | length); . as $i | $deployment_identities[$i] as $identity |
    $final_deployments[0][$i] as $after | ($after | deployment_ready($identity.name; $identity.container)) and
    $after.metadata.uid == $deployments[0][$i].metadata.uid and
    $after.metadata.generation == $deployments[0][$i].metadata.generation and
    $after.metadata.revision == $deployments[0][$i].metadata.revision and
    workload_ready($after; $identity.container; $final_replicasets[0]; $final_pods[0]) and
    current_replicaset_stable($deployments[0][$i]; $after));

if $mode == "product" or $mode == "deployment" or $mode == "replicasets" or $mode == "pods" then
  if type != "array" or length != 1 then error("invalid_response")
  else .[0] |
    if $mode == "product" then product
    elif $mode == "deployment" then deployment
    elif $mode == "replicasets" and ((.kind == "ReplicaSetList" and .apiVersion == "apps/v1") or (.kind == "List" and .apiVersion == "v1")) and
      (.items | type == "array" and length <= 4096) and all(.items[]; .kind == "ReplicaSet" and .apiVersion == "apps/v1")
    then {items:[.items[] | replicaset]}
    elif $mode == "pods" and (.kind == "PodList" or .kind == "List") and .apiVersion == "v1" and
      (.items | type == "array" and length <= 4096) and all(.items[]; .kind == "Pod" and .apiVersion == "v1")
    then {items:[.items[] | pod]}
    else error("invalid_inventory") end
  end
elif ($mode == "snapshot" or $mode == "final") and snapshot_ready and ($mode != "final" or final_stable) then
  {complete:true,products:($product_names | length),deployments:($deployment_identities | length),pods:([$deployments[0][].spec.replicas] | add)}
else false end
