/*
  <chart-panel> — Home's burn-up + activity panel
  (templates/html/partials/chart_panel.html.tmpl).

  The server ships the whole panel as HTML, and the range selector is a
  group of plain links, so the panel works with JavaScript off. This
  element upgrades what is already there; it never renders a chart
  itself and holds none of the counting rules (reporting decision 12).
  It re-fetches the server fragment (GET /home/panel?range=&at=) and
  swaps its own contents when:

    - a range tab is clicked: pushState the tab's URL, fetch, swap;
    - the scrubber moves (jobs:scrubber-frame, debounced) or returns to
      live (jobs:scrubber-live) — the pill has already written ?at= to
      the address bar;
    - back/forward changes ?range= or ?at= (popstate);
    - a live event arrives (the <live-region>'s "event", debounced) so
      the live panel keeps up.

  While a fetch is in flight the element carries aria-busy="true",
  which the stylesheet dims — the preview catalog's "fetching" state
  sets the same attribute server-side.

  Light DOM, like <peek-sheet>: the panel is styled by the shared
  stylesheet and its tokens, and a shadow root would only have to
  re-import them. Self-guarded like the other layout scripts: an
  element that is not on the page does nothing.
*/

import { panelFragmentURL, isPlainClick } from "./chart-panel-url.mjs";

const SCRUB_DEBOUNCE_MS = 300;
const LIVE_DEBOUNCE_MS = 750;

class ChartPanel extends HTMLElement {
  connectedCallback() {
    this._shown = panelFragmentURL(window.location.href);
    this._timer = null;
    this._abort = null;

    this._onClick = (e) => {
      const a = e.target.closest && e.target.closest(".c-chart-panel__ranges a[href]");
      if (!a || !this.contains(a) || !isPlainClick(e)) return;
      e.preventDefault();
      window.history.pushState({}, "", a.getAttribute("href"));
      this.refresh();
    };
    this._onScrub = () => this.schedule(SCRUB_DEBOUNCE_MS);
    this._onLiveReturn = () => this.schedule(0);
    this._onPop = () => {
      if (panelFragmentURL(window.location.href) !== this._shown) this.refresh();
    };
    // A panel parked in history does not change when new events land.
    this._onEvent = () => {
      if (new URL(window.location.href).searchParams.get("at")) return;
      this.schedule(LIVE_DEBOUNCE_MS, { force: true });
    };

    this.addEventListener("click", this._onClick);
    document.addEventListener("jobs:scrubber-frame", this._onScrub);
    document.addEventListener("jobs:scrubber-live", this._onLiveReturn);
    window.addEventListener("popstate", this._onPop);
    this._live = document.querySelector("live-region");
    if (this._live) this._live.addEventListener("event", this._onEvent);
  }

  disconnectedCallback() {
    this.removeEventListener("click", this._onClick);
    document.removeEventListener("jobs:scrubber-frame", this._onScrub);
    document.removeEventListener("jobs:scrubber-live", this._onLiveReturn);
    window.removeEventListener("popstate", this._onPop);
    if (this._live) this._live.removeEventListener("event", this._onEvent);
    if (this._timer) clearTimeout(this._timer);
    if (this._abort) this._abort.abort();
  }

  // schedule coalesces bursts (a scrubber drag, a flurry of events)
  // into one fetch. Without force, a URL the panel already shows is
  // skipped: the scrubber fires frames for positions it has rendered.
  schedule(delay, { force = false } = {}) {
    if (this._timer) clearTimeout(this._timer);
    this._timer = setTimeout(() => {
      this._timer = null;
      if (force || panelFragmentURL(window.location.href) !== this._shown) this.refresh();
    }, delay);
  }

  async refresh() {
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
      this.replaceChildren(...Array.from(fresh.childNodes, (n) => document.importNode(n, true)));
      this._shown = url;
    } catch (_) {
      // Aborted by a newer fetch, or a network blip: the next trigger retries.
    } finally {
      if (this._abort === abort) {
        this._abort = null;
        this.removeAttribute("aria-busy");
      }
    }
  }
}

if (typeof customElements !== "undefined" && !customElements.get("chart-panel")) {
  customElements.define("chart-panel", ChartPanel);
}
