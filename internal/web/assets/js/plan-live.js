/*
  Plan / Issues view live updates. Subscribes to the <live-region>'s 'event'
  custom event; on any incoming event, debounces and refetches the
  current /plan URL, then swaps the freshly-rendered <section> in
  place of the old one.

  The plan tree is structurally too varied to mutate inline (a new
  task may insert under any parent's subtree; a claim flips a status
  pill plus the rollup of every ancestor; a note adds a row inside a
  collapsible details group; a block adds a sub-row). A debounced
  refetch is a few orders of magnitude less code than the surgical
  alternative and the network cost is small at the dashboard's scale.

  After the swap we re-run the idempotent JS that the layout would
  normally trigger at DOMContentLoaded: actor/label color paint, the
  ambient progress bars, and the plan-collapse hydration that pulls
  per-task collapsed state out of localStorage.

  Self-guarded: if the page has no plan section, the module is a
  no-op. That way we can load it from the shared layout without
  per-page wiring.
*/

(function () {
  "use strict";

  var DEBOUNCE_MS = 750;

  document.addEventListener("DOMContentLoaded", init);

  function init() {
    var section = findPlanSection();
    if (!section) return;
    var live = document.querySelector("live-region");
    if (!live) return;

    var pending = null;
    live.addEventListener("event", function () {
      if (pending) clearTimeout(pending);
      pending = setTimeout(refresh, DEBOUNCE_MS);
    });
  }

  function findPlanSection() {
    // Both /plan and /issues render this section; the data attribute
    // is the one selector that matches either view.
    return document.querySelector("main .c-section[data-plan-view]");
  }

  async function refresh() {
    var section = findPlanSection();
    if (!section) return;
    try {
      var res = await fetch(window.location.href, {
        headers: { Accept: "text/html" },
        credentials: "same-origin",
      });
      if (!res.ok) return;
      var html = await res.text();
      var doc = new DOMParser().parseFromString(html, "text/html");
      var fresh = doc.querySelector("main .c-section[data-plan-view]");
      if (!fresh) return;
      // Capture the cursor row before the swap so the focus ring
      // persists through the live refetch — without this, the focused
      // row is yanked from the DOM and the ring vanishes.
      var activeShortID =
        window.JobsPlanKeyboard &&
        typeof window.JobsPlanKeyboard.getActive === "function"
          ? window.JobsPlanKeyboard.getActive()
          : "";
      section.replaceWith(fresh);
      // Also refresh the view header — the Issues meta line's counts
      // move as issues open and close, and it sits in the same chrome.
      var freshHeader = doc.querySelector("main .c-view-header");
      var oldHeader = document.querySelector("main .c-view-header");
      if (freshHeader && oldHeader) oldHeader.replaceWith(freshHeader);
      // Also refresh the filter strip — label frequencies and the
      // top-N membership shift as tasks open and close. Same scope
      // (the chrome above the tree).
      var freshBar = doc.querySelector("main section.c-filter-bar");
      var oldBar = document.querySelector("main section.c-filter-bar");
      if (freshBar && oldBar) oldBar.replaceWith(freshBar);

      // Re-run idempotent paints + collapse hydration on the new DOM.
      if (window.JobsColors) {
        if (typeof window.JobsColors.paint === "function") {
          window.JobsColors.paint(document);
        }
        if (typeof window.JobsColors.paintProgress === "function") {
          window.JobsColors.paintProgress(document);
        }
      }
      if (
        window.JobsPlanCollapse &&
        typeof window.JobsPlanCollapse.applyStored === "function"
      ) {
        window.JobsPlanCollapse.applyStored();
      }
      if (window.JobsPlanKeyboard) {
        if (
          activeShortID &&
          typeof window.JobsPlanKeyboard.setActive === "function"
        ) {
          window.JobsPlanKeyboard.setActive(activeShortID);
        } else if (
          typeof window.JobsPlanKeyboard.ensurePrimed === "function"
        ) {
          window.JobsPlanKeyboard.ensurePrimed();
        }
      }
    } catch (_) {
      // Network blip; the next event triggers another refresh.
    }
  }
})();
