/* Portable data-product-ui/v1 host. No registry or framework dependency. */
(() => {
  "use strict";
  const version = "data-product-ui/v1";
  const supported = ["status", "resize"];

  /** Require a closed object shape before acting on untrusted metadata or messages. */
  function shape(value, keys) {
    return (
      value !== null &&
      typeof value === "object" &&
      !Array.isArray(value) &&
      Object.keys(value).length === keys.length &&
      keys.every((key) => Object.hasOwn(value, key))
    );
  }

  /** HTTPS public metadata URLs never contain credentials, whitespace, or fragments. */
  function httpsURL(value) {
    if (
      typeof value !== "string" ||
      value.length > 2048 ||
      /\s|\\/.test(value)
    ) {
      throw new Error(
        "Use an absolute HTTPS URL without whitespace or credentials.",
      );
    }
    const url = new URL(value);
    if (
      url.protocol !== "https:" ||
      url.username ||
      url.password ||
      url.hash ||
      value.includes("#")
    ) {
      throw new Error(
        "Use an absolute HTTPS URL without credentials or a fragment.",
      );
    }
    return url;
  }

  /** Validate a complete public manifest and its exact host-origin authorization. */
  function validate(manifest, hostOrigin) {
    if (
      new TextEncoder().encode(JSON.stringify(manifest)).length > 16384 ||
      !shape(manifest, ["url", "title", "contract"])
    ) {
      throw new Error(
        "Use a UI manifest with only url, title and contract, at most 16 KiB.",
      );
    }
    httpsURL(manifest.url);
    if (
      typeof manifest.title !== "string" ||
      !manifest.title.trim() ||
      manifest.title.length > 200
    ) {
      throw new Error("Give the product UI a title of 1–200 characters.");
    }
    const contract = manifest.contract;
    if (
      !shape(contract, ["apiVersion", "hostOrigins", "capabilities"]) ||
      contract.apiVersion !== version
    ) {
      throw new Error("This host supports data-product-ui/v1 only.");
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
    if (
      httpsURL(hostOrigin).origin !== hostOrigin ||
      !contract.hostOrigins.includes(hostOrigin)
    ) {
      throw new Error("The publisher has not allowed this host origin.");
    }
    if (
      !Array.isArray(contract.capabilities) ||
      contract.capabilities.length > 2 ||
      new Set(contract.capabilities).size !== contract.capabilities.length ||
      contract.capabilities.some(
        (capability) => !supported.includes(capability),
      )
    ) {
      throw new Error(
        "Only distinct status and resize capabilities are supported.",
      );
    }
    return structuredClone(manifest);
  }

  /** Mount an untrusted surface with per-load session binding and explicit presentation grants. */
  function mount({ frame, manifest, grants = [], onState = () => {} }) {
    const checked = validate(manifest, location.origin);
    const allowed = supported.filter(
      (capability) =>
        grants.includes(capability) &&
        checked.contract.capabilities.includes(capability),
    );
    let session = "";
    let disposed = false;
    let connected = false;
    let timer;
    let received = 0;

    /** Remove listeners, pending deadlines and navigation when selection is withdrawn. */
    function dispose() {
      disposed = true;
      session = "";
      clearTimeout(timer);
      window.removeEventListener("message", receive);
      frame.removeEventListener("load", initialize);
      frame.removeAttribute("src");
      frame.style.height = "";
      frame.hidden = true;
    }

    /** A missing handshake is unavailable, never inferred ready from an iframe load event. */
    function deadline() {
      clearTimeout(timer);
      timer = setTimeout(() => {
        dispose();
        onState("timeout");
      }, 10000);
    }

    /** Every navigation revokes previous correlation tokens; no private context crosses the boundary. */
    function initialize() {
      if (disposed) return;
      session = crypto.randomUUID();
      connected = false;
      received = 0;
      onState("loading");
      deadline();
      // The sandbox has an opaque origin, so exact-origin targeting is unavailable.
      // This envelope contains public protocol metadata only, never credentials or user data.
      frame.contentWindow.postMessage(
        { apiVersion: version, type: "init", session, capabilities: allowed },
        "*",
      );
    }

    /** Authenticate the frame/session and validate message shape before applying bounded UI hints. */
    function receive(event) {
      if (
        disposed ||
        !session ||
        event.source !== frame.contentWindow ||
        event.origin !== "null"
      )
        return;
      const data = event.data;
      if (!data || data.apiVersion !== version || data.session !== session)
        return;
      received += 1;
      if (received > 256) {
        dispose();
        onState("error");
        return;
      }
      if (
        !connected &&
        data.type === "ready" &&
        shape(data, ["apiVersion", "type", "session"])
      ) {
        connected = true;
        clearTimeout(timer);
        onState("ready");
      } else if (
        connected &&
        data.type === "status" &&
        allowed.includes("status") &&
        shape(data, ["apiVersion", "type", "session", "state"]) &&
        ["ready", "error"].includes(data.state)
      ) {
        onState(data.state);
      } else if (
        connected &&
        data.type === "resize" &&
        allowed.includes("resize") &&
        shape(data, ["apiVersion", "type", "session", "height"]) &&
        Number.isInteger(data.height) &&
        data.height >= 240 &&
        data.height <= 1200
      ) {
        frame.style.height = `${data.height}px`;
      }
    }

    frame.setAttribute("sandbox", "allow-forms allow-scripts");
    frame.setAttribute("referrerpolicy", "no-referrer");
    frame.title = checked.title;
    frame.style.height = "";
    frame.addEventListener("load", initialize);
    window.addEventListener("message", receive);
    onState("loading");
    deadline();
    frame.src = checked.url;
    frame.hidden = false;
    return dispose;
  }

  window.DataProductUI = Object.freeze({ validate, mount });
})();
