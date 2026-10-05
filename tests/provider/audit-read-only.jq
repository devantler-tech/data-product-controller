# The verified policy emits only this controller's source and Secret requests.
# Require observed reads, and reject all mutation or broader access attempts,
# including denied requests and requests with no completed response yet.
(length > 0) and
any(.[]; .stage == "ResponseComplete" and .verb == "get") and
all(.[]; .verb == "get" and
  .user.username == "system:serviceaccount:products:dpc" and
  .objectRef.namespace == "products" and
  ((.objectRef.subresource // "") == "") and
  (.objectRef as $object | any($expected[];
    .group == ($object.apiGroup // "") and
    .resource == $object.resource and .name == $object.name)))
