import { View, div } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { closeOverlay, exec, focused, props, terminals, theme } from "herdr";
import { binary } from "./binary.js";

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
    this.target = null;
    this.pending = false;
    this.data = null;
    this.error = "";
    this.handle = cx.focus_handle();
    this.follow(cx);
    this.handle.focus();
    cx.timer.every(3000, cx => this.refresh(cx));
  }

  follow(cx) {
    const focus = focused();
    const next = targetFrom(focus, focus?.shown ? terminals() : []);
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
    if (!this.target || this.pending) return;
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

  key(event, cx) {
    if (event.keystroke === "escape" || event.keystroke === "q") {
      cx.stop_propagation();
      closeOverlay(props().overlay);
    } else if (event.keystroke === "r") {
      cx.stop_propagation();
      this.refresh(cx);
    }
  }

  pr(pr, id, refWidth, marker = "") {
    const colors = theme();
    const row = h_flex().min_w(0).gap(10).h(28).flex_shrink_0().items_center();
    row.child(div().w(34).flex_shrink_0().text_color(colors.muted).child(marker));
    const finished = pr.state === "merged" || pr.state === "closed";
    const ci = finished ? "✓" : ({ pass: "●", fail: "✕", pending: "◌" }[pr.ci] || "·");
    const ciColor = finished ? colors.dim : ({ pass: colors.done, fail: colors.blocked, pending: colors.working }[pr.ci] || colors.dim);
    row.child(div().w(14).flex_shrink_0().text_color(ciColor).child(ci));
    const ref = div().w(refWidth).flex_shrink_0().truncate().child((pr.ref || "PR").split("/").at(-1)).text_color(colors.accent).underline();
    if (githubURL(pr.url)) ref.id(id).on_click((_event, cx) => cx.open_url(pr.url));
    row.child(ref);
    const stateColor = { merged: colors.ansi5, open: colors.done, closed: colors.blocked }[pr.state] || colors.muted;
    row.child(div().w(58).flex_shrink_0().text_color(stateColor).child(pr.state || "unknown"));
    row.child(div().flex_1().min_w(0).truncate().child(pr.title || ""));
    if (pr.stacked_on) row.child(div().flex_shrink_0().text_color(colors.dim).child("↳ " + pr.stacked_on.split("/").at(-1)));
    return row;
  }

  render(cx) {
    this.follow(cx);
    const colors = theme();
    const root = v_flex().size_full().min_h(0).p(20).gap(14).bg(colors.background)
      .text_color(colors.foreground).font_family("JetBrains Mono").text_size(13)
      .track_focus(this.handle).on_key_down((event, cx) => this.key(event, cx));
    const target = this.target;
    const address = target ? (target.machine ? target.machine + "/" : "") + target.terminal : "";
    const terminal = target ? terminals().find(t => t.address === address) : null;
    const name = terminal?.name || terminal?.program?.name || terminal?.title || "Terminal";
    const header = h_flex().items_center().gap(12).flex_shrink_0().text_color(colors.muted);
    header.child(div().child("cairn"));
    header.child(div().id("target").flex_1().tooltip(address)
      .child(target ? (target.machine ? target.machine + "/" : "") + name : ""));
    header.child(div().id("close").child("×").px(6)
      .on_mouse_down("left", (_event, cx) => cx.stop_propagation())
      .on_click(() => closeOverlay(props().overlay)));
    root.child(header);
    const body = v_flex().id("trail-scroll").flex_1().min_h(0).overflow_y_scrollbar()
      .on_mouse_down("left", (_event, cx) => cx.stop_propagation());
    const finish = () => root.child(body).child(div().flex_shrink_0().text_color(colors.dim).text_size(11).child("click PR to open · r refresh · q / esc close"));
    if (!target) { body.child(div().text_color(colors.muted).child(this.error || "Focus a terminal to see its trail.")); return finish(); }
    if (this.error) { body.child(div().text_color(colors.blocked).child(this.error)); return finish(); }
    if (!this.data) { body.child(div().text_color(colors.muted).child("Loading trail…")); return finish(); }
    const trail = this.data.trail;
    const prs = [...(trail?.steps.filter(step => step.kind === "pr") || []), ...this.data.prs];
    const refWidth = Math.min(200, Math.max(70, ...prs.map(pr => (pr.ref || "PR").split("/").at(-1).length * 8)));
    if (trail) {
      body.child(h_flex().gap(14).mb(10).flex_shrink_0()
        .child(div().font_semibold().child(trail.name))
        .child(div().text_color(colors.muted).child(trail.done + "/" + trail.total + " done")));
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
