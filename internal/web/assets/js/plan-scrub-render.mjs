/*
  Plan-view HTML emitter for the scrubber.

  Pure string functions — the driver parses the result with DOMParser
  and swaps the new <section> in place of the live one. Output mirrors
  internal/web/templates/html/pages/plan.html.tmpl exactly so CSS,
  data attributes, and progressive-enhancement hooks (data-peek,
  data-plan-task, data-collapsed) keep working uniformly across SSR
  and history mode.

  Inputs come from plan-scrub-build.mjs (PlanNode trees + filter-bar
  shapes); outputs are HTML strings. No DOM, no globals. The driver
  owns the DOM swap and the post-swap re-hydration (color paint,
  collapse state).
*/

// escapeHTML re-exported from scrub-util so plan-scrub-render's public
// API stays put while actors-scrub-render shares the same helper.
export { escapeHTML } from "./scrub-util.mjs";
import { escapeHTML } from "./scrub-util.mjs";
import { renderProseHTML } from "./prose.mjs";

// labelColorFor reads window.JobsColors.labelColor when available so
// scrub-rendered chips ship with a pre-painted --label-color. Without
// this, replacing the section on cursor change would re-introduce the
// flash that this commit fixed for SSR. Falls back to currentColor in
// non-browser test environments where window isn't present.
function labelColorFor(name) {
  if (typeof window !== "undefined" && window.JobsColors && window.JobsColors.labelColor) {
    return window.JobsColors.labelColor(name);
  }
  return "currentColor";
}

// --- status pill ---

const STATUS_LABELS = {
  done: "Done",
  blocked: "Blocked",
  active: "Active",
  canceled: "Canceled",
  todo: "Todo",
};

function statusIcon(status) {
  switch (status) {
    case "done":
      return `<svg class="c-status-pill__icon" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M2 5l2 2 4-4" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
    case "blocked":
      return `<svg class="c-status-pill__icon" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="2.5" width="1.5" height="5"/><rect x="5.5" y="2.5" width="1.5" height="5"/></svg>`;
    case "active":
      return `<svg class="c-status-pill__icon" viewBox="0 0 10 10" fill="currentColor" aria-hidden="true"><circle cx="5" cy="5" r="3"/></svg>`;
    case "canceled":
      return `<svg class="c-status-pill__icon" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M2.5 2.5l5 5M7.5 2.5l-5 5" stroke-linecap="round"/></svg>`;
    default:
      return `<svg class="c-status-pill__icon" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><circle cx="5" cy="5" r="3"/></svg>`;
  }
}

function statusLabel(status) {
  return STATUS_LABELS[status] ?? "Todo";
}

// --- filter bar ---

// renderFilterBar emits the Plan tabs and label-strip chrome above the
// section. Inputs mirror plan.go's PlanShowTab / PlanLabelChip shapes
// (camelCased: { showTabs, labels, allURL, allActive }), plus the two
// per-view fields: filterLabel names the nav for screen readers
// ("Plan filter" / "Issues filter"), sectionLabel names the filter bar
// ("Plan filters" / "Issues filters"), and meta is the optional period
// stat beside the tabs, carried through verbatim.
export function renderFilterBar({ showTabs, labels, allURL, allActive, filterLabel, sectionLabel, meta }) {
  const tabs = (showTabs ?? [])
    .map(
      (t) =>
        `<a href="${escapeHTML(t.url)}" class="c-tab${t.active ? " c-tab--active" : ""}">${escapeHTML(t.label)}</a>`,
    )
    .join("");
  const allPill = `<a href="${escapeHTML(allURL)}" class="c-label-pill c-label-pill--all${allActive ? " c-label-pill--active" : ""}">any</a>`;
  const labelPills = (labels ?? [])
    .map(
      (l) =>
        `<a href="${escapeHTML(l.url)}" class="c-label-pill${l.active ? " c-label-pill--active" : ""}" data-label="${escapeHTML(l.name)}" style="--label-color: ${labelColorFor(l.name)}">${escapeHTML(l.name)}</a>`,
    )
    .join("");
  const navName = filterLabel ?? "Plan filter";
  const barName = sectionLabel ?? "Plan";
  const metaSpan = meta
    ? `<span class="c-view-meta" data-view-meta>${escapeHTML(meta)}</span>`
    : "";
  return [
    `<div class="row row-gap-md c-view-header">`,
    `<nav class="c-tabs" aria-label="${escapeHTML(navName)}">${tabs}</nav>`,
    metaSpan,
    `</div>`,
    `<section class="c-filter-bar" aria-label="${escapeHTML(barName)} filters">`,
    `<div class="c-filter-bar__group" role="group" aria-label="Labels">`,
    `<span class="c-filter-bar__label">Labels</span>`,
    allPill,
    labelPills,
    `</div>`,
    `</section>`,
  ].join("");
}

// --- plan section ---

function renderTitleLine(node) {
  const idPill = `<a href="${escapeHTML(node.url)}" data-peek class="c-id-pill">${escapeHTML(node.shortID)}</a>`;
  const labels = (node.labels ?? [])
    .map(
      (l) =>
        `<a href="${escapeHTML(l.url)}" class="c-label-pill" data-label="${escapeHTML(l.name)}" style="--label-color: ${labelColorFor(l.name)}">${escapeHTML(l.name)}</a>`,
    )
    .join("");
  const headingClass =
    node.depth === 0 ? " t-heading-lg" : node.depth === 1 ? " t-heading-md" : "";
  return `<div class="c-plan-row__title-line"><span class="c-plan-row__title-text${headingClass}">${escapeHTML(node.title)}</span>${idPill}${labels}</div>`;
}

