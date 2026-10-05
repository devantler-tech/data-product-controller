/* Public descriptor validation. No registry, network or runtime dependency. */
(() => {
  "use strict";
  const encoder = new TextEncoder();
  const protocols = ["OpenAPI", "AsyncAPI", "GraphQL", "DCAT", "ArrowFlight"];
  const states = [
    "ready",
    "not-ready",
    "stale",
    "unobserved",
    "disabled",
    "not-applicable",
  ];

  /** Closed public shapes keep private or unsupported fields out of the handoff. */
  function shape(value, required, optional = []) {
    if (
      !value ||
      typeof value !== "object" ||
      Array.isArray(value) ||
      !required.every((key) => Object.hasOwn(value, key)) ||
      Object.keys(value).some(
        (key) => !required.includes(key) && !optional.includes(key),
      )
    )
      throw new Error(
        "Use a complete public v1 descriptor with supported fields only.",
      );
  }

  function text(value, required = true) {
    if (
      typeof value !== "string" ||
      encoder.encode(value).length > 16384 ||
      (required && value.length === 0)
    )
      throw new Error(
        "Descriptor text must be bounded public strings (up to 16 KiB each).",
      );
  }

  function integer(value) {
    if (!Number.isSafeInteger(value) || value < 0)
      throw new Error(
        "Descriptor generations must be nonnegative safe integers.",
      );
  }

  /** Health states follow the producer's observations independently of aggregate readiness. */
  function validObservation(state, generation, observedGeneration) {
    if (["ready", "not-ready", "disabled"].includes(state)) return observedGeneration === generation;
    if (state === "stale") return observedGeneration !== generation;
    if (state === "not-applicable") return observedGeneration === 0;
    if (state === "unobserved") return observedGeneration === 0 || observedGeneration === generation;
    return false;
  }

  /** Check decimal digits before binary floating-point rounding loses fractional precision. */
  function integerToken(token) {
    const parts = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(token);
    let digits = (parts[2] + (parts[3] || "")).replace(/^0+/, "");
    if (!digits) return; // Zero stays exact, including negative zero and exponent notation.
    const exponent = Number(parts[4] || "0");
    const scale = exponent - (parts[3] || "").length;
    if (parts[1] || !Number.isSafeInteger(exponent) ||
        (scale < 0 && (-scale > digits.length || !/^0+$/.test(digits.slice(scale)))))
      throw new Error("JSON numbers must be exact nonnegative safe integers.");
    if (scale < 0) digits = digits.slice(0, scale);
    if (digits.length + Math.max(scale, 0) > 16)
      throw new Error("JSON numbers must be exact nonnegative safe integers.");
    if (BigInt(digits + "0".repeat(Math.max(scale, 0))) > 9007199254740991n)
      throw new Error("JSON numbers must be exact nonnegative safe integers.");
  }

  function name(value) {
    text(value, true);
    if (value.length > 63 || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(value))
      throw new Error("Use a valid public product or port name.");
  }

  function fullName(value) {
    text(value);
    if (
      value.length > 253 ||
      !value
        .split(".")
        .every(
          (label) =>
            label.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(label),
        )
    )
      throw new Error("Use a valid public product DNS name.");
  }

  /** URL parsing validates metadata only; no declared URL is fetched here. */
  function url(value) {
    text(value, true);
    DataProductUI.validateURL(value);
  }

  function owner(value) {
    shape(value, ["name"], ["url"]);
    text(value.name, true);
    if (Object.hasOwn(value, "url")) url(value.url);
  }

  function output(value) {
    shape(value, ["name", "protocol", "url", "contractUrl"], ["mediaType"]);
    name(value.name);
    if (!protocols.includes(value.protocol))
      throw new Error("Use a supported output protocol.");
    url(value.url);
    url(value.contractUrl);
    if (Object.hasOwn(value, "mediaType")) text(value.mediaType);
  }

  function reference(value) {
    shape(value, ["name", "output"], ["namespace"]);
    fullName(value.name);
    name(value.output);
    if (Object.hasOwn(value, "namespace")) name(value.namespace);
  }

  function readiness(value) {
    shape(value, ["reason", "message"]);
    if (!states.includes(value.reason))
      throw new Error("Use a supported public readiness state.");
    text(value.message);
  }

  function array(value, check) {
    if (!Array.isArray(value) || value.length > 1024)
      throw new Error("Descriptor arrays may contain up to 1024 entries.");
    value.forEach(check);
  }

  function input(value) {
    shape(value, ["name", "productRef"], ["contract"]);
    name(value.name);
    reference(value.productRef);
    if (Object.hasOwn(value, "contract")) {
      shape(value.contract, ["minimumVersion", "protocol"]);
      text(value.contract.minimumVersion, true);
      if (
        !/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(
          value.contract.minimumVersion,
        ) ||
        value.contract.minimumVersion.length > 64 ||
        !protocols.includes(value.contract.protocol)
      )
        throw new Error("Use a supported input version and protocol.");
    }
  }

  function lineage(value) {
    shape(
      value,
      ["name", "productRef", "ready", "reason"],
      ["productID", "observedGeneration", "version", "owner", "output"],
    );
    name(value.name);
    reference(value.productRef);
    if (typeof value.ready !== "boolean")
      throw new Error("Descriptor readiness must be boolean.");
    if (!["InputReady", "InputNotReady"].includes(value.reason))
      throw new Error("Use a supported public lineage reason.");
    for (const key of ["productID", "version"])
      if (Object.hasOwn(value, key)) text(value[key], true);
    if (Object.hasOwn(value, "observedGeneration"))
      integer(value.observedGeneration);
    if (Object.hasOwn(value, "owner")) owner(value.owner);
    if (Object.hasOwn(value, "output")) output(value.output);
  }

  /** Admit the closed public snapshot for inspection; this grants no UI or data access. */
  function validate(value) {
    if (encoder.encode(JSON.stringify(value)).length > 65536)
      throw new Error("Keep the complete descriptor within 64 KiB.");
    shape(
      value,
      [
        "apiVersion",
        "kind",
        "namespace",
        "name",
        "id",
        "displayName",
        "description",
        "version",
        "owner",
        "outputs",
        "ready",
        "readiness",
        "generation",
        "observedGeneration",
        "health",
      ],
      ["documentationUrl", "inputs", "ui", "composition", "lineage"],
    );
    if (
      value.apiVersion !== "data-product-descriptor/v1" ||
      value.kind !== "DataProduct"
    )
      throw new Error(
        "This host imports data-product-descriptor/v1 DataProduct documents only.",
      );
    name(value.namespace);
    fullName(value.name);
    for (const key of ["id", "displayName", "version"]) text(value[key], true);
    if (!/^urn:[A-Za-z0-9][A-Za-z0-9:._-]+$/.test(value.id)) url(value.id);
    text(value.description);
    owner(value.owner);
    array(value.outputs, output);
    if (!value.outputs.length)
      throw new Error("Declare at least one public output.");
    if (Object.hasOwn(value, "documentationUrl")) url(value.documentationUrl);
    if (Object.hasOwn(value, "inputs")) array(value.inputs, input);
    if (Object.hasOwn(value, "lineage")) array(value.lineage, lineage);
    readiness(value.readiness);
    if (Object.hasOwn(value, "composition")) readiness(value.composition);
    if (Object.hasOwn(value, "ui")) {
      shape(value.ui, ["url", "title"], ["contract"]);
      url(value.ui.url);
      text(value.ui.title);
      if (
        value.ui.url.length > 2048 ||
        value.ui.title.length > 200 ||
        !value.ui.title.trim()
      )
        throw new Error("Use a bounded public UI URL and title.");
      if (Object.hasOwn(value.ui, "contract"))
        DataProductUI.validateMetadata(value.ui);
    }
    integer(value.generation);
    integer(value.observedGeneration);
    shape(value.health, ["source", "connector", "contracts", "composition"]);
    for (const dimension of Object.values(value.health)) {
      shape(dimension, [
        "state",
        "message",
        "generation",
        "observedGeneration",
      ]);
      if (!states.includes(dimension.state))
        throw new Error("Use a supported public health state.");
      text(dimension.message);
      integer(dimension.generation);
      integer(dimension.observedGeneration);
      if (
        dimension.generation !== value.generation ||
        !validObservation(dimension.state, dimension.generation, dimension.observedGeneration)
      )
        throw new Error(
          "Health readiness must describe this descriptor generation.",
        );
    }
    if (typeof value.ready !== "boolean")
      throw new Error("Descriptor readiness must be boolean.");
    if (
      value.ready !== (value.readiness.reason === "ready") ||
      (value.ready && value.observedGeneration !== value.generation)
    )
      throw new Error(
        "Descriptor readiness must describe its current generation.",
      );
    return structuredClone(value);
  }

  /** Preserve raw declarations under each caller's bounded public metadata budget. */
  function parseJSON(source, maximum = 65536) {
    if (!Number.isSafeInteger(maximum) || maximum < 1 || maximum > 2097152 ||
        typeof source !== "string" || encoder.encode(source).length > maximum)
      throw new Error("Keep the JSON document within its selected metadata budget.");
    const value = JSON.parse(source);
    const stack = [];
    // Syntax is already validated. Tokens retain object keys that JSON.parse would overwrite.
    for (const [token] of source.matchAll(
      /"(?:\\[\s\S]|[^"\\])*"|[{}\[\]:,]|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g,
    )) {
      if (token === "{" || token === "[") {
        if (stack.length >= 128)
          throw new Error("Use a shallower JSON document.");
        stack.push(token === "{" ? { keys: new Set(), key: true } : null);
      } else if (token === "}" || token === "]") {
        stack.pop();
      } else {
        if (!token.startsWith('"') && /^-?\d/.test(token)) integerToken(token);
        const object = stack.at(-1);
        if (!object) continue;
        if (token === ",") object.key = true;
        else if (token === ":") object.key = false;
        else if (object.key && token.startsWith('"')) {
          const key = JSON.parse(token);
          if (object.keys.has(key))
            throw new Error("Use each JSON field only once.");
          object.keys.add(key);
          object.key = false;
        }
      }
    }
    return value;
  }

  /** Offline mounting additionally requires current readiness and exact publisher host approval. */
  function parse(source, hostOrigin) {
    if (typeof source !== "string" || encoder.encode(source).length > 65536)
      throw new Error("Keep the complete descriptor within 64 KiB.");
    let value;
    try {
      value = parseJSON(source);
    } catch {
      throw new Error(
        "Use a valid descriptor JSON document with unique fields.",
      );
    }
    value = validate(value);
    if (
      !value.ready ||
      value.readiness.reason !== "ready" ||
      value.observedGeneration !== value.generation
    )
      throw new Error(
        "This descriptor snapshot does not report current readiness.",
      );
    if (!Object.hasOwn(value, "ui"))
      throw new Error("This descriptor does not publish a portable UI.");
    url(value.ui?.url);
    return DataProductUI.validate(value.ui, hostOrigin);
  }

  window.DataProductDescriptor = Object.freeze({ validate, parse, parseJSON, validObservation });
})();
