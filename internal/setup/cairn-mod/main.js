import { View, div } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { closeOverlay, exec, focused, layout, props, run, shared, setShared, terminals, theme } from "herdr";
import { binary } from "./binary.js";
import { prepareDock, hideDock, dockVisible } from "./dock.js";

// A remote address is qualified in the app, but cairn resolves it on that machine.
export function targetFrom(focus, allTerminals) {
  if (!focus) return null;
  const address = focus.shown || focus.terminal;
  if (!address) return null;
  const slash = address.indexOf("/");
  const machine = slash < 0 ? null : address.slice(0, slash);
  const terminal = slash < 0 ? address : address.slice(slash + 1);
  if (!/^t[1-9][0-9bcdfghjkmnpqrstvwxyz]*$/.test(terminal) || machine === "") return null;
  const cwd = focus.shown
    ? (allTerminals.find(t => t.address === address)?.cwd || null)
    : (focus.cwd || null);
  return { machine, terminal, cwd };
}

export function githubURL(url) {
  return typeof url === "string" && /^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/pull\/[1-9][0-9]*$/.test(url);
}

export default class Cairn extends View {
  init(_props, cx) {
    this.toolbarMode = props()?.cairnMode === "toolbar";
    if (this.toolbarMode) {
      // A new probe invalidates persisted acknowledgements after restart/reload.
      setShared("cairn.visibility-probe", String(Date.now()) + Math.random());
      return;
    }
    this.floatOwner = String(Date.now()) + Math.random();
    const saved = shared("cairn.target");
    this.target = saved ? targetFrom({ terminal: (saved.machine ? saved.machine + "/" : "") + saved.terminal, cwd: saved.cwd }, []) : null;
    this.handoff = saved?.version;
    this.docked = props()?.cairnMode === "dock";
    this.switching = false;
    this.routed = false;
    this.routing = false;
    this.cancelled = false;
    this.pending = false;
    this.data = null;
    this.error = "";
    this.modeError = "";
    this.routeFailed = false;
    this.handle = cx.focus_handle();
    if (!this.docked && props()?.overlay && shared("cairn.mode") !== "float") {
      // A keybinding opens a short-lived router; it never steals focus or fetches.
      const focus = focused();
      this.target = targetFrom(focus, focus?.shown ? terminals() : []) || this.target;
      this.dock(cx, true);
      return;
    }
    this.startView(cx);
  }

  startView(cx) {
    this.follow(cx);
    this.refresh(cx);
    if (!this.docked) this.handle.focus();
    cx.timer.every(3000, cx => this.refresh(cx));
  }

  follow(cx) {
    if (this.docked && shared("cairn.mode") === "dock") this.switching = false;
    if (this.routed || this.routing || this.switching) return;
    const focus = focused();
    let next = targetFrom(focus, focus?.shown ? terminals() : []);
    const saved = shared("cairn.target");
    if (saved?.version !== this.handoff) {
      this.handoff = saved?.version;
      if (!focus?.terminal && !focus?.shown && saved) {
        next = targetFrom({ terminal: (saved.machine ? saved.machine + "/" : "") + saved.terminal, cwd: saved.cwd }, []);
      }
    }
    if (!next && (focus?.shown || focus?.terminal)) {
      this.target = null;
      this.data = null;
      this.error = "Cannot identify this terminal safely.";
      return;
    }
    // Clicking this mod leaves terminal focus empty; retain the whole last target.
    if (!next || JSON.stringify(next) === JSON.stringify(this.target)) return;
    this.target = next;
    this.data = null;
    this.error = "";
    this.refresh(cx);
  }

  refresh(cx) {
    if (!this.target || this.pending || this.routed || this.routing || this.switching) return;
    const target = this.target;
    this.pending = true;
    cx.spawn(async cx => {
      try {
        const args = ["show", "--pane", "hc-" + target.terminal, "--json"];
        if (target.cwd) args.push("--dir", target.cwd);
        const result = await exec(target.machine ? "cairn" : binary, args, { on: target.machine });
        if (result.code !== 0) throw new Error(result.stderr.trim() || "cairn exited " + result.code);
        const data = JSON.parse(result.stdout);
        if (!data || !Array.isArray(data.prs) || !(data.trail === null || (data.trail && Array.isArray(data.trail.steps) && Array.isArray(data.trail.notes)))) {
          throw new Error("Unexpected cairn JSON; update cairn on the target machine.");
        }
        if (target === this.target) { this.data = data; this.error = ""; }
      } catch (error) {
        if (target === this.target) { this.data = null; this.error = String(error); }
      } finally {
        this.pending = false;
        if (target !== this.target) this.refresh(cx);
        cx.notify();
      }
    });
  }

  saveTarget() {
    if (this.target) setShared("cairn.target", { ...this.target, version: (shared("cairn.target")?.version || 0) + 1 });
  }

