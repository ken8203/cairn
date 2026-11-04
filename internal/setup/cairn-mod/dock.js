import { appHome, layout, readTextFile, run, shared, writeTextFile } from "herdr";

function cairnNode(tree, mode) {
  if (!tree || typeof tree !== "object") return null;
  if (tree.content?.mod === "cairn" && tree.content.props?.cairnMode === mode && tree.id) return tree;
  for (const child of tree.children || []) {
    const found = cairnNode(child, mode);
    if (found) return found;
  }
  return null;
}

export function dockNode(tree) { return cairnNode(tree, "dock"); }

function activeChild(node) {
  const children = (node.children || []).filter(child => !child.hidden);
  return children.find(child => child.id === node.active) || children[0];
}

function pathTo(node, id) {
  if (node.id === id) return [node];
  for (const child of node.children || []) {
    const path = pathTo(child, id);
    if (path) return [node, ...path];
  }
  return null;
}

// A hidden ancestor or an inactive stack child hides all its descendants.
export function dockVisible(node) {
  if (!node || node.hidden) return false;
  if (node.content?.mod === "cairn" && node.content.props?.cairnMode === "dock") return true;
  if (node.type === "stack") return dockVisible(activeChild(node));
  return (node.children || []).some(dockVisible);
}

function unique(base, used) {
  let name = base;
  for (let n = 2; used.has(name); n++) name = base + "-" + n;
  return name;
}

// Wrap only the active tab's block, keeping the rest of the user's tree intact.
export function dockPlan(tree) {
  const existing = dockNode(tree);
  if (existing && cairnNode(tree, "toolbar")) return { id: existing.id };
  const copy = JSON.parse(JSON.stringify(tree));
  const ids = new Set();
  let tab = null;
  const bars = [];
  function visit(node, parent, visible = true) {
    if (!node || typeof node !== "object") return;
    if (node.id) {
      if (ids.has(node.id)) throw new Error("Cannot dock: layout contains duplicate IDs.");
      ids.add(node.id);
    }
    visible = visible && !node.hidden;
    if (visible && node.type === "block" && node.content?.mod === "tabs") bars.push(node);
    if (visible && node.type === "block" && node.content?.tab === "active") {
      if (tab) throw new Error("Cannot dock: layout has multiple active-tab blocks.");
      tab = { node, parent };
    }
    const active = activeChild(node);
    for (const child of node.children || []) {
      const selected = node.type !== "stack" || child === active;
      visit(child, node, visible && selected);
    }
  }
  visit(copy, null);
  if (!existing && !tab) throw new Error("Cannot dock this layout: no active-tab block. Use Float or switch layouts.");
  if (!cairnNode(copy, "toolbar")) {
    if (bars.length !== 1) throw new Error("Cannot add Cairn toolbar: expected one visible native tabs block. Use Float or switch layouts.");
    const bar = bars[0];
    const content = bar.content;
    delete bar.content;
    bar.type = "row";
    bar.resizable = false;
    const tabsID = unique("cairn-tabs", ids);
    ids.add(tabsID);
    const toolbarID = unique("cairn-toolbar", ids);
    ids.add(toolbarID);
    bar.children = [
      { id: tabsID, type: "block", size: { weight: 1 }, content },
      { id: toolbarID, type: "block", size: { px: 84 }, content: { mod: "cairn", props: { cairnMode: "toolbar" } } },
    ];
  }
  let id = existing?.id;
  if (!existing) {
    id = unique("cairn-dock", ids);
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
  } else {
    // Upgrading an old dock must open it in the new copy, even if it was hidden.
    const path = pathTo(copy, id);
    path.forEach((node, i) => {
      delete node.hidden;
      if (node.type === "stack" && path[i + 1]) node.active = path[i + 1].id;
    });
  }
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
  if (!plan.layout) {
    const commands = [];
    // Reveal ancestors before the dock: toggling a child under a hidden parent does nothing.
    const path = pathTo(current, plan.id);
    path.slice(0, -1).forEach((node, i) => {
      if (node.hidden) commands.push({ name: "layout.show", args: { id: node.id } });
      if (node.type === "stack" && activeChild(node) !== path[i + 1]) {
        commands.push({ name: "layout.select", args: { stack: node.id, child: path[i + 1].id } });
      }
    });
    commands.push({ name: toggle && commands.length === 0 ? "layout.toggle" : "layout.show", args: { id: plan.id } });
    return commands;
  }
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
  return [{ name: "layout.switch", args: { name } }];
}

export function hideDock() {
  const node = dockNode(layout());
  if (node) run("layout.hide", { id: node.id });
}
