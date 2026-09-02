/*
  Pure-data layer for the Plan-view scrubber.

  Mirrors the Go logic in internal/web/handlers/plan.go (buildPlanNodes,
  filterRootsByKind, filterRootsByShow, filterForestByLabels,
  pickStripLabels, planURL, DisplayStatus) and internal/web/render/relative_time.go (RelativeTime).
  Same shapes, same defaults — when a user scrubs through history the
  Plan section reads identically to its SSR cousin at the cursor's
  log position, with notes and rel-times pinned to that moment.

  Inputs are Frame objects produced by replay.mjs. Outputs are plain
  PlanNode trees the renderer walks; no DOM, no globals, no async.
*/

import { relativeTime as _relativeTime } from "./scrub-util.mjs";

// re-exported so existing imports continue to resolve from
// plan-scrub-build. Implementation lives in scrub-util.mjs for sharing
// with actors-scrub.
export const relativeTime = _relativeTime;

// displayStatus collapses a raw task status + has-blockers flag into
// the four visual categories the c-status-pill renders.
export function displayStatus(raw, hasOpenBlockers) {
  switch (raw) {
    case "done":
      return "done";
    case "canceled":
      return "canceled";
    case "claimed":
      return hasOpenBlockers ? "blocked" : "active";
    case "available":
      return hasOpenBlockers ? "blocked" : "todo";
    default:
      return raw;
  }
}

// buildForestFromFrame walks the flat tasks Map and assembles a tree
// keyed by parentShortId. Children are sorted by sortKey asc, with
// shortId as a stable tiebreak so two tasks with identical sort keys
// always render in the same position. Tasks whose parent is missing
// from the frame are surfaced as roots — better than dropping them
// silently when a partial replay lacks the parent.
export function buildForestFromFrame(frame) {
  const childrenOf = new Map(); // parentShortId|null -> task[]
  for (const task of frame.tasks.values()) {
    const parent = task.parentShortId ?? null;
    const realParent = parent !== null && frame.tasks.has(parent) ? parent : null;
    let bucket = childrenOf.get(realParent);
    if (!bucket) {
      bucket = [];
      childrenOf.set(realParent, bucket);
    }
    bucket.push(task);
  }
  const sortBucket = (arr) => {
    arr.sort((a, b) => {
      if (a.sortKey !== b.sortKey) return a.sortKey < b.sortKey ? -1 : 1;
      return a.shortId < b.shortId ? -1 : a.shortId > b.shortId ? 1 : 0;
    });
  };
  for (const arr of childrenOf.values()) sortBucket(arr);

  const buildSubtree = (task) => ({
    task,
    children: (childrenOf.get(task.shortId) ?? []).map(buildSubtree),
  });
  return (childrenOf.get(null) ?? []).map(buildSubtree);
}

// isArchivedSubtree is true iff the node and every descendant carry
// a closed status (done/canceled). Used by filterRootsByShow to pick
// the Active vs. Archived top-level partition.
export function isArchivedSubtree(node) {
  const s = node.task.status;
  if (s !== "done" && s !== "canceled") return false;
  for (const c of node.children) {
    if (!isArchivedSubtree(c)) return false;
  }
  return true;
}

// filterRootsByKind keeps the roots belonging to one view: "issue"
// keeps the issue trees, anything else keeps their complement. The
// frame carries `kind` on roots only, and a root without one is a
// task tree — same default as the Go side, so no root falls through
// the gap between /plan and /issues.
export function filterRootsByKind(roots, kind) {
  const wantIssue = kind === "issue";
  return roots.filter((r) => (r.task.kind === "issue") === wantIssue);
}

export function filterRootsByShow(roots, show) {
  if (show === "all") return roots;
  if (show === "archived") return roots.filter((r) => isArchivedSubtree(r));
  return roots.filter((r) => !isArchivedSubtree(r));
}

// filterForestByLabels keeps a node when it (or any descendant) carries
// a selected label. Mirrors plan.go's filterForestByLabels OR semantic.
// task.labels is a Set on the frame's tasks; a missing labels field
// is treated as empty.
export function filterForestByLabels(roots, selected) {
  if (!selected || selected.length === 0) return roots;
  const wanted = new Set(selected);
  const walk = (nodes) => {
    const out = [];
    for (const n of nodes) {
      const kept = walk(n.children);
      const labels = n.task.labels ?? new Set();
      let match = false;
      for (const l of labels) {
        if (wanted.has(l)) {
          match = true;
          break;
        }
      }
      if (match || kept.length > 0) {
        out.push({ task: n.task, children: kept });
      }
    }
    return out;
  };
  return walk(roots);
}

// labelFreqsByView counts label occurrences across the forest, scoped
// to the tasks that match the current ?show= mode. Mirrors plan.go.
export function labelFreqsByView(roots, show) {
  const include = (task) => {
    const s = task.status;
    switch (show) {
      case "archived":
        return s === "done" || s === "canceled";
      case "all":
        return true;
      default:
        return s !== "done" && s !== "canceled";
    }
  };
  const out = {};
  const walk = (nodes) => {
    for (const n of nodes) {
      if (include(n.task)) {
        for (const l of n.task.labels ?? new Set()) {
          out[l] = (out[l] ?? 0) + 1;
        }
      }
      walk(n.children);
    }
  };
  walk(roots);
  return out;
}

