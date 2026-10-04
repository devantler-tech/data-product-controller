const appearance = document.querySelector("#appearance");
const systemAppearance = matchMedia("(prefers-color-scheme: dark)");

/** Resolve System locally; the embedded product receives only a cosmetic enum. */
function resolvedAppearance() {
  return appearance.value === "system"
    ? systemAppearance.matches
      ? "dark"
      : "light"
    : appearance.value;
}

/** Keep an explicit browser preference optional; denied storage must not prevent queries. */
function applyAppearance() {
  if (appearance.value === "system") {
    delete document.documentElement.dataset.appearance;
  } else {
    document.documentElement.dataset.appearance = appearance.value;
  }
  disposeSurface.setAppearance?.(resolvedAppearance());
}

try {
  const saved = localStorage.getItem("data-products.appearance");
  if (saved === "light" || saved === "dark") appearance.value = saved;
} catch {
  // Browser policy may deny storage. System appearance remains available.
}

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
let discoveryEnabled = false;
let lineageEnabled = false;
const dependencyTrace = DataProductLineage.create({
  navigate: key => navigateProduct(key, true),
  linkFor: key => {
    const [namespace, name] = key.split("/");
    return productURL({namespace, name}).href;
  },
});
let continuation = "";
let catalogNamespace = "";
let inventoryComplete = false;
let rejectedProducts = 0;
let pageFailure = "";
let inventoryRequest = 0;
let selectedDescriptor = null;
const more = document.querySelector("#load-more");
const scope = document.querySelector("#discovery-scope");
const namespace = document.querySelector("#namespace-filter");
const selectionStatus = document.querySelector("#selection-status");
applyAppearance();
systemAppearance.addEventListener("change", applyAppearance);
appearance.addEventListener("change", () => {
  applyAppearance();
  try {
    localStorage.setItem("data-products.appearance", appearance.value);
  } catch {
    // The current choice still works when it cannot be retained.
  }
});

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
  showHealth(product);
  document.querySelector("#descriptor-actions").hidden = !discoveryEnabled;
  if (discoveryEnabled) {
    document.querySelector("#product-link").href = productURL(product).href;
    selectedDescriptor = product;
  }
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
    meta.textContent = output.mediaType ? `${output.protocol} (${output.mediaType})` : output.protocol;
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

/** Health is a point-in-time observation; absent or stale checks never appear healthy. */
function showHealth(product) {
  const section = document.querySelector("#health-detail");
  section.hidden = !discoveryEnabled || !product.health;
  const list = document.querySelector("#product-health");
  list.replaceChildren();
  if (section.hidden) return;
  document.querySelector("#health-generation").textContent =
    `Product generation ${product.generation}. Readiness observed at generation ${product.observedGeneration}.`;
  const labels = {source: "Source", connector: "Connector", contracts: "Contracts", composition: "Composition"};
  const states = {ready: "Ready", "not-ready": "Not ready", stale: "Stale", unobserved: "Unobserved", disabled: "Disabled", "not-applicable": "Not applicable"};
  for (const [key, label] of Object.entries(labels)) {
    const check = product.health[key];
    const item = document.createElement("li");
    const name = document.createElement("strong");
    name.textContent = `${label} · ${states[check?.state] || "Unobserved"}`;
    const detail = document.createElement("span");
    detail.textContent = check
      ? `${check.message} Observed generation ${check.observedGeneration}; current generation ${check.generation}.`
      : "No observation is available.";
    item.append(name, detail);
    list.append(item);
  }
}

/** A product link contains catalog identity only, never a publisher URL or session. */
function productURL(product) {
  const url = new URL(location.href);
  url.searchParams.set("product", `${product.namespace}/${product.name}`);
  return url;
}

/** Match the bounded Kubernetes DNS subdomain identity used by exact lookup. */
function validProductName(value) {
  return typeof value === "string" && value.length <= 253 &&
    /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$/.test(value);
}

/** Validate route identities before making any exact-name request. */
function routeKey() {
  const parameters = new URLSearchParams(location.search);
  const value = parameters.get("product");
  if (value === null) return null;
  if (parameters.getAll("product").length !== 1) return false;
  const components = value.split("/");
  return components.length === 2 &&
    /^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/.test(components[0]) &&
    validProductName(components[1]) ? value : false;
}

/** Revoke the current frame and hide descriptors before a new navigation or refresh. */
function clearSelection() {
  dependencyTrace.reset();
  ++selection;
  selectedKey = "";
  selectedDescriptor = null;
  disposeSurface();
  disposeSurface = () => {};
  frame.removeAttribute("src");
  frame.hidden = true;
  empty.hidden = true;
  contractStatus.hidden = true;
  selectionStatus.hidden = true;
  for (const id of ["product-metadata", "readiness-detail", "interfaces-detail", "composition-detail", "health-detail", "descriptor-actions"])
    document.getElementById(id).hidden = true;
  document.querySelectorAll(".product-card").forEach(card => card.setAttribute("aria-pressed", "false"));
  interactionTitle.textContent = "Select a product";
  interactionDescription.textContent = "Choose a product to view its details and data.";
}

