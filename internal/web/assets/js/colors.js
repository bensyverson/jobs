/*
  Deterministic color helpers for actors and labels.

  Same input always yields the same HSL, so an "alice" avatar is the
  same color in every view, every session. Kept small and dependency-
  free; the production implementation will be a Go port in
  internal/web/render.

  Exposed globals: window.JobsColors
*/

(function () {
  "use strict";

  // FNV-1a, 32-bit. Small, stable, zero-dependency. We don't need
  // cryptographic strength — we need the same string to produce the
  // same integer in every runtime.
  function hash(str) {
    let h = 0x811c9dc5;
    for (let i = 0; i < str.length; i++) {
      h ^= str.charCodeAt(i);
      h = (h + ((h << 1) + (h << 4) + (h << 7) + (h << 8) + (h << 24))) >>> 0;
    }
    return h >>> 0;
  }

  function hueFor(name) {
    return hash(name + "u") % 360;
  }

  function satFor(name) {
    return (hash(name + "zzzzzzzz") % 50) + 50;
  }

  // Actor: S 65%, L 48% — saturated enough to read, dark enough for
  // comfortable white-text contrast across every hue. Earlier L 55%
  // was borderline on yellow/green/cyan; 48% normalizes that.
  function actorColor(name) {
    return "hsl(" + hueFor(name) + " " + satFor(name) + " 48%)";
  }

  // Label: S 40%, L 50% — desaturated vs. actors so labels read as
  // supporting metadata rather than identity. CSS renders these at
  // 15% fill + full-chroma 1px border (see .c-label-pill).
  function labelColor(name) {
    return "hsl(" + hueFor(name) + " 40% 50%)";
  }

  function initialOf(name) {
    if (!name) return "";
    const trimmed = String(name).trim();
    return trimmed.length ? trimmed[0].toUpperCase() : "";
  }

  // Walk the DOM and apply colors to elements with data-actor / data-label.
  // Idempotent — safe to call multiple times. Called automatically on
  // DOMContentLoaded; views can call it again after inserting rows.
  function paint(root) {
    root = root || document;

    root.querySelectorAll("[data-actor]").forEach(function (el) {
      const name = el.getAttribute("data-actor");
      if (!name) return;
      // Skip elements where the server (render.ActorColor) already
      // emitted an inline --actor-color, so SSR-painted avatars don't
      // visibly repaint to a different value on DOMContentLoaded.
      if (!el.style.getPropertyValue("--actor-color")) {
        el.style.setProperty("--actor-color", actorColor(name));
      }
      // Filled lettered avatars — fill the initial unless the element
      // already has its own content. Excludes the tiny dot form (too
      // small for a letter).
      const wantsLetter =
        (el.classList.contains("c-avatar") && !el.classList.contains("c-avatar-dot")) ||
        el.classList.contains("c-graph-node__bug");
      if (wantsLetter && el.textContent.trim() === "") {
        el.textContent = initialOf(name);
      }
    });

    root.querySelectorAll("[data-label]").forEach(function (el) {
      const name = el.getAttribute("data-label");
      if (!name) return;
      // Same SSR-precedence rule as actors: respect the server's
      // pre-painted --label-color. Eliminates the flash that used to
      // happen on /plan when colors.js fired on DOMContentLoaded.
      if (!el.style.getPropertyValue("--label-color")) {
        el.style.setProperty("--label-color", labelColor(name));
      }
    });
  }

  // Plan progress — count done leaves per parent subtree, inject the
  // ambient progress bar at the bottom edge of the parent row. Runs once
  // at DOMContentLoaded; a live dashboard will recompute on SSE events.
  function countLeaves(subtree) {
    let done = 0;
    let total = 0;
    const kids = Array.from(subtree.children);
    for (let i = 0; i < kids.length; i++) {
      const node = kids[i];
      if (!node.classList || !node.classList.contains("c-plan-row")) continue;
      const next = kids[i + 1];
      const isParent =
        next && next.classList && next.classList.contains("c-plan-subtree");
      if (isParent) {
        const r = countLeaves(next);
        done += r.done;
        total += r.total;
      } else {
        total += 1;
        if (node.classList.contains("c-plan-row--status-done")) done += 1;
      }
    }
    return { done: done, total: total };
  }

  function paintProgress(root) {
    root = root || document;
    const disclosures = root.querySelectorAll("button.c-plan-row__disclosure");
    disclosures.forEach(function (btn) {
      const row = btn.closest(".c-plan-row");
      if (!row) return;
      // An issue root is a pile, not a decomposition: there is no
      // denominator for it to be N of, and its throughput already
      // lives in the view's "N open · M closed in 7d" meta line. A
      // depth-0 row is one no .c-plan-subtree wraps — nesting is how
      // the template expresses depth. Branches inside an issue are
      // ordinary work and keep their bar.
      if (
        row.closest('[data-plan-view="issue"]') &&
        !row.closest(".c-plan-subtree")
      )
        return;
      // A done or canceled branch already communicates completion via
      // its status pill; a "100% of 100% done" bar would be redundant
      // chrome at best and misleading at worst on a canceled subtree.
      if (
        row.classList.contains("c-plan-row--status-done") ||
        row.classList.contains("c-plan-row--status-canceled")
      )
        return;
      // Skip if a progress bar is already present.
      if (row.querySelector(":scope > .c-plan-row__progress")) return;

      // Find this row's own subtree. The notes <details> sits between
      // the row and the subtree when present; skip it. If the row
      // doesn't actually own a subtree (a leaf with a description gets
      // a disclosure for collapse purposes but has no children), there
      // is no progress to paint — bail rather than falling back to the
      // parent's subtree, which would steal the parent's stats.
      let subtree = row.nextElementSibling;
      if (subtree && subtree.classList.contains("c-plan-notes-group")) {
        subtree = subtree.nextElementSibling;
      }
      if (!subtree || !subtree.classList.contains("c-plan-subtree")) return;

      const result = countLeaves(subtree);
      if (result.total === 0) return;
      // Hide the bar when the row has no momentum to show: nothing done
      // yet and no active work underneath (rollup already bubbles up
      // claimed descendants into c-plan-row--status-active). "0 of 15"
      // on a dormant todo is noise; once one task completes, or the
      // subtree picks up a claim, the bar returns.
      const isActive = row.classList.contains("c-plan-row--status-active");
      if (result.done === 0 && !isActive) return;
      const pct = (result.done / result.total) * 100;

      // Readable summary under the description, in the title column.
      const titleDiv = row.querySelector(":scope > .c-plan-row__title");
      if (titleDiv && !titleDiv.querySelector(".c-plan-row__progress-text")) {
        const text = document.createElement("div");
        text.className = "c-plan-row__progress-text";
        text.textContent = result.done + " of " + result.total + " tasks done";
        titleDiv.appendChild(text);
      }

      // Ambient progress bar at the top edge of the row.
      const bar = document.createElement("div");
      bar.className = "c-plan-row__progress";
      bar.style.setProperty("--progress", pct + "%");
      bar.setAttribute(
        "aria-label",
        result.done + " of " + result.total + " complete"
      );
      row.appendChild(bar);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () {
      paint(document);
      paintProgress(document);
    });
  } else {
    paint(document);
    paintProgress(document);
  }

  window.JobsColors = {
    hash: hash,
    hueFor: hueFor,
    actorColor: actorColor,
    labelColor: labelColor,
    initialOf: initialOf,
    paint: paint,
    paintProgress: paintProgress,
  };
})();
