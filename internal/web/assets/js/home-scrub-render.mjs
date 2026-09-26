/*
  Home-view HTML emitter for the scrubber.

  Pure string functions mirroring internal/web/templates/html/pages/
  home.html.tmpl for the four panels. The driver parses each fragment
  with DOMParser and swaps the four [data-home-*] sections in place,
  preserving the page chrome (header, footer, peek-sheet, scrubber pill,
  dependency-flow graph) and the chart panel, which <chart-panel>
  fetches from the server at the cursor (reporting decision 12).

  The graph is intentionally NOT rendered here — it's refetched server-
  side via POST /home/graph (debounced) so the JS bundle doesn't need
  to carry the subway layout pipeline.
*/

import { escapeHTML } from "./scrub-util.mjs";

// --- Panels (four data-home-* sections in c-grid-cols-4) ---

function emptyPanelRow(text) {
  return `<p class="c-empty">${escapeHTML(text)}</p>`;
}

export function renderActiveClaims(panel) {
  let body;
  if (panel.Rows && panel.Rows.length > 0) {
    body = panel.Rows.map(
      (r) =>
        '<div class="c-panel-row" style="--row-cols: var(--avatar-sm-size) auto 1fr auto" ' +
        `data-claimed-at="${r.ClaimedAtUnix}">` +
        `<a href="${escapeHTML(r.ActorURL)}" class="c-avatar c-avatar-sm" data-actor="${escapeHTML(r.Actor)}" aria-label="Actor ${escapeHTML(r.Actor)}"></a>` +
        `<span class="c-id-pill">${escapeHTML(r.TaskShortID)}</span>` +
        `<span class="c-panel-row__title">${escapeHTML(r.TaskTitle)}</span>` +
        `<span class="c-panel-row__meta" data-claim-idle>${escapeHTML(r.DurationText)}</span>` +
        `<a href="${escapeHTML(r.TaskURL)}" data-peek class="c-row-link" aria-label="Open task ${escapeHTML(r.TaskShortID)}"></a>` +
        "</div>",
    ).join("");
  } else {
    body = emptyPanelRow("No claims in flight.");
  }
  return (
    '<section class="c-panel" aria-labelledby="p-claims" data-home-claims>' +
    '<div class="c-panel__header">' +
    '<h2 id="p-claims" class="c-panel__title">Active claims</h2>' +
    `<span class="c-panel__meta">${panel.Count} in flight</span>` +
    "</div>" +
    `<div class="c-panel__list">${body}</div>` +
    "</section>"
  );
}

export function renderRecentCompletions(panel) {
  let body;
  if (panel.Rows && panel.Rows.length > 0) {
    body = panel.Rows.map(
      (r) =>
        '<div class="c-panel-row" style="--row-cols: var(--avatar-sm-size) auto 1fr auto">' +
        `<a href="${escapeHTML(r.ActorURL)}" class="c-avatar c-avatar-sm" data-actor="${escapeHTML(r.Actor)}" aria-label="Actor ${escapeHTML(r.Actor)}"></a>` +
        `<span class="c-id-pill">${escapeHTML(r.TaskShortID)}</span>` +
        `<span class="c-panel-row__title">${escapeHTML(r.TaskTitle)}</span>` +
        `<span class="c-panel-row__meta">${escapeHTML(r.AgeText)}</span>` +
        `<a href="${escapeHTML(r.TaskURL)}" data-peek class="c-row-link" aria-label="Open task ${escapeHTML(r.TaskShortID)}"></a>` +
        "</div>",
    ).join("");
  } else {
    body = emptyPanelRow("Nothing completed in the last 10 minutes.");
  }
  return (
    '<section class="c-panel" aria-labelledby="p-recent" data-home-recent>' +
    '<div class="c-panel__header">' +
    '<h2 id="p-recent" class="c-panel__title">Recent completions</h2>' +
    `<span class="c-panel__meta">last ${panel.Count}</span>` +
    "</div>" +
    `<div class="c-panel__list">${body}</div>` +
    "</section>"
  );
}

export function renderUpcoming(panel) {
  let body;
  if (panel.Rows && panel.Rows.length > 0) {
    body = panel.Rows.map(
      (r) =>
        `<div class="c-panel-row" style="--row-cols: auto 1fr auto" data-created-at="${r.CreatedAtUnix}">` +
        `<span class="c-id-pill">${escapeHTML(r.TaskShortID)}</span>` +
        `<span class="c-panel-row__title">${escapeHTML(r.TaskTitle)}</span>` +
        `<span class="c-panel-row__meta">${escapeHTML(r.AgeText)}</span>` +
        `<a href="${escapeHTML(r.TaskURL)}" data-peek class="c-row-link" aria-label="Open task ${escapeHTML(r.TaskShortID)}"></a>` +
        "</div>",
    ).join("");
  } else {
    body = emptyPanelRow("Nothing ready to claim.");
  }
  return (
    '<section class="c-panel" aria-labelledby="p-upcoming" data-home-upcoming>' +
    '<div class="c-panel__header">' +
    '<h2 id="p-upcoming" class="c-panel__title">Available</h2>' +
    `<span class="c-panel__meta">${panel.Count} ready</span>` +
    "</div>" +
    `<div class="c-panel__list">${body}</div>` +
    "</section>"
  );
}

export function renderBlocked(panel) {
  let body;
  if (panel.Rows && panel.Rows.length > 0) {
    body = panel.Rows.map((r) => {
      const blockerPills = r.Blockers.map(
        (b, i) =>
          (i > 0 ? ", " : "") +
          `<a href="${escapeHTML(b.URL)}" class="c-id-pill">${escapeHTML(b.ShortID)}</a>`,
      ).join("");
      return (
        '<div class="c-panel-row c-panel-row--stacked" style="--row-cols: auto 1fr">' +
        `<span class="c-id-pill">${escapeHTML(r.TaskShortID)}</span>` +
        '<div class="stack stack-gap-xs">' +
        `<span class="c-panel-row__title">${escapeHTML(r.TaskTitle)}</span>` +
        `<span class="c-panel-row__meta">waiting on ${blockerPills}</span>` +
        "</div>" +
        `<a href="${escapeHTML(r.TaskURL)}" data-peek class="c-row-link" aria-label="Open task ${escapeHTML(r.TaskShortID)}"></a>` +
        "</div>"
      );
    }).join("");
  } else {
    body = emptyPanelRow("Nothing blocked.");
  }
  return (
    '<section class="c-panel" aria-labelledby="p-blocked" data-home-blocked>' +
    '<div class="c-panel__header">' +
    '<h2 id="p-blocked" class="c-panel__title">Blocked</h2>' +
    `<span class="c-panel__meta">${panel.Count} waiting</span>` +
    "</div>" +
    `<div class="c-panel__list">${body}</div>` +
    "</section>"
  );
}
