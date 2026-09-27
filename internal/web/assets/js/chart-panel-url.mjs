/*
  Pure helpers for the <chart-panel> element (chart-panel.mjs): which
  server fragment a page URL maps to, and which clicks the element may
  take over. No DOM, so node tests can import it.
*/

// PANEL_FRAGMENT_PATH mirrors the GET /home/panel route in
// internal/web/server/routes.go.
export const PANEL_FRAGMENT_PATH = "/home/panel";

// The query parameters the panel's handler reads (handlers/chart_panel.go):
// the range and the scrubber cursor. Everything else on the page URL
// belongs to some other part of the page.
const PANEL_PARAMS = ["range", "at"];

// PANEL_LINK_SELECTOR matches the links the element swaps in place
// rather than navigating: the range tabs (partials/chart_panel.html.tmpl).
export const PANEL_LINK_SELECTOR = ".c-chart-panel__ranges a[href]";

// panelFragmentURL maps a Home page URL (path or absolute) onto the
// fragment URL that renders its chart panel.
export function panelFragmentURL(href) {
  const src = new URL(href, "http://placeholder.invalid/");
  const out = new URLSearchParams();
  for (const k of PANEL_PARAMS) {
    const v = src.searchParams.get(k);
    if (v !== null && v !== "") out.set(k, v);
  }
  const q = out.toString();
  return PANEL_FRAGMENT_PATH + (q ? "?" + q : "");
}

// isPlainClick is true for an unmodified primary-button click that no
// one has handled yet — the only kind the panel swaps in place. Anything
// else (open in new tab, etc.) stays an ordinary link.
export function isPlainClick(e) {
  return (
    e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && !e.defaultPrevented
  );
}
