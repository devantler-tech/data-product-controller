if $ARGS.named.mode == "signature" then
  length == 1 and (.[0] | type == "array" and length > 0 and length <= 16 and
    all(.[]; .critical.image["docker-manifest-digest"] == $ARGS.named.digest))
elif $ARGS.named.mode == "manifest" then
  (if length == 1 then .[0] else error("documents") end) |
  if .schemaVersion != 2 then error("schema")
  elif .mediaType == "application/vnd.oci.image.index.v1+json" or
       .mediaType == "application/vnd.docker.distribution.manifest.list.v2+json" then
    [.manifests[] | select(.platform.os == "linux" and .platform.architecture == $ARGS.named.arch) | .digest] |
    if length == 1 and (.[0] | test("^sha256:[a-f0-9]{64}$")) then .[0] else error("platform") end
  elif .mediaType == "application/vnd.oci.image.manifest.v1+json" or
       .mediaType == "application/vnd.docker.distribution.manifest.v2+json" then
    $ARGS.named.digest
  else error("unsupported image manifest") end
elif $ARGS.named.mode == "receipt" then
  {complete:true,tag:$ARGS.named.tag,sourceSHA:$ARGS.named.sha,publisherSHA:$ARGS.named.publisher,
    imageDigest:$ARGS.named.image,chartDigest:$ARGS.named.chart,platform:$ARGS.named.platform,runtimeDigest:$ARGS.named.runtime}
else error("unsupported verification mode") end
