/*
  Log-view chip strips: the overflow expander and the live-arrival
  chip.

  The server renders the whole in-window strip and hides everything
  past the cap (`data-chip-overflow hidden`), followed by a "+N more"
  link to the same URL with ?chips=all. With JS off that link is an
  ordinary navigation and the server renders the strip expanded; with
  JS we intercept it and unhide what is already in the DOM, so
  expanding costs no request and loses no scroll position.

  Hiding rather than omitting is the deliberate trade: it spends a few
  hundred bytes of markup to keep the expander a class toggle instead
  of a fetch-and-swap. The range bound is what keeps that number
  small — the cap is about visual noise, not payload.

  The second job is the live one: an event from an actor with no chip
  (they just started work) adds their chip at the head of the strip,
  since the strip reads most-recent-first. Chip strips are static SSR
  — nothing re-fetches them between navigations — so the arrival has
  to be applied here. log-live.mjs calls in on every event it renders.

  Self-guarded: every entry point tolerates a page with no chip strip,
  so the module can ride the shared layout without per-page wiring.
*/

import { escapeHTML } from "./scrub-util.mjs";

// Query keys a chip href carries forward, mirroring the server's
// logChipCtx.url in internal/web/handlers/log_chips.go. `before`,
// `limit` and `at` are deliberately absent: clicking a filter starts
// a fresh page of results.
const CARRIED_KEYS = ["actor", "task", "label", "type", "since", "range", "chips"];

// expandStrip reveals a group's overflow chips and retires its "+N
// more" chip. Returns how many chips it revealed.
export function expandStrip(group) {
  if (!group) return 0;
  const hidden = Array.from(group.querySelectorAll("[data-chip-overflow]"));
  for (const chip of hidden) {
    chip.hidden = false;
    chip.removeAttribute("hidden");
    chip.removeAttribute("data-chip-overflow");
  }
  const more = group.querySelector("[data-chip-more]");
  if (more) more.remove();
  return hidden.length;
}

// logChipHref builds /log?… with one filter key set (or cleared when
// value is empty), preserving the rest of the view. Mirror of
// logChipCtx.url; keep the two in step.
export function logChipHref(search, key, value) {
  const from = new URLSearchParams(search || "");
  const out = new URLSearchParams();
  for (const k of CARRIED_KEYS) {
    const v = from.get(k);
    if (v) out.set(k, v);
  }
  if (value) {
    out.set(key, value);
  } else {
    out.delete(key);
  }
  // URLSearchParams keeps insertion order; the server encodes sorted,
  // and a chip href only has to resolve to the same page.
  out.sort();
  const qs = out.toString();
  return qs ? "/log?" + qs : "/log";
}

// actorChipMarkup mirrors the actor chip in log.html.tmpl.
export function actorChipMarkup(actor, href) {
  return (
    `<a href="${escapeHTML(href)}" class="c-filter-chip" data-actor-chip="${escapeHTML(actor)}">` +
    `<span class="c-avatar c-avatar-dot" data-actor="${escapeHTML(actor)}"></span>${escapeHTML(actor)}</a>`
  );
}

// findChip returns the chip for one actor, comparing attribute values
// rather than building a selector — actor names are free text and can
// carry quotes.
export function findChip(group, actor) {
  if (!group) return null;
  for (const chip of group.querySelectorAll("[data-actor-chip]")) {
    if (chip.getAttribute("data-actor-chip") === actor) return chip;
  }
  return null;
}

// decrementMore keeps the "+N more" chip honest after a chip is
// promoted out of the overflow, and removes it at zero.
export function decrementMore(group) {
  const more = group && group.querySelector("[data-chip-more]");
  if (!more) return;
  const left = Number(more.getAttribute("data-chip-more")) - 1;
  if (left <= 0) {
    more.remove();
    return;
  }
  more.setAttribute("data-chip-more", String(left));
  more.textContent = `+${left} more`;
}

// ensureActorChip makes sure an actor is visible in the strip: it
// promotes them out of the overflow, or inserts a new chip at the
// head. An actor already showing is left where they are — the strip
// reshuffling under the pointer costs more than the ordering gains.
export function ensureActorChip(root, actor, search) {
  if (!root || !actor) return null;
  const group = root.querySelector('[data-chip-group="actor"]');
  if (!group) return null;

  const existing = findChip(group, actor);
  if (existing) {
    if (!existing.hasAttribute("data-chip-overflow")) return existing;
    existing.hidden = false;
    existing.removeAttribute("hidden");
    existing.removeAttribute("data-chip-overflow");
    decrementMore(group);
    moveToHead(group, existing);
    return existing;
  }

  const anchor = group.querySelector("a.c-filter-chip");
  if (!anchor) return null;
  anchor.insertAdjacentHTML("afterend", actorChipMarkup(actor, logChipHref(search, "actor", actor)));
  return anchor.nextElementSibling;
}

// moveToHead re-seats a chip directly after the leading "any" chip.
function moveToHead(group, chip) {
  const lead = group.querySelector("a.c-filter-chip");
  if (lead && lead !== chip) lead.after(chip);
}

function init() {
  // Delegated on the document: log-scrub.mjs replaces the whole
  // filter bar when the history cursor moves, which would strip a
  // listener bound to the bar itself.
  document.addEventListener("click", (ev) => {
    const target = ev.target;
    if (!target || typeof target.closest !== "function") return;
    const more = target.closest("[data-chip-more]");
    if (!more) return;
    const group = more.closest("[data-chip-group]");
    if (!group) return;
    ev.preventDefault();
    expandStrip(group);
  });
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
