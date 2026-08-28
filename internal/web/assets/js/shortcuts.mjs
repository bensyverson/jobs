// Global keyboard shortcuts for the dashboard.
//
// Bindings:
//   1 … 5         jump to Home / Plan / Issues / Actors / Log
//   `               cycle to the next primary tab (wraps)
//   ~ (shift-`)     cycle to the previous primary tab (wraps)
//
// Pure helpers (pathFromKey, cyclePath, shouldIgnoreShortcut) are
// exported and unit-tested in internal/web/jstest/shortcuts.test.mjs.
// bindShortcuts wires the live document and is exercised against a
// fake document/navigator in the same test file.
//
// Per-page shortcuts (e.g. plan-keyboard.js's j/k row navigation)
// stay scoped to their own modules; this delegator only owns the
// global tab strip. Both layers share the same input-context guard,
// so typing in the search box or any input never triggers either.

// Header order, and the number keys follow it: Issues sits after Plan
// (project/2026-08-28-issues-ux.md, decision 7), so it takes key 3 and
// Actors / Log shift to 4 / 5. The header's title="… (press N)" hints
// are generated from the same order.
export const TAB_PATHS = ["/", "/plan", "/issues", "/actors", "/log"];

const KEY_TO_PATH = {
  "1": "/",
  "2": "/plan",
  "3": "/issues",
  "4": "/actors",
  "5": "/log",
};

export function pathFromKey(key) {
  if (key == null) return null;
  return Object.prototype.hasOwnProperty.call(KEY_TO_PATH, key)
    ? KEY_TO_PATH[key]
    : null;
}

export function cyclePath(currentPath, dir) {
  const idx = TAB_PATHS.indexOf(currentPath);
  if (idx < 0) {
    // Outside the tab strip (e.g. /tasks/abc): seed at the end the
    // user is moving toward, so the first press lands on a tab.
    return dir > 0 ? TAB_PATHS[0] : TAB_PATHS[TAB_PATHS.length - 1];
  }
  const n = TAB_PATHS.length;
  const next = (idx + (dir > 0 ? 1 : -1) + n) % n;
  return TAB_PATHS[next];
}

export function shouldIgnoreShortcut(target) {
  if (!target) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (target.isContentEditable) return true;
  return false;
}

export function bindShortcuts(opts) {
  const doc = opts.document;
  const navigate = opts.navigate;
  const getCurrentPath = opts.getCurrentPath;

  doc.addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (shouldIgnoreShortcut(e.target)) return;

    const direct = pathFromKey(e.key);
    if (direct !== null) {
      if (getCurrentPath() !== direct) navigate(direct);
      e.preventDefault();
      return;
    }

    if (e.key === "`" || e.key === "~") {
      const dir = e.key === "~" ? -1 : 1;
      navigate(cyclePath(getCurrentPath(), dir));
      e.preventDefault();
    }
  });
}

if (typeof document !== "undefined") {
  document.addEventListener("DOMContentLoaded", () => {
    bindShortcuts({
      document,
      navigate: (url) => { window.location.href = url; },
      getCurrentPath: () => window.location.pathname,
    });
  });
}