// pickStripLabels: top-N most frequent labels in view + selected
// labels not already in the top-N. Frequency desc, name asc tiebreak;
// extras appended in name order so the strip stays stable.
export function pickStripLabels(roots, selected, show, n) {
  const freqs = labelFreqsByView(roots, show);
  const all = Object.entries(freqs).map(([name, count]) => ({ name, count }));
  all.sort((a, b) => {
    if (a.count !== b.count) return b.count - a.count;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
  const top = [];
  const inTop = new Set();
  for (const e of all) {
    if (top.length >= n) break;
    top.push(e.name);
    inTop.add(e.name);
  }
  const extras = [];
  for (const s of selected) {
    if (!inTop.has(s)) {
      extras.push(s);
      inTop.add(s);
    }
  }
  extras.sort();
  return [...top, ...extras];
}

// buildPlanNodes turns the (post-filter) tree into PlanNode records the
// renderer consumes. nowSec is the cursor event's created_at — pass it
// through so RelTime is frozen at the historical moment, not wall-clock.
//
// Rollup rules mirror plan.go:
//   - displayStatus uses the task's own status + has-open-blockers.
//   - An open ancestor whose subtree contains an active descendant is
//     promoted to "active" so the tree visibly glows where work is in
//     progress.
//   - A node is "collapsed" when its whole subtree is closed.
export function buildPlanNodes(roots, frame, nowSec, opts = {}) {
  const selected = opts.selected ?? [];
  const show = opts.show ?? "active";
  const base = opts.base ?? "/plan";
  const blockedBySetFor = (shortId) => frame.blocks.get(shortId) ?? new Set();
  const claimsByShortId = frame.claims;
  const titleOf = (shortId) => frame.tasks.get(shortId)?.title ?? "";
  const labelURL = (name) => planURL(addLabel(selected, name), show, base);

  const isOpen = (s) => s !== "done" && s !== "canceled";

  const walk = (nodes, depth) => {
    const out = [];
    for (const n of nodes) {
      const children = walk(n.children, depth + 1);
      const blockers = [...blockedBySetFor(n.task.shortId)];
      let display = displayStatus(n.task.status, blockers.length > 0);

      // Subtree-has-open: the task itself counts (when display is open),
      // and any descendant whose own subtree carries open work.
      let subtreeHasOpen = isOpen(display);
      for (const c of children) {
        if (!c.collapsed || isOpen(c.displayStatus)) {
          subtreeHasOpen = true;
          break;
        }
      }

      // Active-rollup: still-open ancestor → active when any direct
      // child rolls up to active. plan.go only walks direct children;
      // mirror that.
      if (isOpen(display)) {
        for (const c of children) {
          if (c.displayStatus === "active") {
            display = "active";
            break;
          }
        }
      }

      const hasChildren = children.length > 0;
      const hasDesc =
        typeof n.task.description === "string" && n.task.description.trim() !== "";
      const claim = claimsByShortId.get(n.task.shortId);
      const actor = claim?.claimedBy ?? "";

      out.push({
        shortID: n.task.shortId,
        url: "/tasks/" + n.task.shortId,
        title: n.task.title,
        description: n.task.description ?? "",
        displayStatus: display,
        actor,
        labels: [...(n.task.labels ?? new Set())]
          .sort()
          .map((name) => ({ name, url: labelURL(name) })),
        relTime: relativeTime(nowSec, n.task.updatedAt ?? nowSec),
        isoTime: new Date((n.task.updatedAt ?? nowSec) * 1000).toISOString(),
        blockedBy: blockers
          .slice()
          .sort()
          .map((shortID) => ({
            shortID,
            url: "#task-" + shortID,
            title: titleOf(shortID),
          })),
        notes: (n.task.notes ?? []).map((nt) => ({
          actor: nt.actor,
          relTime: relativeTime(nowSec, nt.ts),
          isoTime: new Date(nt.ts * 1000).toISOString(),
          text: nt.text,
          displayStatus: display,
        })),
        children,
        depth,
        hasChildren,
        collapsible: hasChildren || hasDesc,
        collapsed: !subtreeHasOpen,
      });
    }
    return out;
  };
  return walk(roots, 0);
}

// --- URL helpers (mirror plan.go's planURL / toggleLabel / addLabel) ---

export function toggleLabel(selected, name) {
  const out = [];
  let found = false;
  for (const s of selected) {
    if (s === name) {
      found = true;
      continue;
    }
    out.push(s);
  }
  if (!found) out.push(name);
  out.sort();
  return out;
}

export function addLabel(selected, name) {
  if (selected.includes(name)) return [...selected];
  const out = [name, ...selected];
  out.sort();
  return out;
}

// planURL composes <base>?label=…&show=… exactly like plan.go's
// planURL: labels are individually URL-encoded then joined with raw
// commas; the default show ("active") is omitted; keys are emitted
// alphabetically. base is "/plan", "/issues", or a scoped
// "/issues/<root>"; it defaults to the Plan view.
export function planURL(selected, show, base = "/plan") {
  const parts = [];
  if (selected && selected.length > 0) {
    const labelVal = selected.map((s) => encodeURIComponent(s)).join(",");
    parts.push(`label=${labelVal}`);
  }
  if (show && show !== "active") {
    parts.push(`show=${encodeURIComponent(show)}`);
  }
  if (parts.length === 0) return base;
  // Keys are already in alphabetical order: label < show.
  return base + "?" + parts.join("&");
}
