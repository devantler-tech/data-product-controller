const grid = document.querySelector("#data-product-grid");
const status = document.querySelector("#registry-status");
const count = document.querySelector("#product-count");
const frame = document.querySelector("#product-surface");
const empty = document.querySelector("#surface-empty");
const interactionTitle = document.querySelector("#interaction-title");
const interactionDescription = document.querySelector("#interaction-description");
const contractStatus = document.querySelector("#ui-contract-status");
let disposeSurface = () => {};
let selection = 0;

/** Map bounded presentation signals to accessible host-owned wording. */
function surfaceState(state) {
  contractStatus.hidden = false;
  contractStatus.dataset.state = state;
  contractStatus.textContent = {
    loading: "Opening product interface…",
    ready: "Product interface connected.",
    error: "The product interface reported an error. Try its query again or select the product to retry.",
    timeout: "The product interface did not respond. Select the product to retry.",
  }[state];
}

/** Render declared references and current observations as inert text, including untrusted owner metadata. */
function showLineage(product) {
  const section = document.querySelector("#composition-detail");
  const list = document.querySelector("#product-lineage");
  list.replaceChildren();
  section.hidden = !product.inputs?.length;
  document.querySelector("#composition-status").textContent = product.composition
    ? `${product.composition.reason}: ${product.composition.message}`
    : "Composition has not been observed. These are the declared input references.";
  for (const input of product.inputs || []) {
    const edge = product.lineage?.find((candidate) => candidate.name === input.name);
    const ref = edge?.productRef || input.productRef;
    const item = document.createElement("li");
    const title = document.createElement("strong");
    title.textContent = `${input.name} ← ${ref.namespace || product.namespace}/${ref.name} · ${ref.output}`;
    const detail = document.createElement("span");
    detail.textContent = edge
      ? `${edge.version || "Version unobserved"} · ${edge.owner?.name || "Owner unobserved"} · ${edge.reason}`
      : "Not observed";
    item.append(title, detail);
    if (input.contract) {
      const requirement = document.createElement("span");
      requirement.textContent = `Requires ${input.contract.protocol} from ${input.contract.minimumVersion} within the same major; major zero requires an exact match.`;
      item.append(requirement);
    }
    list.append(item);
  }
}

/** Select a descriptor and open its independent surface only while the product is ready. */
async function selectProduct(product, button) {
  const selected = ++selection;
  disposeSurface();
  disposeSurface = () => {};
  frame.removeAttribute("src");
  frame.hidden = true;
  contractStatus.hidden = true;
  document.querySelectorAll(".product-card").forEach((card) => {
    card.setAttribute("aria-pressed", String(card === button));
  });

  interactionTitle.textContent = product.displayName;
  showLineage(product);
  interactionDescription.textContent = product.ready
    ? product.description
    : `${product.readiness.reason}: ${product.readiness.message}`;

  if (!product.ready || !product.ui?.url) {
    frame.hidden = true;
    frame.removeAttribute("src");
    empty.hidden = false;
    empty.querySelector("p").textContent = product.ready
      ? "This product publishes data interfaces but no interaction surface."
      : "This product is not ready. Follow its readiness message before opening it.";
    return;
  }

  empty.hidden = true;
  if (product.ui.contract) {
    surfaceState("loading");
    try {
      const response = await fetch("/api/v1/ui-config", { cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(5000) });
      if (!response.ok) throw new Error("The host could not read its UI configuration. Select the product to retry.");
      const configuration = await response.json();
      if (selected !== selection) return;
      if (configuration.uiContractEnabled !== true) throw new Error("Portable UI contracts are disabled on this host.");
      disposeSurface = DataProductUI.mount({ frame, manifest: product.ui, grants: ["status", "resize"], onState: surfaceState });
    } catch (error) {
      if (selected !== selection) return;
      contractStatus.textContent = error.message;
      contractStatus.dataset.state = "unavailable";
    }
    return;
  }
  frame.title = product.ui.title;
  frame.src = product.ui.url;
  frame.hidden = false;
}

function productCard(product) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "product-card";
  button.setAttribute("aria-pressed", "false");
  button.dataset.ready = String(product.ready);

  const copy = document.createElement("span");
  const name = document.createElement("span");
  name.className = "product-name";
  name.textContent = product.displayName;
  const description = document.createElement("span");
  description.className = "product-description";
  description.textContent = product.description;
  copy.append(name, description);

  const version = document.createElement("span");
  version.className = "product-version";
  version.textContent = product.version;

  button.append(copy, version);
  button.addEventListener("click", () => selectProduct(product, button));
  return button;
}

async function loadProducts() {
  try {
    const response = await fetch("/api/v1/products", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      throw new Error(`registry returned ${response.status}`);
    }

    const { products } = await response.json();
    grid.replaceChildren(...products.map(productCard));
    count.textContent = `${products.length} ${products.length === 1 ? "product" : "products"}`;
    status.textContent = products.length
      ? "Choose a product to inspect its independent surface."
      : "No data products are published yet.";
  } catch (error) {
    count.textContent = "Unavailable";
    status.textContent = "The registry could not load products. Try again after the controller is ready.";
    console.error(error);
  }
}

loadProducts();