  async close(_cx) {
    this.cancelled = true;
    this.clearVisible();
  }

  clearVisible() {
    this.closed = true;
    if (this.floatOwner && shared("cairn.float-visible")?.owner === this.floatOwner) setShared("cairn.float-visible", null);
  }

  acknowledgeFloat() {
    if (this.docked || this.closed || !props()?.overlay) return;
    const probe = shared("cairn.visibility-probe");
    const ack = shared("cairn.float-visible");
    if (probe && (ack?.probe !== probe || ack?.owner !== this.floatOwner)) {
      setShared("cairn.float-visible", { probe, owner: this.floatOwner });
    }
  }

  toolbar() {
    const colors = theme();
    const probe = shared("cairn.visibility-probe");
    const shown = dockVisible(layout()) || Boolean(probe && shared("cairn.float-visible")?.probe === probe);
    return h_flex().size_full().pr(10).items_center().bg(colors.chrome).border_b(1).border_color(colors.border)
      .child(h_flex().id("cairn-toggle").h(26).px(8).flex_none().items_center().rounded(6).text_size(12)
        .bg(shown ? colors.selected : colors.chrome).text_color(shown ? colors.foreground : colors.muted)
        .hover(style => style.bg(colors.hover))
        .on_mouse_down("left", (_event, cx) => cx.stop_propagation())
        .on_click(() => run("mod.open", { name: "cairn" })).child("Cairn"));
  }

  closePanel() {
    this.clearVisible();
    if (this.switching) this.cancelled = true;
    this.saveTarget();
    if (this.docked) hideDock();
    else if (props()?.overlay) closeOverlay(props().overlay);
  }

  switchMode(cx) {
    if (this.switching || this.routed) return;
    this.saveTarget();
    if (this.routeFailed) {
      setShared("cairn.mode", "float");
      this.routeFailed = false;
      this.modeError = "";
      cx.notify();
      return;
    }
    if (this.docked) {
      this.switching = true;
      hideDock();
      setShared("cairn.mode", "float");
      run("mod.open", { name: "cairn" });
    } else {
      this.dock(cx);
    }
  }

  dock(cx, toggle = false) {
    this.switching = true;
    this.routing = toggle;
    this.cancelled = false;
    cx.spawn(async cx => {
      try {
        const commands = await prepareDock(toggle);
        if (this.cancelled) return;
        this.saveTarget();
        setShared("cairn.mode", "dock");
        this.clearVisible();
        this.routed = true;
        for (const command of commands) run(command.name, command.args);
        if (props()?.overlay) closeOverlay(props().overlay);
      } catch (error) {
        if (this.cancelled) return;
        this.switching = false;
        this.modeError = String(error);
        this.routeFailed = toggle;
        this.routing = false;
        if (toggle) this.startView(cx);
        cx.notify();
      }
    });
  }

  key(event, cx) {
    if (!this.handle.is_focused()) return;
    if (event.keystroke === "escape" || event.keystroke === "q") {
      cx.stop_propagation();
      this.closePanel();
    } else if (event.keystroke === "r") {
      cx.stop_propagation();
      this.refresh(cx);
    }
  }

  pr(pr, id, refWidth, marker = "") {
    const colors = theme();
    const row = h_flex().min_w(0).gap(this.docked ? 6 : 10).h(28).flex_shrink_0().items_center();
    row.child(div().w(34).flex_shrink_0().text_color(colors.muted).child(marker));
    const finished = pr.state === "merged" || pr.state === "closed";
    const ci = finished ? "✓" : ({ pass: "●", fail: "✕", pending: "◌" }[pr.ci] || "·");
    const ciColor = finished ? colors.dim : ({ pass: colors.done, fail: colors.blocked, pending: colors.working }[pr.ci] || colors.dim);
    row.child(div().w(14).flex_shrink_0().text_color(ciColor).child(ci));
    const ref = (this.docked ? div().flex_1().min_w(0) : div().w(refWidth).flex_shrink_0()).truncate().child((pr.ref || "PR").split("/").at(-1)).text_color(colors.accent).underline();
    if (githubURL(pr.url)) ref.id(id).on_click((_event, cx) => cx.open_url(pr.url));
    row.child(ref);
    const stateColor = { merged: colors.ansi5, open: colors.done, closed: colors.blocked }[pr.state] || colors.muted;
    row.child(div().w(58).flex_shrink_0().text_color(stateColor).child(pr.state || "unknown"));
    if (this.docked) {
      const block = v_flex().min_w(0).flex_shrink_0().mb(6).child(row);
      if (pr.title) block.child(div().text_color(colors.muted).child(pr.title));
      if (pr.stacked_on) block.child(div().text_color(colors.dim).child("↳ " + pr.stacked_on.split("/").at(-1)));
      return block;
    }
    row.child(div().flex_1().min_w(0).truncate().child(pr.title || ""));
    if (pr.stacked_on) row.child(div().flex_shrink_0().text_color(colors.dim).child("↳ " + pr.stacked_on.split("/").at(-1)));
    return row;
  }

