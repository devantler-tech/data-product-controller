const grid = document.querySelector("#data-product-grid");
const status = document.querySelector("#registry-status");
const count = document.querySelector("#product-count");
const frame = document.querySelector("#product-surface");
const empty = document.querySelector("#surface-empty");
const interactionTitle = document.querySelector("#interaction-title");
const interactionDescription = document.querySelector(
  "#interaction-description",
);
const contractStatus = document.querySelector("#ui-contract-status");
const search = document.querySelector("#product-search");
const readinessFilter = document.querySelector("#readiness-filter");
const refresh = document.querySelector("#refresh-products");
const clearFilters = document.querySelector("#clear-filters");
let products = [];
let inventoryLoaded = false;
let selectedKey = "";
let disposeSurface = () => {};
let selection = 0;

/** Links are publisher metadata: expose only absolute, credential-free HTTPS destinations. */
function publicURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password
      ? url.href
      : null;
  } catch {
    return null;
  }
}

/** Render ownership and published interfaces without interpreting descriptor text as markup. */
function showDetails(product) {
  document.querySelector("#product-metadata").hidden = false;
  document.querySelector("#product-owner").textContent = product.owner.name;
  document.querySelector("#product-version").textContent = product.version;
  document.querySelector("#product-namespace").textContent = product.namespace;
  document.querySelector("#product-readiness").textContent = product.ready
    ? "Ready"
    : "Not ready";
  const readiness = document.querySelector("#readiness-detail");
  readiness.hidden = product.ready;
  readiness.textContent = `${product.readiness.reason}: ${product.readiness.message}`;
  const list = document.querySelector("#product-interfaces");
  list.replaceChildren();
  document.querySelector("#interfaces-detail").hidden =
    !product.outputs?.length;
  for (const output of product.outputs || []) {
    const item = document.createElement("li");
    const name = document.createElement("strong");
    name.textContent = output.name;
    const meta = document.createElement("span");
    meta.className = "interface-meta";
    meta.textContent = `${output.protocol} (${output.mediaType})`;
    item.append(name, meta);
    for (const [kind, label, value] of [
      ["api", "Open API", output.url],
      ["contract", "View contract", output.contractUrl],
    ]) {
      const url = publicURL(value);
      if (!url) continue;
      const link = document.createElement("a");
      link.textContent = label;
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.dataset.kind = kind;
      item.append(link);
    }
    list.append(item);
  }
}

/** Map bounded presentation signals to accessible host-owned wording. */
function surfaceState(state) {
  contractStatus.hidden = false;
  contractStatus.dataset.state = state;
  contractStatus.textContent = {
    loading: "Opening product interface…",
    ready: "Product interface connected.",
    error:
      "The product interface reported an error. Try its query again or select the product to retry.",
    timeout:
      "The product interface did not respond. Select the product to retry.",
  }[state];
}

