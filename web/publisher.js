/* Local report inspection: no network requests, frames, admission or runtime feature changes. */
(() => {
  "use strict";
  const get = id => document.getElementById(id);
  const status = get("report-status");
  let selection = 0, report = null, selected = null, downloadURL = null;
  let products = new Map(), requirements = new Map();

  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  const key = product => product.namespace + "/" + product.name;
  const origin = entry => "Source " + entry.source + " · Document " + entry.document;
  const featureText = list => list.length ? list.join(", ") : "No feature requirements declared.";
  function revokeDownload() {
    if (downloadURL) URL.revokeObjectURL(downloadURL);
    downloadURL = null;
  }
  function clearProduct() {
    revokeDownload();
    selected = null;
    get("preview-title").textContent = "Select a product";
    get("preview-content").replaceChildren();
    get("product-requirements").textContent = "Select a product to inspect its requirements.";
    get("export-preview").disabled = true;
  }
  function resetFilters() {
    for (const id of ["severity-filter", "source-filter", "document-filter", "code-filter"]) get(id).value = "";
  }
  /** Replacement, clear and rejection withdraw every earlier result before reading untrusted bytes. */
  function revoke() {
    selection++;
    report = null;
    products = new Map();
    requirements = new Map();
    clearProduct();
    get("review").hidden = true;
    get("product-search").value = "";
    resetFilters();
    for (const id of ["report-summary", "source-list", "required-features", "requirement-origins", "product-list", "findings", "plan-order", "plan-edges"]) get(id).replaceChildren();
    get("source-filter").replaceChildren(new Option("All", ""));
    return selection;
  }

  function metadata(label, value) {
    const row = node("div");
    row.append(node("dt", label), node("dd", value));
    return row;
  }
  function selectProduct(productKey) {
    const product = products.get(productKey);
    if (!product) return;
    clearProduct();
    selected = productKey;
    get("preview-title").textContent = product.displayName;
    const content = get("preview-content");
    content.append(node("p", product.description));
    const meta = node("dl");
    for (const [label, value] of [["Product", productKey], ["ID", product.id], ["Owner", product.owner.name], ["Version", product.version]]) meta.append(metadata(label, value));
    content.append(meta, node("p", "Publication preview · Readiness unobserved · Generation 0", "muted"));
    if (product.ui) content.append(node("p", "Declared UI: " + product.ui.title + " · " + product.ui.url));
    else content.append(node("p", "No product UI declared.", "muted"));
    content.append(node("h4", "Public outputs"));
    const outputs = node("ul");
    for (const output of product.outputs) {
      const item = node("li");
      item.append(node("strong", output.name + " · " + output.protocol),
        node("p", output.url), node("p", "Contract: " + output.contractUrl, "muted"));
      outputs.append(item);
    }
    content.append(outputs);
    get("product-requirements").textContent = featureText(requirements.get(productKey) || []);
    get("export-preview").disabled = false;
    for (const button of get("product-list").querySelectorAll("button")) button.setAttribute("aria-pressed", String(button.dataset.key === selected));
    get("preview-title").focus();
  }
  function renderInventory() {
    if (!report) return;
    const term = get("product-search").value.trim().toLocaleLowerCase();
    const visible = [...products.entries()].filter(([productKey, product]) =>
      [productKey, product.id, product.displayName, product.owner.name, product.description].some(value => value.toLocaleLowerCase().includes(term)));
    if (selected && !visible.some(([productKey]) => productKey === selected)) clearProduct();
    const list = get("product-list");
    list.replaceChildren();
    for (const [productKey, product] of visible) {
      const button = node("button");
      button.type = "button";
      button.dataset.key = productKey;
      button.setAttribute("aria-pressed", String(selected === productKey));
      button.append(node("strong", product.displayName), node("span", productKey), node("span", product.owner.name));
      button.addEventListener("click", () => selectProduct(productKey));
      const item = node("li");
      item.append(button);
      list.append(item);
    }
    get("product-count").textContent = report.complete
      ? visible.length + " of " + products.size + " previews"
      : "Previews unavailable while the report is incomplete.";
  }

  function renderFindings() {
    if (!report) return;
    const severity = get("severity-filter").value, source = get("source-filter").value;
    const documentNumber = get("document-filter").value, code = get("code-filter").value.trim().toLocaleLowerCase();
    const visible = report.diagnostics.filter(finding =>
      (!severity || finding.severity === severity) &&
      (!source || String(finding.source) === source) &&
      (!documentNumber || String(finding.document) === documentNumber) &&
      (!code || finding.code.toLocaleLowerCase().includes(code)));
    get("finding-count").textContent = visible.length + " of " + report.diagnostics.length +
      " retained findings shown · " + report.diagnosticCounts.omitted + " omitted from the report";
    const list = get("findings");
    list.replaceChildren();
    if (!visible.length) list.append(node("li", report.diagnosticCounts.total
      ? "No retained findings match these filters." : "The report contains no findings."));
    for (const finding of visible) {
      const item = node("li");
      item.append(node("strong", finding.severity + " · " + finding.code), node("p", finding.message));
      const location = finding.source === 0 ? "Global finding" :
        "Source " + finding.source + (finding.document ? " · Document " + finding.document : " · Document location unavailable");
      const coordinates = finding.line ? " · Line " + finding.line + ", column " + finding.column : " · Line/column unavailable";
      item.append(node("p", location + coordinates, "location"), node("code", finding.path || "Field path unavailable"));
      if (finding.witness.length) {
        const detail = node("details");
        detail.append(node("summary", finding.witness.length + " witness steps" + (finding.witnessTruncated ? " · Truncated" : "")));
        const steps = node("ol");
        for (const step of finding.witness) steps.append(node("li", origin(step) + " · " + (step.path || "Field path unavailable")));
        detail.append(steps);
        item.append(detail);
      } else item.append(node("p", finding.witnessTruncated ? "Witness unavailable · Truncated" : "No witness supplied.", "muted"));
      list.append(item);
    }
  }
  function productButton(productKey) {
    const button = node("button", productKey);
    button.type = "button";
    button.addEventListener("click", () => {
      get("product-search").value = "";
      renderInventory();
      selectProduct(productKey);
      get("product-detail").scrollIntoView({block: "nearest"});
    });
    return button;
  }
  function renderPlan() {
    if (!report.plan) {
      get("plan-order").append(node("li", "No plan available while the report is incomplete."));
      return;
    }
    for (const entry of report.plan.order) {
      const item = node("li");
      item.append(productButton(entry.key), node("span", " · " + origin(entry), "muted"));
      get("plan-order").append(item);
    }
    for (const edge of report.plan.edges) {
      const item = node("li");
      item.append(productButton(edge.consumer), node("span", " input " + edge.input + " → "),
        productButton(edge.producer), node("span", " output " + edge.output));
      get("plan-edges").append(item);
    }
    if (!report.plan.edges.length) get("plan-edges").append(node("li", "No dependencies declared."));
  }

  function show(value) {
    report = value;
    const counts = report.diagnosticCounts;
    const summary = get("report-summary");
    summary.append(node("strong", (report.complete ? "Complete" : "Incomplete") + " · Metadata " + (report.valid ? "valid" : "invalid")));
    const totals = node("dl");
    for (const [label, value] of [
      ["Products", report.products], ["Sources", report.sources.length],
      ["Documents", report.sources.reduce((count, source) => count + source.documents, 0)],
      ["Errors", counts.errors], ["Warnings", counts.warnings], ["Omitted findings", counts.omitted],
    ]) totals.append(metadata(label, String(value)));
    summary.append(totals);
    for (const source of report.sources) {
      const row = node("tr");
      for (const value of [source.source, source.documents, source.products]) row.append(node("td", String(value)));
      get("source-list").append(row);
      get("source-filter").append(new Option("Source " + source.source, String(source.source)));
    }
    get("source-filter").append(new Option("Global", "0"));
    for (const feature of report.requiredFeatures) get("required-features").append(node("li", feature));
    if (!report.requiredFeatures.length) get("required-features").append(node("li", "No feature requirements declared."));
    const byOrigin = new Map(report.productFeatures.map(entry => [entry.source + "/" + entry.document, entry.requiredFeatures]));
    for (const entry of report.productFeatures) get("requirement-origins").append(node("li", origin(entry) + " · " + featureText(entry.requiredFeatures)));
    for (const product of report.descriptors) products.set(key(product), product);
    if (report.plan) for (const entry of report.plan.order) requirements.set(entry.key, byOrigin.get(entry.source + "/" + entry.document));
    renderInventory();
    renderFindings();
    renderPlan();
    get("review").hidden = false;
    status.dataset.state = "accepted";
    status.textContent = "Report accepted for local inspection.";
  }

  function replaceSelection() {
    revoke();
    status.dataset.state = "selected";
    status.textContent = "Selection changed. Read the report to inspect it.";
  }
  get("report-file").addEventListener("change", () => {
    get("report-text").value = "";
    replaceSelection();
  });
  get("report-text").addEventListener("input", () => {
    get("report-file").value = "";
    replaceSelection();
  });
  get("report-form").addEventListener("submit", async event => {
    event.preventDefault();
    const current = revoke();
    status.dataset.state = "reading";
    status.textContent = "Reading local report…";
    try {
      const files = get("report-file").files, pasted = get("report-text").value;
      if (files.length > 1 || (files.length && pasted.trim())) throw new Error();
      let source = pasted;
      if (files.length) {
        if (files[0].size > 2 << 20) throw new Error();
        const buffer = await files[0].arrayBuffer();
        if (buffer.byteLength > 2 << 20) throw new Error();
        source = new TextDecoder("utf-8", {fatal: true, ignoreBOM: true}).decode(buffer);
      }
      if (selection !== current) return;
      show(DataProductPreflightReport.parse(source));
    } catch {
      if (selection !== current) return;
      revoke();
      status.dataset.state = "rejected";
      status.textContent = "Report rejected. Choose one valid UTF-8 v2 JSON file or paste one report, up to 2 MiB. Earlier results are cleared.";
    }
  });
  get("clear-report").addEventListener("click", () => {
    revoke();
    get("report-file").value = "";
    get("report-text").value = "";
    status.dataset.state = "cleared";
    status.textContent = "Report cleared.";
  });
  get("product-search").addEventListener("input", renderInventory);
  for (const id of ["severity-filter", "source-filter", "document-filter", "code-filter"]) get(id).addEventListener("input", renderFindings);
  get("reset-filters").addEventListener("click", () => { resetFilters(); renderFindings(); });
  get("export-preview").addEventListener("click", () => {
    const product = products.get(selected);
    if (!report?.complete || !product) return;
    revokeDownload();
    downloadURL = URL.createObjectURL(new Blob([JSON.stringify(product)], {type: "application/json"}));
    const anchor = node("a");
    anchor.href = downloadURL;
    anchor.download = (product.namespace + "-" + product.name).slice(0, 240) + ".json";
    anchor.click();
    const exported = downloadURL;
    setTimeout(() => { if (downloadURL === exported) revokeDownload(); }, 0);
  });

  const theme = get("theme"), system = matchMedia("(prefers-color-scheme: dark)");
  function applyTheme() {
    document.documentElement.dataset.theme = theme.value === "System"
      ? (system.matches ? "dark" : "light") : theme.value.toLowerCase();
  }
  try {
    const saved = localStorage.getItem("dpc-publisher-theme");
    if (["System", "Light", "Dark"].includes(saved)) theme.value = saved;
  } catch { /* Theme controls work without storage. */ }
  theme.addEventListener("change", () => {
    applyTheme();
    try { localStorage.setItem("dpc-publisher-theme", theme.value); } catch { /* Keep the live preference. */ }
  });
  system.addEventListener("change", applyTheme);
  applyTheme();
  window.addEventListener("pagehide", () => {
    revoke();
    status.dataset.state = "cleared";
    status.textContent = "Report cleared.";
    system.removeEventListener("change", applyTheme);
  });
  window.addEventListener("pageshow", () => { system.addEventListener("change", applyTheme); applyTheme(); });
})();
