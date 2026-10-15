/* Inert public metadata validation. No frames, sessions, messages or network access. */
(() => {
  "use strict";
  const capabilities = Object.freeze({
    "data-product-ui/v1": Object.freeze(["status", "resize"]),
    "data-product-ui/v2": Object.freeze(["status", "resize", "appearance"]),
  });

  /** Return immutable presentation capabilities without creating a session or granting permission. */
  function supportedCapabilities(version) {
    return Object.hasOwn(capabilities, version) ? capabilities[version] : Object.freeze([]);
  }

  /** Name only fixed required fields; never reflect unknown keys or publisher values. */
  function requireFields(value, keys, label) {
    if (value === null || typeof value !== "object" || Array.isArray(value) ||
        Object.keys(value).some(key => !keys.includes(key))) {
      throw new Error(`Use a ${label} with only ${keys.join(", ")}.`);
    }
    const missing = keys.filter(key => !Object.hasOwn(value, key));
    if (missing.length) {
      throw new Error(`Add the missing ${label} fields: ${missing.join(", ")}.`);
    }
  }

  /** Validate the literal public HTTPS profile without normalizing malformed declarations or fetching them. */
  function httpsURL(value) {
    const invalid = () => { throw new Error("Use a literal public HTTPS URL without credentials, whitespace or fragments."); };
    if (typeof value !== "string" || new TextEncoder().encode(value).length > 16384 ||
        !value.startsWith("https://") || /[^\x21-\x7e]|\\|#/.test(value)) invalid();
    if (/%(?![0-9a-fA-F]{2})/.test(value.split("?", 1)[0])) invalid();
    const authority = /^https:\/\/(\[[0-9A-Fa-f:.]+\]|[a-z0-9.-]+)(?::([0-9]+))?(?:[/?]|$)/.exec(value);
    if (!authority || (authority[2] !== undefined &&
        (Number(authority[2]) < 1 || Number(authority[2]) > 65535))) invalid();
    if (!authority[1].startsWith("[") &&
        (authority[1].length > 253 || !authority[1].split(".").every(label =>
          label.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(label)))) invalid();
    let parsed;
    try { parsed = new URL(value); } catch { invalid(); }
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.hash) invalid();
    if (!authority[1].startsWith("[")) {
      const labels = authority[1].split("."), last = labels.at(-1);
      if (/^[0-9]+$/.test(last) || last.startsWith("0x")) {
        if (labels.length !== 4 || labels.some(label =>
          !/^(0|[1-9][0-9]*)$/.test(label) || Number(label) > 255) || parsed.hostname !== authority[1]) invalid();
      }
    }
    return parsed;
  }

  /** Validate public presentation metadata without granting any host permission. */
  function validateMetadata(manifest) {
    if (new TextEncoder().encode(JSON.stringify(manifest)).length > 16384)
      throw new Error("Keep the UI manifest within 16 KiB.");
    requireFields(manifest, ["url", "title", "contract"], "UI manifest");
    if (typeof manifest.url !== "string" || manifest.url.length > 2048)
      throw new Error("Keep the UI URL within 2048 characters.");
    httpsURL(manifest.url);
    if (
      typeof manifest.title !== "string" ||
      !manifest.title.trim() ||
      manifest.title.length > 200
    ) {
      throw new Error("Give the product UI a title of 1–200 characters.");
    }
    const contract = manifest.contract;
    requireFields(contract, ["apiVersion", "hostOrigins", "capabilities"], "UI contract");
    if (
      typeof contract.apiVersion !== "string" ||
      !Object.hasOwn(capabilities, contract.apiVersion)
    ) {
      throw new Error("This host supports data-product-ui/v1 and v2 only.");
    }
    if (
      !Array.isArray(contract.hostOrigins) ||
      !contract.hostOrigins.length ||
      contract.hostOrigins.length > 16 ||
      new Set(contract.hostOrigins).size !== contract.hostOrigins.length
    ) {
      throw new Error("Declare 1–16 distinct host origins.");
    }
    for (const origin of contract.hostOrigins) {
      const parsed = httpsURL(origin);
      if (
        origin.length > 253 ||
        parsed.origin !== origin ||
        parsed.port === "0" ||
        !parsed.hostname
          .split(".")
          .every((label) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
      ) {
        throw new Error(
          "Host origins must be exact HTTPS origins without a path or wildcard.",
        );
      }
    }
    const supported = capabilities[contract.apiVersion];
    if (
      !Array.isArray(contract.capabilities) ||
      contract.capabilities.length > supported.length ||
      new Set(contract.capabilities).size !== contract.capabilities.length ||
      contract.capabilities.some(
        (capability) => !supported.includes(capability),
      )
    ) {
      throw new Error(
        "Use distinct presentation capabilities supported by this protocol version.",
      );
    }
    return structuredClone(manifest);
  }

  /** Bind validated metadata to the publisher's exact host-origin authorization. */
  function validate(manifest, hostOrigin) {
    const checked = validateMetadata(manifest);
    if (
      httpsURL(hostOrigin).origin !== hostOrigin ||
      !checked.contract.hostOrigins.includes(hostOrigin)
    ) {
      throw new Error("The publisher has not allowed this host origin.");
    }
    return checked;
  }

  window.DataProductMetadata = Object.freeze({
    validateMetadata, validate, validateURL: httpsURL, supportedCapabilities,
  });
})();
