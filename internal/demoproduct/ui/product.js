const form = document.querySelector("#query-form");
const station = document.querySelector("#station");
const status = document.querySelector("#status");
const results = document.querySelector("#observations");
const releaseProduct =
  document.querySelector('meta[name="product-kind"]')?.content ===
  "ksail-releases";
let releaseItems = [];
let activeDocument = true;
let initGeneration = 0;
// Embedded navigation belongs to the workspace; its sandbox blocks popup links.
if (parent !== window) {
  document.querySelector("nav[aria-label='Data access']")?.remove();
}
let connection;
let queryState = "ready";
let requestNumber = 0;
let activeRequest;
function loadHostConfiguration() {
  return fetch("ui-contract-config", {
    credentials: "omit",
    cache: "no-store",
    redirect: "error",
    signal: AbortSignal.timeout(10000),
  })
    .then((response) => (response.ok ? response.json() : { hostOrigins: [] }))
    .catch(() => ({ hostOrigins: [] }));
}
let hostConfiguration = loadHostConfiguration();

/** Send only bounded presentation hints to the publisher-approved parent origin. */
function reportState() {
  if (!connection) return;
  const envelope = {
    apiVersion: connection.apiVersion,
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
  if (!data || typeof data !== "object" || Array.isArray(data)) return;
  if (
    connection &&
    connection.apiVersion === "data-product-ui/v2" &&
    connection.capabilities.includes("appearance") &&
    event.source === parent &&
    event.origin === connection.origin &&
    data.apiVersion === connection.apiVersion &&
    data.type === "appearance" &&
    data.session === connection.session &&
    Object.keys(data).sort().join(",") ===
      "apiVersion,appearance,session,type" &&
    ["light", "dark"].includes(data.appearance)
  ) {
    if (++connection.appearanceUpdates > 256) {
      connection = undefined;
      return;
    }
    document.documentElement.dataset.appearance = data.appearance;
    return;
  }
  if (
    event.source !== parent ||
    parent === window ||
    !["data-product-ui/v1", "data-product-ui/v2"].includes(data.apiVersion) ||
    data.type !== "init" ||
    typeof data.session !== "string" ||
    !/^[a-f0-9-]{36}$/.test(data.session) ||
    !Array.isArray(data.capabilities) ||
    new Set(data.capabilities).size !== data.capabilities.length ||
    data.capabilities.length >
      (data.apiVersion === "data-product-ui/v2" ? 3 : 2) ||
    data.capabilities.some(
      (value) =>
        !(
          data.apiVersion === "data-product-ui/v2"
            ? ["status", "resize", "appearance"]
            : ["status", "resize"]
        ).includes(value),
    ) ||
    Object.keys(data).sort().join(",") !==
      "apiVersion,capabilities,session,type"
  )
    return;
  const generation = ++initGeneration;
  const config = await hostConfiguration;
  if (
    !activeDocument ||
    generation !== initGeneration ||
    !Array.isArray(config.hostOrigins) ||
    !config.hostOrigins.includes(event.origin) ||
    (data.apiVersion === "data-product-ui/v2" &&
      config.appearanceEnabled !== true)
  )
    return;
  connection = {
    session: data.session,
    apiVersion: data.apiVersion,
    origin: event.origin,
    capabilities: data.capabilities,
    appearanceUpdates: 0,
  };
  delete document.documentElement.dataset.appearance;
  parent.postMessage(
    {
      apiVersion: connection.apiVersion,
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
  if (releaseProduct) return queryReleases();
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

/** Validate a complete default page and retain only the fields this view displays. */
function validateReleasePage(payload) {
  if (!Array.isArray(payload) || payload.length > 30)
    throw new Error("Invalid release page");
  const seen = new Set();
  return payload.map((item) => {
    if (
      !item ||
      typeof item !== "object" ||
      Array.isArray(item) ||
      typeof item.tag_name !== "string" ||
      item.tag_name.length > 128 ||
      !/^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(
        item.tag_name,
      ) ||
      seen.has(item.tag_name) ||
      item.draft !== false ||
      typeof item.prerelease !== "boolean" ||
      typeof item.published_at !== "string" ||
      !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(item.published_at) ||
      !Number.isFinite(Date.parse(item.published_at)) ||
      new Date(item.published_at).toISOString().slice(0, 19) + "Z" !==
        item.published_at
    )
      throw new Error("Invalid release metadata");
    seen.add(item.tag_name);
    return {
      version: item.tag_name,
      publishedAt: item.published_at,
      prerelease: item.prerelease,
    };
  });
}

/** Decode bounded streamed UTF-8; a bad page never produces partial rows. */
async function readReleasePage(response) {
  const type = (response.headers.get("content-type") || "")
    .split(";")[0]
    .trim()
    .toLowerCase();
  if (
    response.status !== 200 ||
    response.redirected ||
    !response.body ||
    !["application/json", "application/vnd.github+json"].includes(type)
  )
    throw new Error("Unavailable release response");
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let bytes = 0;
  let text = "";
  try {
    for (;;) {
      const part = await reader.read();
      if (part.done) break;
      bytes += part.value.byteLength;
      if (bytes > 1024 * 1024) throw new Error("Release page too large");
      text += decoder.decode(part.value, { stream: true });
    }
    return validateReleasePage(JSON.parse(text + decoder.decode()));
  } finally {
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** Filters reuse the loaded page; displayed metadata is always plain text. */
function renderReleases() {
  const search = document.querySelector("#release-search").value.toLowerCase();
  const items = releaseItems.filter(
    (item) =>
      item.version.toLowerCase().includes(search) &&
      (station.value === "" ||
        (station.value === "prerelease") === item.prerelease),
  );
  const table = document.createElement("table");
  table.setAttribute("role", "table");
  table.createCaption().textContent =
    "Published KSail versions. Times are in UTC.";
  const headings = ["Version", "Published at", "Channel"];
  const header = table.createTHead().insertRow();
  for (const label of headings) {
    const cell = document.createElement("th");
    cell.scope = "col";
    cell.textContent = label;
    header.append(cell);
  }
  const body = table.createTBody();
  for (const item of items) {
    const row = body.insertRow();
    row.className = "release";
    const version = document.createElement("th");
    version.scope = "row";
    version.textContent = item.version;
    row.append(version);
    const time = document.createElement("time");
    time.dateTime = item.publishedAt;
    time.textContent = item.publishedAt.slice(0, 16).replace("T", " ");
    for (const [index, value] of [
      time,
      item.prerelease ? "Pre-release" : "Stable",
    ].entries()) {
      const cell = row.insertCell();
      cell.dataset.label = headings[index + 1];
      cell.append(value);
    }
  }
  results.replaceChildren(table);
  status.textContent = `${items.length} of ${releaseItems.length} releases`;
  if (!releaseItems.length)
    status.textContent =
      "GitHub returned no published releases. Use Refresh releases to check again.";
  else if (!items.length)
    status.textContent =
      "No releases match. Clear the version filter or choose another channel.";
}

/** Refresh and navigation revoke earlier reads before they can update the view. */
async function queryReleases() {
  const current = ++requestNumber;
  activeRequest?.abort();
  const controller = new AbortController();
  activeRequest = controller;
  releaseItems = [];
  results.replaceChildren();
  results.setAttribute("aria-busy", "true");
  status.textContent = "Loading releases…";
  queryState = "loading";
  reportState();
  try {
    const response = await fetch(
      "https://api.github.com/repos/devantler-tech/ksail/releases",
      {
        credentials: "omit",
        cache: "no-store",
        redirect: "error",
        referrerPolicy: "no-referrer",
        signal: AbortSignal.any([
          controller.signal,
          AbortSignal.timeout(10000),
        ]),
      },
    );
    const items = await readReleasePage(response);
    if (current !== requestNumber || !activeDocument) return;
    releaseItems = items;
    renderReleases();
    queryState = "ready";
  } catch {
    if (current !== requestNumber || !activeDocument) return;
    releaseItems = [];
    results.replaceChildren();
    status.textContent =
      "Could not read GitHub releases. Use Refresh releases to retry.";
    queryState = "error";
  } finally {
    if (current === requestNumber) results.setAttribute("aria-busy", "false");
  }
  if (current === requestNumber && activeDocument) reportState();
}

if (releaseProduct) {
  const filterLoadedPage = () => {
    if (queryState === "ready") renderReleases();
  };
  document
    .querySelector("#release-search")
    .addEventListener("input", filterLoadedPage);
  station.addEventListener("change", filterLoadedPage);
}

window.addEventListener("pagehide", () => {
  activeDocument = false;
  ++initGeneration;
  connection = undefined;
  if (releaseProduct) {
    ++requestNumber;
    activeRequest?.abort();
    releaseItems = [];
    results.replaceChildren();
  }
});
window.addEventListener("pageshow", (event) => {
  if (!event.persisted) return;
  activeDocument = true;
  hostConfiguration = loadHostConfiguration();
  if (releaseProduct) queryReleases();
});

form.addEventListener("submit", (event) => {
  event.preventDefault();
  query();
});

query();
