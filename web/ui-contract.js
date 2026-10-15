/* Portable data-product-ui/v1 and v2 host. No registry or framework dependency. */
(() => {
  "use strict";
  const { validateMetadata, validate, validateURL: httpsURL, supportedCapabilities } = DataProductMetadata;

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

  /** Mount an untrusted surface with per-load session binding and explicit presentation grants. */
  function mount({
    frame,
    manifest,
    grants = [],
    appearanceEnabled = false,
    appearance = "light",
    onState = () => {},
  }) {
    const checked = validate(manifest, location.origin);
    const version = checked.contract.apiVersion;
    if (version === "data-product-ui/v2" && appearanceEnabled !== true) {
      throw new Error("Appearance contracts are disabled on this host.");
    }
    const supported = supportedCapabilities(version);
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
    let currentAppearance;
    let lastAppearance = "";
    let appearanceUpdates = 0;

    /** Send only an explicitly granted light/dark hint to the active opaque frame. */
    function setAppearance(value) {
      if (!["light", "dark"].includes(value)) {
        throw new Error("Appearance must be light or dark.");
      }
      currentAppearance = value;
      if (
        disposed ||
        !connected ||
        !allowed.includes("appearance") ||
        value === lastAppearance
      )
        return;
      if (++appearanceUpdates > 256) {
        dispose();
        onState("error");
        return;
      }
      lastAppearance = value;
      frame.contentWindow.postMessage(
        { apiVersion: version, type: "appearance", session, appearance: value },
        "*",
      );
    }
    setAppearance(appearance);

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
      lastAppearance = "";
      appearanceUpdates = 0;
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
        setAppearance(currentAppearance);
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
    dispose.setAppearance = setAppearance;
    return dispose;
  }

  window.DataProductUI = Object.freeze({ validateMetadata, validate, mount, validateURL: httpsURL });
})();
