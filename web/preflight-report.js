/* Offline publisher report profile. Validation grants no session or runtime authority. */
(() => {
  "use strict";
  const encoder = new TextEncoder();
  const features = new Set("provisioned-sources engine-providers connector-readiness composition contract-readiness ui-contract ui-appearance dcat-catalog".split(" "));
  const codes = new Set("NoProducts SourceLimit DocumentLimit ReadFailed InputLimit ProductLimit DepthLimit InvalidDocument UnsupportedResource UnknownField AdmissionInvalid NamespaceRequired InvalidNamespace IdentityConflict InvalidPublicMetadata InvalidUI ContractOutputNotFound ProducerUnresolved OutputNotFound ContractIncompatible CrossNamespaceInput CompositionCycle CompositionLimit ValidationLimit ValidationUnavailable PreviewLimit InvalidField RequiredField DuplicateListItem CrossFieldInvalid".split(" "));
  const pathPattern = /^(\/(apiVersion|kind|metadata|spec)(\/[A-Za-z][A-Za-z0-9]*|\/[0-9]+)*)?$/;
  const reject = () => { throw new Error("Use a complete, supported publisher report within the public v2 bounds."); };
  const require = (condition) => { if (!condition) reject(); };
  const bytes = (text) => encoder.encode(text).length;

  function shape(value, required, optional = "", nullable = "") {
    const keys = required.split(" ").filter(Boolean);
    const allowed = new Set([...keys, ...optional.split(" ").filter(Boolean)]);
    require(value && typeof value === "object" && !Array.isArray(value));
    require(keys.every(key => Object.hasOwn(value, key)));
    require(Object.keys(value).every(key => allowed.has(key) &&
      (value[key] !== null || nullable.split(" ").includes(key))));
    return value;
  }
  function array(value, maximum) {
    require(Array.isArray(value) && value.length <= maximum);
    return value;
  }
  function integer(value, maximum) {
    require(Number.isSafeInteger(value) && value >= 0 && value <= maximum);
    return value;
  }
  function text(value, maximum, empty = false) {
    require(typeof value === "string" && (empty || value.length > 0) && bytes(value) <= maximum);
    return value;
  }
  function featureSet(value) {
    let previous = "";
    for (const feature of array(value, 8)) {
      text(feature, 64);
      require(features.has(feature) && feature > previous);
      previous = feature;
    }
    return value;
  }

  /** Scan before decoding so duplicate decoded keys and non-integral numeric tokens cannot disappear. */
  function decode(source) {
    text(source, 2 << 20);
    let at = 0;
    const space = () => { while (/[ \t\r\n]/.test(source[at] || "\0")) at++; };
    function string() {
      require(source[at] === '"');
      const start = at++;
      while (at < source.length) {
        const char = source[at++];
        if (char === "\\") { at++; continue; }
        if (char === '"') {
          const result = JSON.parse(source.slice(start, at));
          require(result.isWellFormed());
          return result;
        }
      }
      reject();
    }
    function value(depth) {
      require(depth <= 64);
      space();
      if (source[at] === '"') { string(); return; }
      const opener = source[at];
      if (opener === "{" || opener === "[") {
        at++;
        const close = opener === "{" ? "}" : "]";
        const keys = new Set();
        let count = 0;
        space();
        if (source[at] === close) { at++; return; }
        for (;;) {
          if (opener === "{") {
            const key = string();
            require(!keys.has(key));
            keys.add(key);
            require(keys.size <= 32);
            space();
            require(source[at++] === ":");
          } else require(++count <= 1024);
          value(depth + 1);
          space();
          if (source[at] === close) { at++; return; }
          require(source[at++] === ",");
          space();
        }
      }
      const token = /^(true|false|null|-?(?:0|[1-9][0-9]*))/.exec(source.slice(at));
      require(token !== null);
      at += token[0].length;
    }
    value(0);
    space();
    require(at === source.length);
    return JSON.parse(source);
  }

  /** Preview validation remains inert: never call the descriptor's ready-snapshot mount parser. */
  function preview(value) {
    const descriptor = DataProductDescriptor.validate(value);
    // Match the independent Go reader's bounded JSON encoding, including HTML escapes.
    const encoded = JSON.stringify(descriptor).replace(/[<>&\u2028\u2029]/g,
      char => "\\u" + char.charCodeAt(0).toString(16).padStart(4, "0"));
    require(bytes(encoded) <= 65536);
    require(!descriptor.ready && descriptor.generation === 0 && descriptor.observedGeneration === 0);
    require(descriptor.readiness.reason === "unobserved");
    if (descriptor.composition) require(descriptor.composition.reason === "unobserved");
    require(!descriptor.lineage || descriptor.lineage.length === 0);
    for (const dimension of Object.values(descriptor.health)) {
      require(["unobserved", "not-applicable"].includes(dimension.state) &&
        dimension.generation === 0 && dimension.observedGeneration === 0);
    }
    for (const ports of [descriptor.outputs, descriptor.inputs || []]) {
      require(new Set(ports.map(port => port.name)).size === ports.length);
    }
    return descriptor;
  }

  function validate(r) {
    shape(r, "apiVersion valid complete products sources requiredFeatures productFeatures diagnostics diagnosticCounts descriptors plan", "", "plan");
    require(r.apiVersion === "data-product-preflight/v2" &&
      typeof r.valid === "boolean" && typeof r.complete === "boolean" && (!r.complete || r.valid));
    integer(r.products, 256);
    let totalProducts = 0, documents = 0;
    array(r.sources, 32).forEach((source, index) => {
      shape(source, "source documents products");
      require(integer(source.source, 32) === index + 1);
      documents += integer(source.documents, 4097);
      totalProducts += integer(source.products, 256);
      require(source.products <= source.documents);
    });
    require(totalProducts === r.products && documents <= (r.valid ? 4096 : 4097));
    const position = (source, document, global = false) => {
      integer(source, 32); integer(document, 4097);
      if (source === 0) require(global && document === 0);
      else require(source <= r.sources.length && document <= r.sources[source - 1].documents &&
        (global || document > 0));
      return source + "/" + document;
    };
    featureSet(r.requiredFeatures);
    const featurePositions = new Map(), featureCounts = new Map(), union = new Set();
    for (const entry of array(r.productFeatures, 256)) {
      shape(entry, "source document requiredFeatures");
      const key = position(entry.source, entry.document);
      require(!featurePositions.has(key));
      featurePositions.set(key, entry);
      featureCounts.set(entry.source, (featureCounts.get(entry.source) || 0) + 1);
      featureSet(entry.requiredFeatures).forEach(feature => union.add(feature));
    }
    r.sources.forEach(source => {
      const count = featureCounts.get(source.source) || 0;
      require(count <= source.products && (!r.complete || count === source.products));
    });
    require(union.size === r.requiredFeatures.length && r.requiredFeatures.every(feature => union.has(feature)));
    const counts = shape(r.diagnosticCounts, "total errors warnings omitted");
    Object.values(counts).forEach(n => integer(n, 1 << 20));
    let errors = 0, warnings = 0;
    for (const finding of array(r.diagnostics, 128)) {
      shape(finding, "source document line column severity code path message witness witnessTruncated");
      position(finding.source, finding.document, true);
      integer(finding.line, (2 << 20) + 1); integer(finding.column, (2 << 20) + 1);
      require((finding.line === 0) === (finding.column === 0));
      require(codes.has(text(finding.code, 64)));
      require(pathPattern.test(text(finding.path, 4096, true)));
      text(finding.message, 256);
      require(typeof finding.witnessTruncated === "boolean");
      require(finding.severity === "error" || finding.severity === "warning");
      if (finding.severity === "error") errors++; else warnings++;
      for (const step of array(finding.witness, 64)) {
        shape(step, "source document path");
        position(step.source, step.document);
        require(pathPattern.test(text(step.path, 4096, true)));
      }
    }
    require(counts.total === counts.errors + counts.warnings &&
      counts.omitted === counts.total - r.diagnostics.length &&
      errors <= counts.errors && warnings <= counts.warnings &&
      (counts.omitted !== 0 || (errors === counts.errors && warnings === counts.warnings)));
    require(r.valid === (counts.errors === 0) && r.complete === (counts.total === 0));
    array(r.descriptors, 256);
    if (!r.complete) {
      require(r.descriptors.length === 0 && r.plan === null);
      return r;
    }
    require(r.products > 0 && r.descriptors.length === r.products && r.productFeatures.length === r.products);
    const products = new Map(), ids = new Set();
    for (const value of r.descriptors) {
      const descriptor = preview(value), key = descriptor.namespace + "/" + descriptor.name;
      require(!products.has(key) && !ids.has(descriptor.id));
      products.set(key, descriptor);
      ids.add(descriptor.id);
    }
    shape(r.plan, "order edges");
    require(array(r.plan.order, 256).length === r.products);
    const rank = new Map(), origins = new Map(), usedPositions = new Set();
    r.plan.order.forEach((entry, index) => {
      shape(entry, "key source document");
      text(entry.key, 317);
      const at = position(entry.source, entry.document);
      require(products.has(entry.key) && !rank.has(entry.key) && featurePositions.has(at) && !usedPositions.has(at));
      usedPositions.add(at);
      rank.set(entry.key, index);
      origins.set(entry.key, at);
    });
    const seen = new Set();
    let expected = 0;
    products.forEach(product => { expected += (product.inputs || []).length; });
    require(array(r.plan.edges, 1024).length === expected);
    for (const edge of r.plan.edges) {
      shape(edge, "consumer producer input inputIndex output source document");
      text(edge.consumer, 317); text(edge.producer, 317);
      const consumer = products.get(edge.consumer), producer = products.get(edge.producer);
      require(consumer && producer && rank.get(edge.producer) < rank.get(edge.consumer));
      integer(edge.inputIndex, 1023);
      const input = (consumer.inputs || [])[edge.inputIndex];
      require(input);
      const ref = input.productRef, namespace = ref.namespace || consumer.namespace;
      require(namespace === consumer.namespace && producer.namespace === consumer.namespace &&
        namespace + "/" + ref.name === edge.producer && edge.input === input.name && edge.output === ref.output &&
        producer.outputs.some(output => output.name === edge.output));
      require(position(edge.source, edge.document) === origins.get(edge.consumer));
      const key = edge.consumer + "/" + edge.inputIndex;
      require(!seen.has(key));
      seen.add(key);
    }
    return r;
  }

  /** Return a fresh validated value; all errors use fixed host wording. */
  function parse(source) {
    try { return structuredClone(validate(decode(source))); }
    catch { reject(); }
  }
  window.DataProductPreflightReport = Object.freeze({parse});
})();
