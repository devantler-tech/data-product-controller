const manifest = document.querySelector("#manifest");
const status = document.querySelector("#kit-status");
const frame = document.querySelector("#product-surface");
let dispose = () => {};
let selection = 0;
const appearanceEnabled = document.body.dataset.appearanceEnabled === "true";
const discoveryEnabled = document.body.dataset.discoveryEnabled === "true";
const appearance = document.querySelector("#appearance");
const snapshot = document.querySelector("#descriptor-snapshot");
document.querySelector("#appearance-policy").hidden = !appearanceEnabled;
document.querySelector("#descriptor-import").hidden = !discoveryEnabled;
appearance.addEventListener("change", () =>
  dispose.setAppearance?.(appearance.value),
);

/** Display protocol evidence with host-owned wording and an accessible status region. */
function showState(state) {
  status.dataset.state = state;
  status.textContent = {
    loading: "Manifest accepted. Waiting for the product interface…",
    ready:
      "Compatible handshake received. Exercise the product and check its keyboard and small-screen behavior.",
    error: frame.hidden
      ? "The host closed the interface after a protocol failure. Validate and open it again to retry."
      : "The product reported an interface error. Check recovery through its own controls.",
    timeout:
      "No compatible handshake arrived within 10 seconds. Check the publisher’s host origins, version, availability and embedding policy, then retry.",
  }[state];
}

/** A new selection withdraws the old frame before any untrusted input is read. */
function revoke() {
  selection += 1;
  dispose();
  dispose = () => {};
  snapshot.hidden = true;
  return selection;
}

/** Both import formats share the existing opaque sandbox and explicit grant intersection. */
function openManifest(value) {
  const grants = ["status", "resize"].filter(
    (name) => document.querySelector(`#grant-${name}`).checked,
  );
  if (appearanceEnabled && document.querySelector("#grant-appearance").checked)
    grants.push("appearance");
  dispose = DataProductUI.mount({
    frame,
    manifest: value,
    grants,
    appearanceEnabled,
    appearance: appearance.value,
    onState: showState,
  });
}

document.querySelector("#manifest-form").addEventListener("submit", (event) => {
  event.preventDefault();
  revoke();
  try {
    if (new TextEncoder().encode(manifest.value).length > 16384)
      throw new Error("Keep the manifest within 16 KiB.");
    openManifest(JSON.parse(manifest.value));
  } catch (error) {
    status.dataset.state = "invalid";
    status.textContent = `Manifest rejected: ${error.message}`;
  }
});

document.querySelector("#descriptor-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const current = revoke();
  status.dataset.state = "importing";
  status.textContent = "Reading local public descriptor…";
  try {
    if (!discoveryEnabled) throw new Error("Offline descriptor import is disabled on this host.");
    const files = document.querySelector("#descriptor-file").files;
    const pasted = document.querySelector("#descriptor").value;
    if (files.length > 1 || (files.length && pasted.trim()))
      throw new Error("Choose one local descriptor file or paste one descriptor.");
    if (files.length && files[0].size > 65536)
      throw new Error("Keep the complete descriptor within 64 KiB.");
    const text = files.length ? await files[0].text() : pasted;
    // A slow local file read cannot revive a session after Close or a newer selection.
    if (current !== selection) return;
    openManifest(DataProductDescriptor.parse(text, location.origin));
    snapshot.hidden = false;
  } catch (error) {
    if (current !== selection) return;
    status.dataset.state = "invalid";
    status.textContent = `Descriptor rejected: ${error.message}`;
  }
});

document.querySelector("#close").addEventListener("click", () => {
  revoke();
  status.dataset.state = "closed";
  status.textContent = "Interface closed. Its session and grants are revoked.";
});