function renderBlockedBy(blockedBy) {
  if (!blockedBy || blockedBy.length === 0) return "";
  const parts = blockedBy.map((b) => {
    const titleAttr = b.title ? ` title="${escapeHTML(b.title)}"` : "";
    return `<a href="${escapeHTML(b.url)}" data-peek class="c-id-pill"${titleAttr}>${escapeHTML(b.shortID)}</a>`;
  });
  return `<span class="c-plan-row__blocked-by">Blocked by ${parts.join(", ")}</span>`;
}

function renderNotes(node) {
  const notes = node.notes ?? [];
  if (notes.length === 0) return "";
  const noteRows = notes
    .map(
      (n) =>
        `<div class="c-plan-note c-plan-note--status-${escapeHTML(n.displayStatus)}">` +
        `<span class="c-avatar c-avatar-sm" data-actor="${escapeHTML(n.actor)}"></span>` +
        `<div class="c-plan-note__meta"><span class="c-plan-note__actor">${escapeHTML(n.actor)}</span></div>` +
        `<span class="c-plan-note__time"><time datetime="${escapeHTML(n.isoTime)}">${escapeHTML(n.relTime)}</time></span>` +
        `<pre class="c-plan-note__text">${escapeHTML(n.text)}</pre>` +
        `</div>`,
    )
    .join("");
  const word = notes.length > 1 ? "notes" : "note";
  return (
    `<details class="c-plan-notes-group" data-plan-task="${escapeHTML(node.shortID)}">` +
    `<summary class="c-plan-notes-group__summary"><span class="t-dim">${notes.length} ${word}</span></summary>` +
    noteRows +
    `</details>`
  );
}

function renderNode(node, links) {
  const collapsedAttr = node.collapsible
    ? ` data-collapsed="${node.collapsed ? "true" : "false"}"`
    : "";
  const collapsedClass = node.collapsed ? " c-plan-row--collapsed" : "";

  const disclosure = node.collapsible
    ? `<button class="c-plan-row__disclosure" aria-expanded="${node.collapsed ? "false" : "true"}" aria-label="${node.collapsed ? "Expand" : "Collapse"}">` +
      `<svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M3 5l3 3 3-3" stroke-linecap="round" stroke-linejoin="round"/></svg>` +
      `</button>`
    : `<span></span>`;

  const desc =
    node.description && node.description.trim() !== ""
      ? `<div class="c-plan-row__desc c-prose">${renderProseHTML(node.description, links)}</div>`
      : "";

  const avatar = node.actor
    ? `<span class="c-avatar c-avatar-sm" data-actor="${escapeHTML(node.actor)}"></span>`
    : "";

  const timeCell = node.relTime
    ? `<time datetime="${escapeHTML(node.isoTime)}">${escapeHTML(node.relTime)}</time>`
    : "—";

  const row =
    `<div class="c-plan-row c-plan-row--status-${escapeHTML(node.displayStatus)}${collapsedClass}" id="task-${escapeHTML(node.shortID)}" data-plan-task="${escapeHTML(node.shortID)}"${collapsedAttr}>` +
    disclosure +
    `<div class="c-plan-row__title">` +
    renderTitleLine(node) +
    desc +
    `</div>` +
    `<span class="c-plan-row__avatar-slot">${avatar}</span>` +
    `<span class="c-status-pill c-status-pill--${escapeHTML(node.displayStatus)}">` +
    statusIcon(node.displayStatus) +
    statusLabel(node.displayStatus) +
    `</span>` +
    `<span class="c-plan-row__timestamp">${timeCell}</span>` +
    renderBlockedBy(node.blockedBy) +
    `</div>`;

  const notes = renderNotes(node);
  const subtree = node.hasChildren
    ? `<div class="c-plan-subtree">${(node.children ?? []).map((c) => renderNode(c, links)).join("")}</div>`
    : "";
  return row + notes + subtree;
}

// renderPlanSection emits the Plan <section> the page currently shows.
// links is the prose resolver (see proseLinksFromFrame): the short ids a
// row description may link. Omit it and descriptions render without links.
// The wrapping <main> stays put — the driver swaps just this section.
// view carries the per-view attributes the SSR template emits, so the
// scrubbed section stays findable by the same selectors: { label,
// kind, base, emptyText }. Defaults are the Plan view's.
export function renderPlanSection(planNodes, view = {}, links = null) {
  const label = view.label ?? "Plan";
  const kind = view.kind ?? "task";
  const base = view.base ?? "/plan";
  const emptyText = view.emptyText ?? "No active tasks.";
  const inner =
    planNodes && planNodes.length > 0
      ? `<div class="stack stack-gap-xs">${planNodes.map((n) => renderNode(n, links)).join("")}</div>`
      : `<div class="c-plan-empty"><span class="t-muted">${escapeHTML(emptyText)}</span></div>`;
  return `<section class="c-section" aria-label="${escapeHTML(label)}" data-plan-view="${escapeHTML(kind)}" data-plan-base="${escapeHTML(base)}">${inner}</section>`;
}
