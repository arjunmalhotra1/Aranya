// Fetches cluster state from the operator's API and renders it.
"use strict";

// el builds a DOM element. Text is always set with textContent, never
// innerHTML, so names coming from the cluster cannot inject markup.
function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function formatCores(millis) {
  return (millis / 1000).toFixed(millis % 1000 === 0 ? 0 : 1);
}

function formatGiB(bytes) {
  return (bytes / 2 ** 30).toFixed(1);
}

// resourceBar shows how much of a node's capacity is allocatable to pods.
function resourceBar(label, allocatable, capacity, format, unit) {
  const row = el("div", "resource");
  row.title = "Share of capacity available to pods (not live usage)";
  row.append(el("span", "resource-label", label));

  const bar = el("div", "bar");
  const fill = el("div", "bar-fill");
  const percent = capacity > 0 ? Math.round((allocatable / capacity) * 100) : 0;
  fill.style.width = percent + "%";
  bar.append(fill);
  row.append(bar);

  row.append(el("span", "resource-value", `${format(allocatable)} of ${format(capacity)} ${unit} allocatable`));
  return row;
}

// A condition is a problem when Ready is not True, or any other condition
// (MemoryPressure, DiskPressure, ...) is True.
function isProblem(condition) {
  return condition.type === "Ready" ? condition.status !== "True" : condition.status === "True";
}

// selectedNode is the node whose pods are shown, or null.
let selectedNode = null;

function nodeCard(node) {
  const card = el("article", node.name === selectedNode ? "node-card selected" : "node-card");
  card.dataset.node = node.name;
  // Clickable with mouse and keyboard (Tab to focus, Enter or Space to open).
  card.tabIndex = 0;
  card.setAttribute("role", "button");
  card.setAttribute("aria-label", `Show pods on ${node.name}`);
  card.addEventListener("click", () => selectNode(node.name));
  card.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      selectNode(node.name);
    }
  });

  const header = el("div", "node-header");
  header.append(el("h2", "node-name", node.name));
  header.append(el("span", node.ready ? "badge ready" : "badge not-ready", node.ready ? "Ready" : "Not Ready"));
  card.append(header);

  card.append(resourceBar("CPU", node.allocatable.cpuMillis, node.capacity.cpuMillis, formatCores, "cores"));
  card.append(resourceBar("Memory", node.allocatable.memoryBytes, node.capacity.memoryBytes, formatGiB, "GiB"));
  card.append(el("p", "pod-count", `${node.podCount} ${node.podCount === 1 ? "pod" : "pods"}`));

  const conditions = el("ul", "conditions");
  for (const c of node.conditions) {
    const item = el("li", isProblem(c) ? "condition problem" : "condition", `${c.type}=${c.status}`);
    if (c.message) item.title = c.message;
    conditions.append(item);
  }
  card.append(conditions);
  return card;
}

// loadNodes redraws the node grid and reports whether it succeeded.
async function loadNodes() {
  const section = document.getElementById("nodes");
  try {
    const response = await fetch("/api/nodes");
    if (!response.ok) throw new Error(`GET /api/nodes returned ${response.status}`);
    const nodes = await response.json();

    // Redrawing replaces the cards, so remember which one had keyboard focus.
    const focused = document.activeElement?.dataset?.node;
    const grid = el("div", "node-grid");
    for (const node of nodes) grid.append(nodeCard(node));
    if (nodes.length === 0) grid.append(el("p", "empty", "No nodes found."));
    section.replaceChildren(el("h2", "section-title", `Nodes (${nodes.length})`), grid);
    if (focused) grid.querySelector(`[data-node="${CSS.escape(focused)}"]`)?.focus();
    return true;
  } catch (err) {
    section.replaceChildren(el("p", "error", `Could not load nodes: ${err.message}`));
    return false;
  }
}

function selectNode(name) {
  selectedNode = name;
  for (const card of document.querySelectorAll(".node-card")) {
    card.classList.toggle("selected", card.dataset.node === name);
  }
  loadPods(name);
}

const phaseClass = {
  Running: "phase running",
  Pending: "phase pending",
  Failed: "phase failed",
  Succeeded: "phase succeeded",
};

function podTable(pods) {
  const table = el("table", "pod-table");
  const head = el("tr");
  for (const title of ["Namespace", "Name", "Phase", "Ready", "Restarts"]) head.append(el("th", "", title));
  table.append(el("thead"));
  table.tHead.append(head);

  const body = el("tbody");
  for (const pod of pods) {
    const row = el("tr");
    row.append(el("td", "", pod.namespace));
    row.append(el("td", "pod-name", pod.name));
    const phase = el("td");
    phase.append(el("span", phaseClass[pod.phase] || "phase unknown", pod.phase || "Unknown"));
    row.append(phase);
    row.append(el("td", "", `${pod.readyContainers}/${pod.totalContainers}`));
    row.append(el("td", pod.restarts > 0 ? "restarts warn" : "restarts", String(pod.restarts)));
    body.append(row);
  }
  table.append(body);
  return table;
}

async function loadPods(name) {
  const section = document.getElementById("pods");
  try {
    const response = await fetch(`/api/nodes/${encodeURIComponent(name)}/pods`);
    if (!response.ok) throw new Error(`GET pods returned ${response.status}`);
    const pods = await response.json();
    // A later click may have selected another node while this request was in
    // flight; drop the stale response instead of showing the wrong node's pods.
    if (name !== selectedNode) return;

    const title = el("h2", "section-title", `Pods on ${name} (${pods.length})`);
    section.replaceChildren(title, pods.length > 0 ? podTable(pods) : el("p", "empty", "No pods on this node."));
  } catch (err) {
    if (name !== selectedNode) return;
    section.replaceChildren(el("p", "error", `Could not load pods on ${name}: ${err.message}`));
  }
}

const REFRESH_MS = 5000;
let refreshing = false;

// refresh reloads the grid and, if a node is selected, its pods. It skips a
// tick if the previous refresh is still running.
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    const ok = await loadNodes();
    if (selectedNode) await loadPods(selectedNode);
    if (ok) document.getElementById("updated").textContent = `Updated ${new Date().toLocaleTimeString()}`;
  } finally {
    refreshing = false;
  }
}

// Poll only while the tab is visible, and catch up as soon as it is shown again.
setInterval(() => {
  if (!document.hidden) refresh();
}, REFRESH_MS);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) refresh();
});

refresh();
