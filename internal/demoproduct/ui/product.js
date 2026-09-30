const form = document.querySelector("#query-form");
const station = document.querySelector("#station");
const status = document.querySelector("#status");
const results = document.querySelector("#observations");
let connection;
let queryState = "ready";
const hostConfiguration = fetch("ui-contract-config", { credentials: "omit", cache: "no-store" })
  .then((response) => response.ok ? response.json() : { hostOrigins: [] })
  .catch(() => ({ hostOrigins: [] }));

/** Send only bounded presentation hints to the publisher-approved parent origin. */
function reportState() {
  if (!connection) return;
  const envelope = { apiVersion: "data-product-ui/v1", session: connection.session };
  if (connection.capabilities.includes("status")) {
    parent.postMessage({ ...envelope, type: "status", state: queryState }, connection.origin);
  }
  if (connection.capabilities.includes("resize")) {
    parent.postMessage({ ...envelope, type: "resize", height: Math.min(1200, Math.max(240, document.documentElement.scrollHeight)) }, connection.origin);
  }
}

/** The product uses its own origin policy, never a host-provided allowlist or user context. */
window.addEventListener("message", async (event) => {
  const data = event.data;
  if (event.source !== parent || parent === window || !data || data.apiVersion !== "data-product-ui/v1"
    || data.type !== "init" || typeof data.session !== "string"
    || !/^[a-f0-9-]{36}$/.test(data.session) || !Array.isArray(data.capabilities)
    || new Set(data.capabilities).size !== data.capabilities.length
    || data.capabilities.length > 2 || data.capabilities.some((value) => !["status", "resize"].includes(value))
    || Object.keys(data).sort().join(",") !== "apiVersion,capabilities,session,type") return;
  const config = await hostConfiguration;
  if (!Array.isArray(config.hostOrigins) || !config.hostOrigins.includes(event.origin)) return;
  connection = { session: data.session, origin: event.origin, capabilities: data.capabilities };
  parent.postMessage({ apiVersion: "data-product-ui/v1", type: "ready", session: connection.session }, connection.origin);
  reportState();
});

function render(items) {
  results.replaceChildren(...items.map((item) => {
    const card = document.createElement("article");
    card.className = "observation";
    const heading = document.createElement("h2");
    heading.textContent = item.station;
    const temperature = document.createElement("p");
    temperature.className = "metric";
    const temperatureLabel = document.createElement("span");
    temperatureLabel.textContent = "Temperature";
    const temperatureValue = document.createElement("strong");
    temperatureValue.textContent = `${item.temperatureCelsius.toFixed(1)} °C`;
    temperature.append(temperatureLabel, temperatureValue);
    const salinity = document.createElement("p");
    salinity.className = "metric";
    const salinityLabel = document.createElement("span");
    salinityLabel.textContent = "Salinity";
    const salinityValue = document.createElement("strong");
    salinityValue.textContent = `${item.salinityPsu.toFixed(1)} PSU`;
    salinity.append(salinityLabel, salinityValue);
    card.append(heading, temperature, salinity);
    return card;
  }));
  status.textContent = `${items.length} observation${items.length === 1 ? "" : "s"}`;
}

async function query() {
  status.textContent = "Querying product…";
  const parameter = station.value ? `?station=${encodeURIComponent(station.value)}` : "";
  try {
    const response = await fetch(`api/observations${parameter}`);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const payload = await response.json();
    render(payload.items);
    queryState = "ready";
  } catch (error) {
    status.textContent = `The product could not answer: ${error.message}`;
    results.replaceChildren();
    queryState = "error";
  }
  reportState();
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  query();
});

query();