/** Re-read a linked product; cached cards never authorize an iframe navigation. */
async function navigateProduct(key, push = false) {
  clearSelection();
  const selected = selection;
  if (push && key) {
    const [namespace, name] = key.split("/");
    const url = productURL({namespace, name});
    if (url.href !== location.href) history.pushState(null, "", url);
  }
  if (key === null) return;
  selectionStatus.hidden = false;
  if (key === false) {
    selectionStatus.textContent = "This product link is invalid. Select a product from the catalog.";
    return;
  }
  selectionStatus.textContent = "Loading selected product…";
  const [namespace, name] = key.split("/");
  try {
    const response = await fetch(`/api/v2/products/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`, {
      cache: "no-store", credentials: "same-origin", signal: AbortSignal.timeout(10000),
    });
    if (selected !== selection) return;
    if (!response.ok) {
      if (response.status === 404) throw new Error("This product is no longer available. Select another product.");
      throw new Error("Could not load the selected product. Open its link again or select it to retry.");
    }
    const product = await response.json();
    if (selected !== selection) return;
    if (product.apiVersion !== "data-product-descriptor/v1" || `${product.namespace}/${product.name}` !== key)
      throw new Error("The registry returned an invalid product descriptor.");
    selectionStatus.hidden = true;
    await selectProduct(product);
  } catch (error) {
    if (selected !== selection) return;
    selectionStatus.hidden = false;
    selectionStatus.textContent = error.message;
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
    const targetNamespace = ref.namespace || product.namespace;
    if (discoveryEnabled && edge?.ready === true && edge.reason === "InputReady" &&
        product.health?.composition?.state === "ready" &&
        product.health.composition.observedGeneration === product.generation &&
        product.health.composition.generation === product.generation &&
        ref.name === input.productRef.name && ref.output === input.productRef.output &&
        targetNamespace === (input.productRef.namespace || product.namespace) &&
        targetNamespace === product.namespace &&
        validProductName(ref.name)) {
      const link = document.createElement("a");
      link.className = "lineage-link";
      link.textContent = "Open upstream product";
      link.href = productURL({namespace: targetNamespace, name: ref.name}).href;
      link.addEventListener("click", event => {
        if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        navigateProduct(`${targetNamespace}/${ref.name}`, true);
      });
      item.append(link);
    }
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
  dependencyTrace.select(product, discoveryEnabled && lineageEnabled);
  const selected = ++selection;
  selectedKey = `${product.namespace}/${product.name}`;
  disposeSurface();
  disposeSurface = () => {};
  frame.removeAttribute("src");
  frame.hidden = true;
  contractStatus.hidden = true;
  document.querySelectorAll(".product-card").forEach((card) => {
    card.setAttribute("aria-pressed", String(card.dataset.key === selectedKey));
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
        credentials: "same-origin",
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
        grants:
          configuration.uiAppearanceEnabled === true
            ? ["status", "resize", "appearance"]
            : ["status", "resize"],
        appearanceEnabled: configuration.uiAppearanceEnabled === true,
        appearance: resolvedAppearance(),
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
  button.dataset.key = `${product.namespace}/${product.name}`;

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
  button.addEventListener("click", () => discoveryEnabled
    ? navigateProduct(button.dataset.key, true)
    : selectProduct(product, button));
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
  scope.hidden = !discoveryEnabled;
  scope.textContent = !inventoryComplete
    ? `Search covers ${products.length} loaded ${products.length === 1 ? "product" : "products"}. ${continuation ? "More products are available." : "Discovery is incomplete. Refresh products to restart."}`
    : rejectedProducts ? "Discovery finished for this namespace scope." : "All products in this namespace scope are loaded.";
  if (rejectedProducts)
    scope.textContent += ` ${rejectedProducts} ${rejectedProducts === 1 ? "product omitted" : "products omitted"}: public metadata is invalid or too large. Ask the publisher to correct it.`;
  status.textContent = !products.length
    ? !inventoryComplete ? "No products on this page. Discovery is incomplete." : rejectedProducts ? "No valid product descriptors are available in this scope." : "No products have been published in this scope."
    : !filtered.length
      ? !inventoryComplete
        ? "No products match among loaded products. Load more or clear filters."
        : "No products match these filters. Clear filters to see all products."
      : pageFailure;
  if (pageFailure && !status.textContent.includes(pageFailure))
    status.textContent += ` ${pageFailure}`;
}

/** Validate the page's bounded omission indicator before committing inventory state. */
function validDiscoveryPage(collection) {
  return collection.apiVersion === "data-product-discovery/v1" && Array.isArray(collection.products) &&
    Number.isInteger(collection.rejected) && collection.rejected >= 0 &&
    collection.rejected <= 100 && collection.products.length + collection.rejected <= 100 &&
    typeof collection.continue === "string";
}

/** Refresh invalidates the selected surface before re-reading readiness; failures remain retryable. */
async function loadProducts() {
  const request = ++inventoryRequest;
  refresh.disabled = true;
  clearSelection();
  continuation = "";
  inventoryComplete = false;
  rejectedProducts = 0;
  pageFailure = "";
  more.hidden = true;
  more.disabled = false;
  catalogNamespace = namespace.value.trim();
  grid.replaceChildren();
  products = [];
  inventoryLoaded = false;
  count.textContent = "Loading…";
  status.textContent = "Loading products…";
  try {
    const configurationResponse = await fetch("/api/v1/ui-config", {cache: "no-store", signal: AbortSignal.timeout(5000)});
    const configuration = configurationResponse.ok ? await configurationResponse.json() : {};
    if (request !== inventoryRequest) return;
    discoveryEnabled = configuration.discoveryEnabled === true;
    lineageEnabled = configuration.lineageEnabled === true;
    document.querySelector("#catalog-scope").hidden = !discoveryEnabled;
    if (discoveryEnabled) navigateProduct(routeKey());
    const query = new URLSearchParams({limit: "50"});
    if (catalogNamespace) query.set("namespace", catalogNamespace);
    const endpoint = discoveryEnabled ? `/api/v2/products?${query}` : "/api/v1/products";
    const response = await fetch(endpoint, {
      headers: { Accept: "application/json" },
      cache: "no-store",
      signal: AbortSignal.timeout(10000),
    });
    if (!response.ok) {
      throw new Error(`registry returned ${response.status}`);
    }

    const collection = await response.json();
    if (request !== inventoryRequest) return;
    if (!Array.isArray(collection.products) ||
        (discoveryEnabled && !validDiscoveryPage(collection)))
      throw new Error("Invalid product inventory");
    products = collection.products;
    rejectedProducts = discoveryEnabled ? collection.rejected : 0;
    continuation = discoveryEnabled ? collection.continue || "" : "";
    inventoryComplete = !continuation;
    more.hidden = !continuation;
    inventoryLoaded = true;
    filterProducts();
  } catch (error) {
    if (request !== inventoryRequest) return;
    count.textContent = "Unavailable";
    status.textContent =
      "Could not load products. Use Refresh products to retry.";
    console.error(error);
  } finally {
    if (request === inventoryRequest) refresh.disabled = false;
  }
}

/** Load one explicit page; a failed page retains its cursor and all already loaded cards. */
async function loadMore() {
  if (!continuation || more.disabled) return;
  const request = inventoryRequest;
  const query = new URLSearchParams({limit: "50", continue: continuation});
  if (catalogNamespace) query.set("namespace", catalogNamespace);
  more.disabled = true;
  try {
    const response = await fetch(`/api/v2/products?${query}`, {cache: "no-store", signal: AbortSignal.timeout(10000)});
    if (request !== inventoryRequest) return;
    if (!response.ok) {
      if (response.status === 410) {
        continuation = "";
        more.hidden = true;
        throw new Error("The catalog page expired. Use Refresh products to restart discovery.");
      }
      throw new Error("Could not load more products. Use Load more products to retry.");
    }
    const collection = await response.json();
    if (request !== inventoryRequest) return;
    if (!validDiscoveryPage(collection))
      throw new Error("The registry returned an invalid catalog page. Use Refresh products to restart.");
    products = [...new Map([...products, ...collection.products].map(product => [`${product.namespace}/${product.name}`, product])).values()];
    rejectedProducts += collection.rejected;
    continuation = collection.continue || "";
    inventoryComplete = !continuation;
    pageFailure = "";
    more.hidden = !continuation;
    filterProducts();
  } catch (error) {
    if (request === inventoryRequest) {
      pageFailure = error.message;
      filterProducts();
    }
  } finally {
    if (request === inventoryRequest) more.disabled = false;
  }
}

/** Download only the observed public descriptor, with its snapshot semantics unchanged. */
function saveDescriptor() {
  if (!discoveryEnabled || !selectedDescriptor) return;
  const url = URL.createObjectURL(new Blob([JSON.stringify(selectedDescriptor)], {type: "application/json"}));
  const link = document.createElement("a");
  link.href = url;
  link.download = `${selectedDescriptor.namespace}-${selectedDescriptor.name}.json`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

search.addEventListener("input", filterProducts);
readinessFilter.addEventListener("change", filterProducts);
refresh.addEventListener("click", loadProducts);
more.addEventListener("click", loadMore);
document.querySelector("#export-descriptor").addEventListener("click", saveDescriptor);
document.querySelector("#catalog-scope").addEventListener("submit", event => {
  event.preventDefault();
  const value = namespace.value.trim();
  if (value && !/^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/.test(value)) {
    status.textContent = "Enter a valid namespace or leave it empty for all namespaces.";
    return;
  }
  loadProducts();
});
addEventListener("popstate", () => { if (discoveryEnabled) navigateProduct(routeKey()); });
clearFilters.addEventListener("click", () => {
  search.value = "";
  readinessFilter.value = "all";
  filterProducts();
  search.focus();
});
loadProducts();
