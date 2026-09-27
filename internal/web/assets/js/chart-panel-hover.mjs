/*
  The chart panel's crosshair (decision 9 of
  project/2026-09-27-chart-panel-revision.md), as DOM: one vertical line
  through both charts at the same moment, the burn-up's dots and end
  labels moved to that sample, the legend counting up to it, and a
  tooltip naming the slice.

  The server ships every element this touches (partials/
  chart_panel.html.tmpl): the two crosshair lines, one empty tooltip per
  chart, a polite live region. This file only positions and fills them
  — SVG geometry attributes (x1, cx, y) and text, plus data-*
  attributes the stylesheet reads to show and dock things. It never
  writes a style attribute, so the web rule holds at runtime too.

  State lives on the host as the attributes a server render can
  declare: data-hover-at (the moment, Unix ms) and data-hover-chart
  (which chart the pointer is over). A panel rendered with them — the
  preview catalog's hover states — is upgraded to the full hover by
  sync(). Leaving restores every value the hover changed exactly as
  the server wrote it.
*/

import { hoverModel, momentAt, nearestSample, stepBucket, tooltipText } from "./chart-hover.mjs";

// PLOT_SELECTOR matches the two plots a pointer or the keyboard can
// hover; KIND maps each to the chart it is (handlers.ChartKind).
const PLOT_SELECTOR = ".c-burnup, svg.c-activity";
const kindOf = (plot) => (plot.classList.contains("c-burnup") ? "burnup" : "activity");

const HOVER_AT = "data-hover-at";
const HOVER_CHART = "data-hover-chart";
const KEYS = new Set(["ArrowLeft", "ArrowRight", "Home", "End"]);

export class PanelHover {
  constructor(host) {
    this.host = host;
    this.index = -1;
    this.saved = null;
    this.data = undefined;
    this.touch = null;
    this.byKeyboard = false;

    this.onMove = (e) => this.pointer(e);
    this.onDown = (e) => {
      if (e.pointerType === "touch" && this.plotOf(e.target)) {
        this.touch = e.pointerId;
        this.pointer(e);
      }
    };
    this.onUp = (e) => {
      if (e.pointerId === this.touch) {
        this.touch = null;
        this.clear();
      }
    };
    this.onLeave = (e) => {
      if (e.pointerType !== "touch") this.clear();
    };
    this.onKey = (e) => this.key(e);
    this.onBlur = (e) => {
      if (this.byKeyboard && this.plotOf(e.target)) this.clear();
    };
  }

  bind() {
    const h = this.host;
    h.addEventListener("pointermove", this.onMove);
    h.addEventListener("pointerdown", this.onDown);
    h.addEventListener("pointerup", this.onUp);
    h.addEventListener("pointercancel", this.onUp);
    h.addEventListener("pointerleave", this.onLeave);
    h.addEventListener("keydown", this.onKey);
    h.addEventListener("focusout", this.onBlur);
    this.sync();
  }

  unbind() {
    const h = this.host;
    h.removeEventListener("pointermove", this.onMove);
    h.removeEventListener("pointerdown", this.onDown);
    h.removeEventListener("pointerup", this.onUp);
    h.removeEventListener("pointercancel", this.onUp);
    h.removeEventListener("pointerleave", this.onLeave);
    h.removeEventListener("keydown", this.onKey);
    h.removeEventListener("focusout", this.onBlur);
  }

  // sync adopts freshly swapped-in contents: it forgets the old island
  // and saved values, makes the plots keyboard-reachable, and redraws a
  // declared hover (data-hover-at) over the new drawing.
  sync() {
    this.data = undefined;
    this.saved = null;
    this.index = -1;
    for (const plot of this.host.querySelectorAll(PLOT_SELECTOR)) {
      plot.setAttribute("tabindex", "0");
    }
    const at = Number(this.host.getAttribute(HOVER_AT));
    const d = this.island();
    if (!at || !d) return;
    const i = nearestSample(d.trace, at);
    if (i >= 0) this.show(i, this.host.getAttribute(HOVER_CHART) || "burnup", false);
  }

  // island parses the panel's data island once per swap; null when the
  // panel has none (empty and error states) or it will not parse.
  island() {
    if (this.data !== undefined) return this.data;
    const el = this.host.querySelector("script.c-chart-panel__data");
    let d = null;
    try {
      d = el ? JSON.parse(el.textContent) : null;
    } catch (_) {
      d = null;
    }
    this.data = d && Array.isArray(d.trace) && d.trace.length ? d : null;
    return this.data;
  }

  plotOf(target) {
    const plot = target && target.closest ? target.closest(PLOT_SELECTOR) : null;
    return plot && this.host.contains(plot) ? plot : null;
  }

  sliding() {
    return this.host.hasAttribute("data-chart-anim");
  }

