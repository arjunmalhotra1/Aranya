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

function nodeCard(node) {
  const card = el("article", "node-card");

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

async function loadNodes() {
  const section = document.getElementById("nodes");
  try {
    const response = await fetch("/api/nodes");
    if (!response.ok) throw new Error(`GET /api/nodes returned ${response.status}`);
    const nodes = await response.json();

    const grid = el("div", "node-grid");
    for (const node of nodes) grid.append(nodeCard(node));
    if (nodes.length === 0) grid.append(el("p", "empty", "No nodes found."));
    section.replaceChildren(el("h2", "section-title", `Nodes (${nodes.length})`), grid);
  } catch (err) {
    section.replaceChildren(el("p", "error", `Could not load nodes: ${err.message}`));
  }
}

loadNodes();
