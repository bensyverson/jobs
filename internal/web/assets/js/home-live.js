/*
  Home view live updates. Two responsibilities:

    1. Tick the "idle" timer in every active-claims row once a second.
       Rows carry data-claimed-at="<unix seconds>"; the display cell
       is [data-claim-idle]. The ticker is pure wall-clock math — the
       server doesn't have to push a frame for each second to pass.

    2. On any incoming live event, debounce and refetch the current
       /home URL, then swap the four panels and the task map in
       place. Mirrors plan-live.js: rows come and go too freely to
       mutate inline, and the refetch is a few orders of magnitude
       less code than the surgical alternative. The chart panel
       refreshes itself (chart-panel.mjs).

  Self-guarded: if the page has no data-home-claims panel, both
  features are no-ops. Safe to load from the shared layout.
*/

(function () {
  "use strict";

  var DEBOUNCE_MS = 750;
  var TICK_MS = 1000;

  document.addEventListener("DOMContentLoaded", init);

  function init() {
    var panel = document.querySelector("[data-home-claims]");
    if (!panel) return;

    startIdleTicker();

    var live = document.querySelector("live-region");
    if (!live) return;

    var pending = null;
    live.addEventListener("event", function () {
      if (pending) clearTimeout(pending);
      pending = setTimeout(refresh, DEBOUNCE_MS);
    });
  }

  // Every TICK_MS, walk every claim row and rewrite the idle cell
  // from its data-claimed-at attribute. Cheap: a handful of rows and
  // a string format per tick.
  function startIdleTicker() {
    tickOnce();
    setInterval(tickOnce, TICK_MS);
  }

  function tickOnce() {
    var nowSec = Math.floor(Date.now() / 1000);
    var rows = document.querySelectorAll("[data-home-claims] [data-claimed-at]");
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var claimedAt = parseInt(row.getAttribute("data-claimed-at"), 10);
      if (!Number.isFinite(claimedAt)) continue;
      var cell = row.querySelector("[data-claim-idle]");
      if (!cell) continue;
      var age = nowSec - claimedAt;
      if (age < 0) age = 0;
      cell.textContent = formatClaimDuration(age);
    }
  }

  // formatClaimDuration mirrors render.ClaimDuration (Go):
  //   <1m → "Ns"; <1h → "Nm Ms"; <1d → "Hh" or "Hh Mm"; else "Dd" or "Dd Hh".
  function formatClaimDuration(seconds) {
    if (seconds < 60) return seconds + "s";
    var m = Math.floor(seconds / 60);
    var s = seconds - m * 60;
    if (m < 60) return m + "m " + s + "s";
    var h = Math.floor(m / 60);
    var mm = m - h * 60;
    if (h < 24) return mm === 0 ? h + "h" : h + "h " + mm + "m";
    var d = Math.floor(h / 24);
    var hh = h - d * 24;
    return hh === 0 ? d + "d" : d + "d " + hh + "h";
  }

  async function refresh() {
    var oldClaims = document.querySelector("[data-home-claims]");
    var oldRecent = document.querySelector("[data-home-recent]");
    var oldBlocked = document.querySelector("[data-home-blocked]");
    var oldGraph = document.querySelector("[data-home-graph] .c-mini-graph");
    if (!oldClaims) return;
    try {
      var res = await fetch(window.location.href, {
        headers: { Accept: "text/html" },
        credentials: "same-origin",
      });
      if (!res.ok) return;
      var html = await res.text();
      var doc = new DOMParser().parseFromString(html, "text/html");
      var freshClaims = doc.querySelector("[data-home-claims]");
      var freshRecent = doc.querySelector("[data-home-recent]");
      var freshBlocked = doc.querySelector("[data-home-blocked]");
      var freshGraph = doc.querySelector("[data-home-graph] .c-mini-graph");
      if (freshClaims) oldClaims.replaceWith(freshClaims);
      if (freshRecent && oldRecent) oldRecent.replaceWith(freshRecent);
      if (freshBlocked && oldBlocked) oldBlocked.replaceWith(freshBlocked);
      if (freshGraph && oldGraph) oldGraph.replaceWith(freshGraph);

      // Idempotent re-paint so new rows get their actor colors.
      if (window.JobsColors && typeof window.JobsColors.paint === "function") {
        window.JobsColors.paint(document);
      }
      // Tick once immediately so the swapped-in rows don't show the
      // server-pinned text for up to a full second.
      tickOnce();
    } catch (_) {
      // Network blip; next event triggers another refresh.
    }
  }
})();