  pointer(e) {
    if (this.sliding()) return;
    if (e.pointerType === "touch" && e.pointerId !== this.touch) return;
    const plot = this.plotOf(e.target);
    const d = this.island();
    if (!plot || !d) {
      if (e.pointerType !== "touch") this.clear();
      return;
    }
    const r = plot.getBoundingClientRect();
    if (!r.width) return;
    const i = nearestSample(d.trace, momentAt(d, (e.clientX - r.left) / r.width));
    this.byKeyboard = false;
    this.show(i, kindOf(plot), false);
  }

  key(e) {
    const plot = this.plotOf(e.target);
    if (!plot || e.target !== plot || this.sliding()) return;
    if (e.key === "Escape") {
      if (this.index >= 0) this.clear();
      return;
    }
    const d = this.island();
    if (!d || !KEYS.has(e.key)) return;
    e.preventDefault();
    this.byKeyboard = true;
    this.show(stepBucket(d, this.index, e.key), kindOf(plot), true);
  }

  // show draws the crosshair at trace sample i over chart, and says it
  // in the live region when announce (keyboard only: a pointer sweep
  // would flood it).
  show(i, chart, announce) {
    const d = this.island();
    if (!d || i < 0) return;
    const h = this.host;
    const m = hoverModel(d, i);
    const tip = tooltipText(m, d);
    this.save();
    this.index = i;
    h.setAttribute(HOVER_AT, String(m.t));
    h.setAttribute(HOVER_CHART, chart);

    this.attrs(".c-chart-cross--burnup", { x1: m.x, x2: m.x });
    this.attrs(".c-chart-cross--activity", { x1: m.activityX, x2: m.activityX });
    const { scopeDot, doneDot, createdY, doneY } = m.markers;
    this.attrs(".c-burnup__dot--scope", { cx: scopeDot.x, cy: scopeDot.y });
    this.attrs(".c-burnup__dot--done", { cx: doneDot.x, cy: doneDot.y });
    this.endBlock("created", createdY, m.created, m.createdTotal);
    this.endBlock("done", doneY, m.done, m.doneTotal);
    for (const [kind, text] of Object.entries(m.legend)) {
      const el = h.querySelector(`.c-activity-legend__count[data-kind="${kind}"]`);
      if (el) el.textContent = text;
    }

    // The tooltip docks on the side away from the moment, so it never
    // sits under the pointer or the finger.
    const side = (m.t - d.since) / Math.max(1, d.until - d.since) > 0.5 ? "start" : "end";
    for (const el of h.querySelectorAll(".c-chart-tip")) {
      el.setAttribute("data-side", side);
      this.text(el, ".c-chart-tip__when", tip.when);
      this.text(el, ".c-chart-tip__events", tip.events);
      this.text(el, ".c-chart-tip__state", tip.state);
    }
    if (announce) this.text(h, ".c-chart-panel__live", tip.spoken);
  }

  // clear restores the server's values and drops the hover.
  clear() {
    if (this.index < 0 && !this.host.hasAttribute(HOVER_AT)) return;
    this.restore();
    this.index = -1;
    this.byKeyboard = false;
    this.host.removeAttribute(HOVER_AT);
    this.host.removeAttribute(HOVER_CHART);
  }

  endBlock(which, y, value, total) {
    const g = this.host.querySelector(`.c-burnup-ends__block[data-end="${which}"]`);
    if (!g) return;
    for (const t of g.querySelectorAll("text")) t.setAttribute("y", y);
    this.text(g, ".c-burnup-ends__value", value);
    this.text(g, ".c-burnup-ends__total", total);
  }

  attrs(selector, values) {
    const el = this.host.querySelector(selector);
    if (!el) return;
    for (const [k, v] of Object.entries(values)) el.setAttribute(k, v);
  }

  text(root, selector, value) {
    const el = root.querySelector(selector);
    if (el) el.textContent = value;
  }

  // save records, once per hover, every attribute and text the hover
  // rewrites, as the server rendered it.
  save() {
    if (this.saved) return;
    const h = this.host;
    const saved = [];
    const keep = (el, name) => {
      if (el) saved.push([el, name, name === "#text" ? el.textContent : el.getAttribute(name)]);
    };
    for (const sel of [".c-chart-cross--burnup", ".c-chart-cross--activity"]) {
      const el = h.querySelector(sel);
      keep(el, "x1");
      keep(el, "x2");
    }
    for (const sel of [".c-burnup__dot--scope", ".c-burnup__dot--done"]) {
      const el = h.querySelector(sel);
      keep(el, "cx");
      keep(el, "cy");
    }
    for (const t of h.querySelectorAll(".c-burnup-ends__block text")) {
      keep(t, "y");
      keep(t, "#text");
    }
    for (const el of h.querySelectorAll(".c-activity-legend__count")) keep(el, "#text");
    for (const el of h.querySelectorAll(".c-chart-tip")) {
      keep(el, "data-side");
      for (const p of el.children) keep(p, "#text");
    }
    this.saved = saved;
  }

  restore() {
    if (!this.saved) return;
    for (const [el, name, value] of this.saved) {
      if (name === "#text") el.textContent = value;
      else if (value === null) el.removeAttribute(name);
      else el.setAttribute(name, value);
    }
    this.saved = null;
  }
}
