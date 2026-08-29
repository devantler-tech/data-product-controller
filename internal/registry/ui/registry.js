const grid = document.querySelector("#data-product-grid");
const status = document.querySelector("#registry-status");
const count = document.querySelector("#product-count");
const frame = document.querySelector("#product-surface");
const empty = document.querySelector("#surface-empty");
const interactionTitle = document.querySelector("#interaction-title");
const interactionDescription = document.querySelector("#interaction-description");

function selectProduct(product, button) {
  document.querySelectorAll(".product-card").forEach((card) => {
    card.setAttribute("aria-pressed", String(card === button));
  });

  interactionTitle.textContent = product.displayName;
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
