/*
  <chart-panel> — Home's burn-up + activity panel
  (templates/html/partials/chart_panel.html.tmpl).

  The server ships the whole panel as HTML — the burn-up and the
  histogram side by side — and the range selector is plain links, so
  the panel works with JavaScript off. This element upgrades what is
  already there; it holds none of the counting rules (reporting
  decision 12). It re-fetches the server fragment (GET
  /home/panel?range=&at=) and swaps its own contents when:

    - a range tab is clicked: pushState the link's URL, fetch, and
      slide the burn-up from the old window to the new before the swap
      (chart-panel-slide.mjs) — unless the reader prefers reduced
      motion, read once at load, when it swaps at once;
    - the scrubber moves (jobs:scrubber-frame, debounced) or returns to
      live (jobs:scrubber-live) — the pill has already written ?at= to
      the address bar;
    - back/forward changes ?range= or ?at= (popstate);
    - a live event arrives (the <live-region>'s "event", debounced) so
      the live panel keeps up.

  Only the range click slides (chart-motion.mjs's swapMode): the others
  swap instantly, and a hover in progress is redrawn over the new
  contents. Whatever the path, the resting contents are the server's
  fragment.

  It also carries the crosshair (chart-panel-hover.mjs), drawn from the
  fragment's data island.

  While a fetch is in flight the element carries aria-busy="true",
  which the stylesheet dims — the preview catalog's "fetching" state
  sets the same attribute server-side.

  Light DOM, like <peek-sheet>: the panel is styled by the shared
  stylesheet and its tokens, and a shadow root would only have to
  re-import them. Self-guarded like the other layout scripts: an
  element that is not on the page does nothing.
*/

import { PANEL_LINK_SELECTOR, panelFragmentURL, isPlainClick } from "./chart-panel-url.mjs";
import { SwapCause, SwapMode, swapMode } from "./chart-motion.mjs";
import { PanelHover } from "./chart-panel-hover.mjs";
import { slide } from "./chart-panel-slide.mjs";

const SCRUB_DEBOUNCE_MS = 300;
const LIVE_DEBOUNCE_MS = 750;

// REDUCED is read once, at load: reduced motion skips the slide
// entirely rather than shortening it.
const REDUCED =
  typeof window !== "undefined" && window.matchMedia
    ? window.matchMedia("(prefers-reduced-motion: reduce)").matches
    : false;

// islandIn parses a fragment's data island; null when it has none.
function islandIn(root) {
  const el = root.querySelector("script.c-chart-panel__data");
  if (!el) return null;
  try {
    return JSON.parse(el.textContent);
  } catch (_) {
    return null;
  }
}

class ChartPanel extends HTMLElement {
  connectedCallback() {
    this._shown = panelFragmentURL(window.location.href);
    this._timer = null;
    this._abort = null;
    this._landNow = null;
    this._hover = new PanelHover(this);

    this._onClick = (e) => {
      const a = e.target.closest && e.target.closest(PANEL_LINK_SELECTOR);
      if (!a || !this.contains(a) || !isPlainClick(e)) return;
      e.preventDefault();
      window.history.pushState({}, "", a.getAttribute("href"));
      this.refresh(SwapCause.Range);
    };
    this._onScrub = () => this.schedule(SCRUB_DEBOUNCE_MS, { cause: SwapCause.Scrub });
    this._onLiveReturn = () => this.schedule(0, { cause: SwapCause.Scrub });
    this._onPop = () => {
      if (panelFragmentURL(window.location.href) !== this._shown) this.refresh(SwapCause.History);
    };
    // A panel parked in history does not change when new events land.
    this._onEvent = () => {
      if (new URL(window.location.href).searchParams.get("at")) return;
      this.schedule(LIVE_DEBOUNCE_MS, { force: true, cause: SwapCause.Live });
    };

    this.addEventListener("click", this._onClick);
    document.addEventListener("jobs:scrubber-frame", this._onScrub);
    document.addEventListener("jobs:scrubber-live", this._onLiveReturn);
    window.addEventListener("popstate", this._onPop);
    this._live = document.querySelector("live-region");
    if (this._live) this._live.addEventListener("event", this._onEvent);
    this._hover.bind();
  }

  disconnectedCallback() {
    this.removeEventListener("click", this._onClick);
    document.removeEventListener("jobs:scrubber-frame", this._onScrub);
    document.removeEventListener("jobs:scrubber-live", this._onLiveReturn);
    window.removeEventListener("popstate", this._onPop);
    if (this._live) this._live.removeEventListener("event", this._onEvent);
    if (this._timer) clearTimeout(this._timer);
    if (this._abort) this._abort.abort();
    this._hover.unbind();
  }

  // schedule coalesces bursts (a scrubber drag, a flurry of events)
  // into one fetch. Without force, a URL the panel already shows is
  // skipped: the scrubber fires frames for positions it has rendered.
  schedule(delay, { force = false, cause = SwapCause.Live } = {}) {
    if (this._timer) clearTimeout(this._timer);
    this._timer = setTimeout(() => {
      this._timer = null;
      if (force || panelFragmentURL(window.location.href) !== this._shown) this.refresh(cause);
    }, delay);
  }

  async refresh(cause = SwapCause.Live) {
    const url = panelFragmentURL(window.location.href);
    if (this._abort) this._abort.abort();
    const abort = new AbortController();
    this._abort = abort;
    this.setAttribute("aria-busy", "true");
    try {
      const res = await fetch(url, {
        headers: { Accept: "text/html" },
        credentials: "same-origin",
        signal: abort.signal,
      });
      if (!res.ok) return;
      const html = await res.text();
      const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("chart-panel");
      if (!fresh || abort.signal.aborted) return;
      this.swap(fresh, url, cause);
    } catch (_) {
      // Aborted by a newer fetch, or a network blip: the next trigger retries.
    } finally {
      if (this._abort === abort) {
        this._abort = null;
        this.removeAttribute("aria-busy");
      }
    }
  }

  // swap puts the server's fragment in place — after a slide when the
  // cause earns one, at once otherwise. A slide still running from an
  // earlier click lands first, so two never overlap.
  swap(fresh, url, cause) {
    if (this._landNow) this._landNow();
    const put = () => {
      this.replaceChildren(...Array.from(fresh.childNodes, (n) => document.importNode(n, true)));
      this._shown = url;
      this._hover.sync();
    };
    const from = this._hover.island();
    const to = islandIn(fresh);
    if (swapMode({ cause, reduced: REDUCED, from, to }) !== SwapMode.Animate) {
      put();
      return;
    }
    this._hover.clear();
    this._landNow = slide(this, from, to, () => {
      this._landNow = null;
      put();
    });
  }
}

if (typeof customElements !== "undefined" && !customElements.get("chart-panel")) {
  customElements.define("chart-panel", ChartPanel);
}
