const form = document.querySelector("#query-form");
const station = document.querySelector("#station");
const status = document.querySelector("#status");
const results = document.querySelector("#observations");
// Embedded navigation belongs to the workspace; its sandbox blocks popup links.
if (parent !== window) {
  document.querySelector("nav[aria-label='Data access']")?.remove();
}
let connection;
let queryState = "ready";
let requestNumber = 0;
let activeRequest;
const hostConfiguration = fetch("ui-contract-config", {
  credentials: "omit",
  cache: "no-store",
})
  .then((response) => (response.ok ? response.json() : { hostOrigins: [] }))
  .catch(() => ({ hostOrigins: [] }));

/** Send only bounded presentation hints to the publisher-approved parent origin. */
function reportState() {
  if (!connection) return;
  const envelope = {
    apiVersion: "data-product-ui/v1",
    session: connection.session,
  };
  if (connection.capabilities.includes("status")) {
    parent.postMessage(
      { ...envelope, type: "status", state: queryState },
      connection.origin,
    );
  }
  if (connection.capabilities.includes("resize")) {
    parent.postMessage(
      {
        ...envelope,
        type: "resize",
        height: Math.min(
          1200,
          Math.max(240, document.documentElement.scrollHeight),
        ),
      },
      connection.origin,
    );
  }
}

/** The product uses its own origin policy, never a host-provided allowlist or user context. */
window.addEventListener("message", async (event) => {
  const data = event.data;
  if (
    event.source !== parent ||
    parent === window ||
    !data ||
    data.apiVersion !== "data-product-ui/v1" ||
    data.type !== "init" ||
    typeof data.session !== "string" ||
    !/^[a-f0-9-]{36}$/.test(data.session) ||
    !Array.isArray(data.capabilities) ||
    new Set(data.capabilities).size !== data.capabilities.length ||
    data.capabilities.length > 2 ||
    data.capabilities.some((value) => !["status", "resize"].includes(value)) ||
    Object.keys(data).sort().join(",") !==
      "apiVersion,capabilities,session,type"
  )
    return;
  const config = await hostConfiguration;
  if (
    !Array.isArray(config.hostOrigins) ||
    !config.hostOrigins.includes(event.origin)
  )
    return;
  connection = {
    session: data.session,
    origin: event.origin,
    capabilities: data.capabilities,
  };
  parent.postMessage(
    {
      apiVersion: "data-product-ui/v1",
      type: "ready",
      session: connection.session,
    },
    connection.origin,
  );
  reportState();
});

/** Render observations as semantic rows with UTC times and explicitly named units. */
function render(items) {
  const table = document.createElement("table");
  table.setAttribute("role", "table");
  const caption = table.createCaption();
  caption.textContent = "Harbour observations. Times are in UTC.";
  const headings = [
    "Station",
    "Observed at",
    "Temperature (°C)",
    "Salinity (PSU)",
  ];
  const header = table.createTHead().insertRow();
  header.setAttribute("role", "row");
  for (const [index, label] of headings.entries()) {
    const cell = document.createElement("th");
    cell.scope = "col";
    cell.setAttribute("role", "columnheader");
    cell.textContent = label;
    if (index > 1) cell.className = "numeric";
    header.append(cell);
  }
  const body = table.createTBody();
  for (const item of items) {
    const row = body.insertRow();
    row.className = "observation";
    row.setAttribute("role", "row");
    const stationCell = document.createElement("th");
    stationCell.scope = "row";
    stationCell.setAttribute("role", "rowheader");
    stationCell.textContent =
      { nordhavn: "Nordhavn", refshaleoen: "Refshaleoen" }[item.station] ||
      item.station;
    row.append(stationCell);
    const time = document.createElement("time");
    time.dateTime = item.observedAt;
    time.textContent = new Date(item.observedAt)
      .toISOString()
      .slice(0, 16)
      .replace("T", " ");
    const values = [
      time,
      item.temperatureCelsius.toFixed(1),
      item.salinityPsu.toFixed(1),
    ];
    for (const [index, value] of values.entries()) {
      const cell = row.insertCell();
      cell.setAttribute("role", "cell");
      cell.dataset.label = headings[index + 1];
      if (index > 0) cell.className = "numeric";
      cell.append(value);
    }
  }
  results.replaceChildren(table);
  status.textContent = `${items.length} observation${items.length === 1 ? "" : "s"}`;
  if (!items.length)
    status.textContent +=
      ". No observations match this station. Choose another station.";
}

/** A newer station query owns the view; abort and discard every older response. */
async function query() {
  const current = ++requestNumber;
  activeRequest?.abort();
  activeRequest = new AbortController();
  status.textContent = "Loading observations…";
  results.replaceChildren();
  results.setAttribute("aria-busy", "true");
  const parameter = station.value
    ? `?station=${encodeURIComponent(station.value)}`
    : "";
  try {
    const response = await fetch(`api/observations${parameter}`, {
      credentials: "omit",
      signal: AbortSignal.any([
        activeRequest.signal,
        AbortSignal.timeout(10000),
      ]),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const payload = await response.json();
    if (current !== requestNumber) return;
    if (
      !Array.isArray(payload.items) ||
      payload.items.some(
        (item) =>
          !item ||
          typeof item.station !== "string" ||
          !Number.isFinite(item.temperatureCelsius) ||
          !Number.isFinite(item.salinityPsu) ||
          typeof item.observedAt !== "string" ||
          !Number.isFinite(Date.parse(item.observedAt)),
      )
    ) {
      throw new Error("Invalid observation data");
    }
    render(payload.items);
    queryState = "ready";
  } catch (error) {
    if (current !== requestNumber) return;
    status.textContent = `Could not load observations (${error.name === "TimeoutError" ? "request timed out" : error.message}). Use Load observations to retry.`;
    results.replaceChildren();
    queryState = "error";
  } finally {
    if (current === requestNumber) results.setAttribute("aria-busy", "false");
  }
  if (current !== requestNumber) return;
  reportState();
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  query();
});

query();
