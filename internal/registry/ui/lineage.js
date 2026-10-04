/* On-demand catalog metadata only. The host owns navigation and the product surface. */
window.DataProductLineage = (() => {
  const states = {
    ready: "Ready", "not-ready": "Not ready", stale: "Stale",
    unobserved: "Unobserved", disabled: "Disabled", deleting: "Deleting",
    missing: "Missing product", unavailable: "Unavailable", invalid: "Invalid public metadata",
    "identity-mismatch": "Unexpected product", timeout: "Timed out",
    "metadata-limit": "Metadata limit", "product-limit": "Product limit",
    "depth-limit": "Depth limit", "edge-limit": "Input limit",
    "cross-namespace": "Namespace boundary", cycle: "Circular dependency", resolved: "Inspected",
  };
  const contracts = {
    compatible: "Compatible", "output-missing": "Output missing",
    "contract-incompatible": "Incompatible", "not-evaluated": "Not evaluated",
  };
  const labelValid = value => typeof value === "string" && /^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/.test(value);
  const keyValid = value => typeof value === "string" && value.length <= 317 &&
    value.split("/").length === 2 && labelValid(value.split("/")[0]) &&
    value.split("/")[1].length <= 253 && value.split("/")[1].split(".").every(labelValid);
  const textValid = value => typeof value === "string" && value.length <= 16384;
  const closed = (value, required, optional = []) => value && typeof value === "object" &&
    !Array.isArray(value) && required.every(key => Object.hasOwn(value, key)) &&
    Object.keys(value).every(key => required.includes(key) || optional.includes(key));
  const observedStates = new Set(["ready", "not-ready", "stale", "unobserved", "disabled", "deleting"]);
  const failures = new Set(["missing", "unavailable", "invalid", "identity-mismatch", "timeout", "metadata-limit", "product-limit", "depth-limit", "edge-limit", "cross-namespace", "cycle"]);
  const safeGeneration = value => Number.isSafeInteger(value) && value >= 0;
  function validHealth(health) {
    return closed(health, ["source", "connector", "contracts", "composition"]) &&
      Object.values(health).every(check =>
        closed(check, ["state", "message", "generation", "observedGeneration"]) &&
        (observedStates.has(check.state) && check.state !== "deleting" || check.state === "not-applicable") &&
        textValid(check.message) && safeGeneration(check.generation) && safeGeneration(check.observedGeneration) &&
        (check.state !== "ready" || check.generation === check.observedGeneration));
  }

  function validTrace(trace, root) {
    if (!closed(trace, ["apiVersion", "root", "complete", "issues", "nodes", "edges"]) ||
        trace.apiVersion !== "data-product-lineage/v1" || trace.root !== root ||
        typeof trace.complete !== "boolean" || !Array.isArray(trace.nodes) ||
        !Array.isArray(trace.edges) || !Array.isArray(trace.issues) ||
        trace.nodes.length > 256 || trace.edges.length > 1024 || trace.issues.length > 11 ||
        (trace.complete && trace.issues.length) ||
        new Set(trace.issues).size !== trace.issues.length ||
        !trace.issues.every(issue => failures.has(issue)) ||
        (!trace.complete && !trace.issues.length)) return false;
    const keys = new Set();
    const observed = new Set();
    for (const node of trace.nodes) {
      if (!closed(node, ["key", "state", "generation", "observedGeneration"], ["id", "displayName", "version", "owner", "health"]) ||
          !keyValid(node.key) || node.key.split("/")[0] !== root.split("/")[0] ||
          keys.has(node.key) || (!observedStates.has(node.state) && !failures.has(node.state)) ||
          !safeGeneration(node.generation) || !safeGeneration(node.observedGeneration) ||
          (node.state === "ready" && node.generation !== node.observedGeneration) ||
          (node.displayName !== undefined && !textValid(node.displayName)) ||
          (node.version !== undefined && !textValid(node.version)) ||
          (node.id !== undefined && !textValid(node.id)) ||
          (node.owner !== undefined && (!closed(node.owner, ["name"], ["url"]) || !textValid(node.owner.name) ||
            (node.owner.url !== undefined && !textValid(node.owner.url)))) ||
          (node.health !== undefined && !validHealth(node.health))) return false;
      if (observedStates.has(node.state)) observed.add(node.key);
      else if (trace.complete || !trace.issues.includes(node.state)) return false;
      keys.add(node.key);
    }
    const inputs = new Set();
    return observed.has(root) && trace.edges.every(edge => {
      const inputKey = edge.from + "/" + edge.input;
      if (!closed(edge, ["from", "to", "input", "output", "depth", "state", "compatibility"], ["requirement"]) ||
          !observed.has(edge.from) || !keyValid(edge.to) || !labelValid(edge.input) || !labelValid(edge.output) ||
          inputs.has(inputKey) || !Object.hasOwn(states, edge.state) ||
          !Object.hasOwn(contracts, edge.compatibility) ||
          !Number.isInteger(edge.depth) || edge.depth < 1 || edge.depth > 64 ||
          (edge.requirement && (!closed(edge.requirement, ["protocol", "minimumVersion"]) ||
            !["OpenAPI", "AsyncAPI", "GraphQL", "DCAT", "ArrowFlight"].includes(edge.requirement.protocol) ||
            typeof edge.requirement.minimumVersion !== "string" || edge.requirement.minimumVersion.length > 64 ||
            !/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(edge.requirement.minimumVersion)))) return false;
      if (edge.state === "resolved") {
        if (!observed.has(edge.to) || edge.compatibility === "not-evaluated") return false;
      } else if (!failures.has(edge.state) || trace.complete || !trace.issues.includes(edge.state)) return false;
      if (edge.state !== "cross-namespace" && edge.to.split("/")[0] !== root.split("/")[0]) return false;
      inputs.add(inputKey);
      return true;
    });
  }

  async function boundedJSON(response) {
    const reader = response.body.getReader();
    const chunks = [];
    let bytes = 0;
    try {
      for (;;) {
        const {value, done} = await reader.read();
        if (done) break;
        bytes += value.byteLength;
        if (bytes > 2 * 1024 * 1024) throw new Error("Trace response is too large.");
        chunks.push(value);
      }
    } finally { await reader.cancel(); }
    const data = new Uint8Array(bytes);
    let offset = 0;
    for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.byteLength; }
    return JSON.parse(new TextDecoder("utf-8", {fatal: true}).decode(data));
  }

  function create({navigate, linkFor}) {
    const section = document.querySelector("#dependency-trace");
    const status = document.querySelector("#trace-status");
    const result = document.querySelector("#trace-result");
    const action = document.querySelector("#trace-inputs");
    const save = document.querySelector("#save-trace");
    const nodes = document.querySelector("#trace-table tbody");
    const edges = document.querySelector("#trace-edges tbody");
    let selected = null, current = 0, controller = null, snapshot = null;

    function reset() {
      ++current;
      controller?.abort();
      controller = null;
      selected = snapshot = null;
      section.hidden = result.hidden = save.hidden = true;
      section.removeAttribute("aria-busy");
      action.disabled = false;
      action.textContent = "Trace inputs";
      status.textContent = "";
      nodes.replaceChildren();
      edges.replaceChildren();
    }
    function select(product, enabled) {
      reset();
      const key = product.namespace + "/" + product.name;
      if (!enabled || !keyValid(key)) return;
      selected = key;
      section.hidden = false;
      status.textContent = "Trace this product's inputs to find upstream dependencies.";
    }
    const cell = value => {
      const element = document.createElement("td");
      element.textContent = value;
      return element;
    };
    function render(trace) {
      for (const node of trace.nodes) {
        const row = document.createElement("tr");
        row.dataset.state = node.state;
        const product = cell("");
        const name = document.createElement("strong");
        name.textContent = node.displayName || node.key;
        product.append(name);
        if (node.displayName) {
          const identity = document.createElement("span");
          identity.textContent = node.key;
          product.append(identity);
        }
        if (node.displayName && node.key !== selected &&
            node.key.split("/")[0] === selected.split("/")[0]) {
          const link = document.createElement("a");
          link.textContent = "Open product";
          link.href = linkFor(node.key);
          link.dataset.product = node.key;
          link.addEventListener("click", event => {
            if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
            event.preventDefault();
            navigate(node.key);
          });
          product.append(link);
        }
        const observation = cell(states[node.state]);
        if (node.displayName) {
          const generation = document.createElement("span");
          generation.textContent = "Observed " + node.observedGeneration + " / current " + node.generation;
          observation.append(generation);
        }
        row.append(product, cell([node.owner?.name, node.version].filter(Boolean).join(" · ") || "—"), observation);
        nodes.append(row);
      }
      for (const edge of trace.edges) {
        const row = document.createElement("tr");
        row.dataset.state = edge.state;
        const contract = cell(contracts[edge.compatibility]);
        if (edge.requirement) {
          const requirement = document.createElement("span");
          requirement.textContent = edge.requirement.protocol + " · minimum " + edge.requirement.minimumVersion;
          contract.append(requirement);
        }
        row.append(cell(edge.from + " / " + edge.input), cell(edge.to + " / " + edge.output), contract, cell(states[edge.state]));
        edges.append(row);
      }
      document.querySelector("#trace-findings").textContent =
        trace.issues.length ? trace.issues.map(issue => states[issue]).join(" · ") :
          trace.edges.length ? "Every declared input was inspected." : "This product declares no inputs.";
      status.textContent = (trace.complete ? "Trace complete" : "Incomplete trace") +
        " · " + trace.nodes.length + " products · " + trace.edges.length + " inputs.";
      result.hidden = save.hidden = false;
    }
    async function load() {
      if (!selected) return;
      controller?.abort();
      controller = new AbortController();
      const request = ++current, root = selected;
      snapshot = null;
      result.hidden = save.hidden = true;
      nodes.replaceChildren();
      edges.replaceChildren();
      action.disabled = true;
      status.textContent = "Tracing inputs…";
      section.setAttribute("aria-busy", "true");
      const [namespace, name] = root.split("/");
      try {
        const response = await fetch("/api/v2/products/" + encodeURIComponent(namespace) + "/" + encodeURIComponent(name) + "/lineage", {
          cache: "no-store", credentials: "same-origin",
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        });
        if (!response.ok) throw new Error("Trace unavailable.");
        const trace = await boundedJSON(response);
        if (request !== current) return;
        if (!validTrace(trace, root)) throw new Error("Invalid trace.");
        snapshot = trace;
        render(trace);
      } catch {
        if (request !== current) return;
        status.textContent = "Could not trace these inputs. Use Trace inputs to retry.";
      } finally {
        if (request === current) {
          action.disabled = false;
          section.removeAttribute("aria-busy");
        }
      }
    }
    action.addEventListener("click", load);
    save.addEventListener("click", () => {
      if (!selected || !snapshot || snapshot.root !== selected) return;
      const url = URL.createObjectURL(new Blob([JSON.stringify(snapshot)], {type: "application/json"}));
      const link = document.createElement("a");
      link.href = url;
      const [namespace, name] = selected.split("/");
      link.download = namespace + "-" + name.slice(0, 80) + "-lineage.json";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    });
    return {reset, select};
  }
  return {create};
})();