/** Render declared references and current observations as inert text, including untrusted owner metadata. */
function showLineage(product) {
  const section = document.querySelector("#composition-detail");
  const list = document.querySelector("#product-lineage");
  list.replaceChildren();
  section.hidden = !product.inputs?.length;
  document.querySelector("#composition-status").textContent =
    product.composition
      ? `${product.composition.reason}: ${product.composition.message}`
      : "Composition has not been observed. These are the declared input references.";
  for (const input of product.inputs || []) {
    const edge = product.lineage?.find(
      (candidate) => candidate.name === input.name,
    );
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
  selectedKey = `${product.namespace}/${product.name}`;
  disposeSurface();
  disposeSurface = () => {};
  frame.removeAttribute("src");
  frame.hidden = true;
  contractStatus.hidden = true;
  document.querySelectorAll(".product-card").forEach((card) => {
    card.setAttribute("aria-pressed", String(card === button));
  });

  interactionTitle.textContent = product.displayName;
  showDetails(product);
  showLineage(product);
  interactionDescription.textContent = product.description;
  interactionTitle.focus({ preventScroll: true });
  if (matchMedia("(max-width: 52rem)").matches)
    interactionTitle.scrollIntoView({ block: "start" });

  if (!product.ready || !publicURL(product.ui?.url)) {
    frame.hidden = true;
    frame.removeAttribute("src");
    empty.hidden = false;
    empty.querySelector("p").textContent = product.ready
      ? "No embedded view is available. Use the published data interfaces above."
      : "The data view is unavailable until this product is ready.";
    return;
  }

  empty.hidden = true;
  if (product.ui.contract) {
    surfaceState("loading");
    try {
      const response = await fetch("/api/v1/ui-config", {
        cache: "no-store",
        credentials: "omit",
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok)
        throw new Error(
          "The host could not read its UI configuration. Select the product to retry.",
        );
      const configuration = await response.json();
      if (selected !== selection) return;
      if (configuration.uiContractEnabled !== true)
        throw new Error("Portable UI contracts are disabled on this host.");
      disposeSurface = DataProductUI.mount({
        frame,
        manifest: product.ui,
        grants: ["status", "resize"],
        onState: surfaceState,
      });
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
  button.setAttribute(
    "aria-pressed",
    String(selectedKey === `${product.namespace}/${product.name}`),
  );
  button.dataset.ready = String(product.ready);

  const copy = document.createElement("span");
  const name = document.createElement("span");
  name.className = "product-name";
  name.textContent = product.displayName;
  const description = document.createElement("span");
  description.className = "product-description";
  description.textContent = product.description;
  copy.append(name, description);

  const summary = document.createElement("span");
  summary.className = "product-summary";
  const owner = document.createElement("span");
  owner.textContent = product.owner.name;
  const readiness = document.createElement("span");
  readiness.className = "readiness";
  readiness.textContent = product.ready ? "Ready" : "Not ready";
  summary.append(owner, readiness);

  button.append(copy, summary);
  button.addEventListener("click", () => selectProduct(product, button));
  return button;
}

/** Filter the fetched inventory locally; searching never queries product data. */
function filterProducts() {
  if (!inventoryLoaded) return;
  const term = search.value.trim().toLocaleLowerCase();
  const filtered = products.filter((product) => {
    const text = [
      product.displayName,
      product.description,
      product.owner.name,
      product.namespace,
    ]
      .join(" ")
      .toLocaleLowerCase();
    return (
      text.includes(term) &&
      (readinessFilter.value === "all" ||
        product.ready === (readinessFilter.value === "ready"))
    );
  });
  grid.replaceChildren(...filtered.map(productCard));
  const total = `${products.length} ${products.length === 1 ? "product" : "products"}`;
  count.textContent =
    filtered.length === products.length
      ? total
      : `${filtered.length} of ${total}`;
  clearFilters.hidden = !term && readinessFilter.value === "all";
  status.textContent = !products.length
    ? "No products have been published."
    : !filtered.length
      ? "No products match these filters. Clear filters to see all products."
      : "";
}

/** Refresh invalidates the selected surface before re-reading readiness; failures remain retryable. */
async function loadProducts() {
  refresh.disabled = true;
  ++selection;
  selectedKey = "";
  disposeSurface();
  disposeSurface = () => {};
  frame.removeAttribute("src");
  frame.hidden = true;
  empty.hidden = true;
  contractStatus.hidden = true;
  for (const id of [
    "product-metadata",
    "readiness-detail",
    "interfaces-detail",
    "composition-detail",
  ])
    document.getElementById(id).hidden = true;
  interactionTitle.textContent = "Select a product";
  interactionDescription.textContent =
    "Choose a product to view its details and data.";
  grid.replaceChildren();
  products = [];
  inventoryLoaded = false;
  count.textContent = "Loading…";
  status.textContent = "Loading products…";
  try {
    const response = await fetch("/api/v1/products", {
      headers: { Accept: "application/json" },
      cache: "no-store",
      signal: AbortSignal.timeout(10000),
    });
    if (!response.ok) {
      throw new Error(`registry returned ${response.status}`);
    }

    const collection = await response.json();
    if (!Array.isArray(collection.products))
      throw new Error("Invalid product inventory");
    products = collection.products;
    inventoryLoaded = true;
    filterProducts();
  } catch (error) {
    count.textContent = "Unavailable";
    status.textContent =
      "Could not load products. Use Refresh products to retry.";
    console.error(error);
  } finally {
    refresh.disabled = false;
  }
}

search.addEventListener("input", filterProducts);
readinessFilter.addEventListener("change", filterProducts);
refresh.addEventListener("click", loadProducts);
clearFilters.addEventListener("click", () => {
  search.value = "";
  readinessFilter.value = "all";
  filterProducts();
  search.focus();
});
loadProducts();
