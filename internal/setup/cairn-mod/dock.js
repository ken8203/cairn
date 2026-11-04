import { appHome, layout, readTextFile, run, shared, writeTextFile } from "herdr";

export function dockNode(tree) {
  if (!tree || typeof tree !== "object") return null;
  if (tree.content?.mod === "cairn" && tree.content.props?.cairnMode === "dock" && tree.id) return tree;
  for (const child of tree.children || []) {
    const found = dockNode(child);
    if (found) return found;
  }
  return null;
}

function unique(base, used) {
  let name = base;
  for (let n = 2; used.has(name); n++) name = base + "-" + n;
  return name;
}

// Wrap only the active tab's block, keeping the rest of the user's tree intact.
export function dockPlan(tree) {
  const existing = dockNode(tree);
  if (existing) return { id: existing.id };
  const copy = JSON.parse(JSON.stringify(tree));
  const ids = new Set();
  let tab = null;
  function visit(node, parent, visible = true) {
    if (!node || typeof node !== "object") return;
    if (node.id) {
      if (ids.has(node.id)) throw new Error("Cannot dock: layout contains duplicate IDs.");
      ids.add(node.id);
    }
    visible = visible && !node.hidden;
    if (visible && node.type === "block" && node.content?.tab === "active") {
      if (tab) throw new Error("Cannot dock: layout has multiple active-tab blocks.");
      tab = { node, parent };
    }
    const visibleChildren = (node.children || []).filter(child => !child.hidden);
    const active = visibleChildren.find(child => child.id === node.active) || visibleChildren[0];
    for (const child of node.children || []) {
      const selected = node.type !== "stack" || child === active;
      visit(child, node, visible && selected);
    }
  }
  visit(copy, null);
  if (!tab) throw new Error("Cannot dock this layout: no active-tab block. Use Float or switch layouts.");
  const id = unique("cairn-dock", ids);
  ids.add(id);
  const rowID = unique("cairn-work", ids);
  const dock = { id, type: "block", size: { px: 400 }, min: 280,
    content: { mod: "cairn", props: { cairnMode: "dock" } } };
  const row = { id: rowID, type: "row", children: [tab.node, dock] };
  // The wrapper occupies the tab block's old slot; the terminal takes remaining space.
  for (const key of ["size", "min", "max", "hidden"]) {
    if (key in tab.node) { row[key] = tab.node[key]; delete tab.node[key]; }
  }
  tab.node.size = { weight: 1 };
  if (tab.parent) {
    tab.parent.children[tab.parent.children.indexOf(tab.node)] = row;
    if (tab.parent.type === "stack") tab.parent.active = rowID;
  } else throw new Error("Cannot dock this layout: the active tab must be inside a layout container.");
  return { id, layout: copy };
}

// Preparation never queues a structural command. The caller switches only on success.
export async function prepareDock(toggle = false) {
  const source = shared("layout");
  if (typeof source !== "string" || !/^[A-Za-z0-9_-]+$/.test(source)) {
    throw new Error("Cannot dock: invalid current layout name.");
  }
  const current = layout();
  const snapshot = JSON.stringify(current);
  const unchanged = () => {
    if (shared("layout") !== source || JSON.stringify(layout()) !== snapshot) {
      throw new Error("Layout changed while preparing Dock. Try again in the current layout.");
    }
  };
  const plan = dockPlan(current);
  if (!plan.layout) return { name: toggle ? "layout.toggle" : "layout.show", args: { id: plan.id } };
  const base = source + "-cairn";
  let name = base;
  const directory = appHome() + "/layouts/";
  // Avoid omitted layouts and leftover click state, then create exclusively.
  for (let n = 2;
    await readTextFile(directory + name + ".json") !== null ||
    await readTextFile(directory + ".state/" + name + ".json") !== null;
    n++) {
    unchanged();
    name = base + "-" + n;
  }
  unchanged();
  await writeTextFile(directory + name + ".json", JSON.stringify(plan.layout), null);
  unchanged();
  return { name: "layout.switch", args: { name } };
}

export function hideDock() {
  const node = dockNode(layout());
  if (node) run("layout.hide", { id: node.id });
}
