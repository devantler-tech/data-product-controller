/* Public descriptor validation. No registry, network or runtime dependency. */
(() => {
  "use strict";
  const encoder = new TextEncoder();
  const protocols = ["OpenAPI", "AsyncAPI", "GraphQL", "DCAT", "ArrowFlight"];
  const states = ["ready", "not-ready", "stale", "unobserved", "disabled", "not-applicable"];

  /** Closed public shapes keep private or unsupported fields out of the handoff. */
  function shape(value, required, optional = []) {
    if (!value || typeof value !== "object" || Array.isArray(value) ||
        !required.every((key) => Object.hasOwn(value, key)) ||
        Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key)))
      throw new Error("Use a complete public v1 descriptor with supported fields only.");
  }

  function text(value, required = true) {
    if (typeof value !== "string" || encoder.encode(value).length > 16384 ||
        (required && value.length === 0))
      throw new Error("Descriptor text must be bounded public strings (up to 16 KiB each).");
  }

  function integer(value) {
    if (!Number.isSafeInteger(value) || value < 0)
      throw new Error("Descriptor generations must be nonnegative safe integers.");
  }

  function name(value) {
    text(value, true);
    if (value.length > 63 || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(value))
      throw new Error("Use a valid public product or port name.");
  }

  function fullName(value) {
    text(value);
    if (value.length > 253 || !value.split(".").every((label) =>
      label.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(label)))
      throw new Error("Use a valid public product DNS name.");
  }

  /** URL parsing validates metadata only; no declared URL is fetched here. */
  function url(value) {
    text(value, true);
    if (!value.startsWith("https://") || /[^\x21-\x7e]|\\|#/.test(value))
      throw new Error("Use literal public HTTPS metadata URLs without credentials, whitespace or fragments.");
    if (/%(?![0-9a-fA-F]{2})/.test(value.split("?", 1)[0]))
      throw new Error("Use valid percent escapes in public HTTPS metadata paths.");
    const authority = /^https:\/\/(\[[0-9A-Fa-f:.]+\]|[a-z0-9.-]+)(?::([0-9]+))?(?:[/?]|$)/.exec(value);
    if (!authority || (authority[2] !== undefined &&
        (Number(authority[2]) < 1 || Number(authority[2]) > 65535)))
      throw new Error("Use a valid public HTTPS host and port from 1 to 65535.");
    if (!authority[1].startsWith("[")) fullName(authority[1]);
    let parsed;
    try { parsed = new URL(value); } catch { throw new Error("Use absolute public HTTPS metadata URLs."); }
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.hash || /\s|\\|#/.test(value))
      throw new Error("Use public HTTPS metadata URLs without credentials, whitespace or fragments.");
    if (!authority[1].startsWith("[")) {
      const labels = authority[1].split(".");
      const last = labels.at(-1);
      if (/^[0-9]+$/.test(last) || last.startsWith("0x")) {
        if (labels.length !== 4 || labels.some(label =>
          !/^(0|[1-9][0-9]*)$/.test(label) || Number(label) > 255) ||
            parsed.hostname !== authority[1])
          throw new Error("Use canonical numeric IP addresses in public metadata URLs.");
      }
    }
  }

  function owner(value) {
    shape(value, ["name"], ["url"]);
    text(value.name, true);
    if (Object.hasOwn(value, "url")) url(value.url);
  }

  function output(value) {
    shape(value, ["name", "protocol", "url", "contractUrl"], ["mediaType"]);
    name(value.name);
    if (!protocols.includes(value.protocol)) throw new Error("Use a supported output protocol.");
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
    if (!states.includes(value.reason)) throw new Error("Use a supported public readiness state.");
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
      if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(value.contract.minimumVersion) ||
          value.contract.minimumVersion.length > 64 || !protocols.includes(value.contract.protocol))
        throw new Error("Use a supported input version and protocol.");
    }
  }

  function lineage(value) {
    shape(value, ["name", "productRef", "ready", "reason"], ["productID", "observedGeneration", "version", "owner", "output"]);
    name(value.name);
    reference(value.productRef);
    if (typeof value.ready !== "boolean") throw new Error("Descriptor readiness must be boolean.");
    if (!["InputReady", "InputNotReady"].includes(value.reason))
      throw new Error("Use a supported public lineage reason.");
    for (const key of ["productID", "version"]) if (Object.hasOwn(value, key)) text(value[key], true);
    if (Object.hasOwn(value, "observedGeneration")) integer(value.observedGeneration);
    if (Object.hasOwn(value, "owner")) owner(value.owner);
    if (Object.hasOwn(value, "output")) output(value.output);
  }

  /** Admit the closed public snapshot for inspection; this grants no UI or data access. */
  function validate(value) {
    if (encoder.encode(JSON.stringify(value)).length > 65536)
      throw new Error("Keep the complete descriptor within 64 KiB.");
    shape(value, ["apiVersion", "kind", "namespace", "name", "id", "displayName", "description", "version", "owner", "outputs", "ready", "readiness", "generation", "observedGeneration", "health"],
      ["documentationUrl", "inputs", "ui", "composition", "lineage"]);
    if (value.apiVersion !== "data-product-descriptor/v1" || value.kind !== "DataProduct")
      throw new Error("This host imports data-product-descriptor/v1 DataProduct documents only.");
    name(value.namespace);
    fullName(value.name);
    for (const key of ["id", "displayName", "version"]) text(value[key], true);
    if (!/^urn:[A-Za-z0-9][A-Za-z0-9:._-]+$/.test(value.id)) url(value.id);
    text(value.description);
    owner(value.owner);
    array(value.outputs, output);
    if (!value.outputs.length) throw new Error("Declare at least one public output.");
    if (Object.hasOwn(value, "documentationUrl")) url(value.documentationUrl);
    if (Object.hasOwn(value, "inputs")) array(value.inputs, input);
    if (Object.hasOwn(value, "lineage")) array(value.lineage, lineage);
    readiness(value.readiness);
    if (Object.hasOwn(value, "composition")) readiness(value.composition);
    if (Object.hasOwn(value, "ui")) {
      shape(value.ui, ["url", "title"], ["contract"]);
      url(value.ui.url);
      text(value.ui.title);
      if (value.ui.url.length > 2048 || value.ui.title.length > 200 || !value.ui.title.trim())
        throw new Error("Use a bounded public UI URL and title.");
      if (Object.hasOwn(value.ui, "contract")) DataProductUI.validateMetadata(value.ui);
    }
    integer(value.generation);
    integer(value.observedGeneration);
    shape(value.health, ["source", "connector", "contracts", "composition"]);
    for (const dimension of Object.values(value.health)) {
      shape(dimension, ["state", "message", "generation", "observedGeneration"]);
      if (!states.includes(dimension.state)) throw new Error("Use a supported public health state.");
      text(dimension.message);
      integer(dimension.generation);
      integer(dimension.observedGeneration);
      if (dimension.generation !== value.generation ||
          (dimension.state === "ready" && dimension.observedGeneration !== value.generation))
        throw new Error("Health readiness must describe this descriptor generation.");
    }
    if (typeof value.ready !== "boolean") throw new Error("Descriptor readiness must be boolean.");
    if (value.ready !== (value.readiness.reason === "ready") ||
        (value.ready && value.observedGeneration !== value.generation))
      throw new Error("Descriptor readiness must describe its current generation.");
    return structuredClone(value);
  }

  /** Offline mounting additionally requires current readiness and exact publisher host approval. */
  function parse(source, hostOrigin) {
    if (typeof source !== "string" || encoder.encode(source).length > 65536)
      throw new Error("Keep the complete descriptor within 64 KiB.");
    let value;
    try { value = JSON.parse(source); } catch { throw new Error("Use a valid descriptor JSON document."); }
    value = validate(value);
    if (!value.ready || value.readiness.reason !== "ready" || value.observedGeneration !== value.generation)
      throw new Error("This descriptor snapshot does not report current readiness.");
    if (!Object.hasOwn(value, "ui")) throw new Error("This descriptor does not publish a portable UI.");
    url(value.ui?.url);
    return DataProductUI.validate(value.ui, hostOrigin);
  }

  window.DataProductDescriptor = Object.freeze({ validate, parse });
})();
