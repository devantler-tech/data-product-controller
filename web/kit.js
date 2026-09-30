const manifest = document.querySelector("#manifest");
const status = document.querySelector("#kit-status");
const frame = document.querySelector("#product-surface");
let dispose = () => {};

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

document.querySelector("#manifest-form").addEventListener("submit", (event) => {
  event.preventDefault();
  dispose();
  try {
    if (new TextEncoder().encode(manifest.value).length > 16384)
      throw new Error("Keep the manifest within 16 KiB.");
    const grants = ["status", "resize"].filter(
      (name) => document.querySelector(`#grant-${name}`).checked,
    );
    dispose = DataProductUI.mount({
      frame,
      manifest: JSON.parse(manifest.value),
      grants,
      onState: showState,
    });
  } catch (error) {
    status.dataset.state = "invalid";
    status.textContent = `Manifest rejected: ${error.message}`;
  }
});

document.querySelector("#close").addEventListener("click", () => {
  dispose();
  status.dataset.state = "closed";
  status.textContent = "Interface closed. Its session and grants are revoked.";
});