  render(cx) {
    if (this.toolbarMode) return this.toolbar();
    if (this.routed || this.routing) return div();
    this.acknowledgeFloat();
    this.follow(cx);
    const colors = theme();
    const root = v_flex().size_full().min_h(0).p(this.docked ? 12 : 20).gap(14).bg(colors.background)
      .text_color(colors.foreground).font_family("JetBrains Mono").text_size(13)
      .id("cairn-panel").track_focus(this.handle)
      .on_mouse_down("left", () => this.handle.focus()).on_key_down((event, cx) => this.key(event, cx));
    if (this.docked) root.rounded(10).border(1)
      .border_color(this.handle.is_focused() ? colors.accent : colors.border).overflow_hidden();
    const target = this.target;
    const address = target ? (target.machine ? target.machine + "/" : "") + target.terminal : "";
    const terminal = target ? terminals().find(t => t.address === address) : null;
    const name = terminal?.name || terminal?.program?.name || terminal?.title || "Terminal";
    const header = h_flex().min_w(0).items_center().gap(12).flex_shrink_0().text_color(colors.muted);
    header.child(div().flex_shrink_0().child("cairn"));
    header.child(div().id("target").flex_1().min_w(0).truncate().tooltip(address)
      .child(target ? (target.machine ? target.machine + "/" : "") + name : ""));
    header.child(div().id("mode").flex_shrink_0().child(this.docked || this.routeFailed ? "Float" : "Dock").text_color(colors.accent)
      .on_mouse_down("left", (_event, cx) => cx.stop_propagation())
      .on_click((_event, cx) => this.switchMode(cx)));
    header.child(div().id("close").flex_shrink_0().child("×").px(6)
      .on_mouse_down("left", (_event, cx) => cx.stop_propagation())
      .on_click(() => this.closePanel()));
    root.child(header);
    if (this.modeError) root.child(div().text_color(colors.blocked).child(this.modeError));
    const body = v_flex().id("trail-scroll").flex_1().min_h(0).overflow_y_scrollbar()
      .on_mouse_down("left", (_event, cx) => { this.handle.focus(); cx.stop_propagation(); });
    const finish = () => {
      root.child(body).child(div().flex_shrink_0().text_color(colors.dim).text_size(11).child(this.docked ? "PR ↗ · r refresh · q/esc close" : "click PR to open · r refresh · q / esc close"));
      return this.docked ? div().size_full().pl(8).child(root) : root;
    };
    if (!target) { body.child(div().text_color(colors.muted).child(this.error || "Focus a terminal to see its trail.")); return finish(); }
    if (this.error) { body.child(div().text_color(colors.blocked).child(this.error)); return finish(); }
    if (!this.data) { body.child(div().text_color(colors.muted).child("Loading trail…")); return finish(); }
    const trail = this.data.trail;
    const prs = [...(trail?.steps.filter(step => step.kind === "pr") || []), ...this.data.prs];
    const refWidth = Math.min(200, Math.max(70, ...prs.map(pr => (pr.ref || "PR").split("/").at(-1).length * 8)));
    if (trail) {
      body.child(h_flex().min_w(0).gap(14).mb(10).flex_shrink_0()
        .child(div().flex_1().min_w(0).truncate().font_semibold().child(trail.name))
        .child(div().flex_shrink_0().text_color(colors.muted).child(trail.done + "/" + trail.total + " done")));
      if (this.data.via === "dir") body.child(div().text_color(colors.muted).child("Found by folder"));
      const next = trail.steps.find(step => !step.done);
      for (const step of trail.steps) {
        const marker = (step === next ? "▶ " : "  ") + step.n;
        if (step.kind === "pr") body.child(this.pr(step, "step-" + step.n, refWidth, marker));
        else body.child(h_flex().h(28).gap(10).items_center().flex_shrink_0()
          .child(div().w(34).text_color(colors.muted).child(marker))
          .child(div().w(14).text_color(step.done ? colors.done : colors.dim).child(step.done ? "✓" : "○"))
          .child(div().flex_1().min_w(0).truncate().child(step.text || "")));
      }
      if (trail.notes.length) body.child(div().mt(16).mb(4).text_color(colors.muted).child("Notes"));
      for (const note of trail.notes) body.child(div().flex_shrink_0().text_color(colors.muted).child("· " + note));
    } else {
      body.child(div().text_color(colors.muted).child("No trail for this terminal."));
    }
    if (this.data.prs.length) body.child(div().mt(16).mb(4).text_color(colors.muted).child("Touched PRs"));
    this.data.prs.forEach((pr, i) => body.child(this.pr(pr, "touched-" + i, refWidth)));
    return finish();
  }
}
