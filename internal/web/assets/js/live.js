/*
  Live updates — thin WebComponent wrapper around EventSource.
  Mount as <live-region src="/events?…"> (default src: /events). Views
  subscribe to two custom events on the element:

    'event'      detail = parsed event object from the server
    'connection' detail = 'connecting' | 'connected' | 'reconnecting'

  What this module owns:

    - EventSource lifecycle (open on connect, close on disconnect).
    - last-event-id persistence across reloads via localStorage so the
      dashboard resumes from where it stopped rather than missing the
      window between page-load-start and SSE-open.
    - Heartbeat rendering into [data-heartbeat] in the footer: pulsing
      dot + "last event Ns ago" / "reconnecting…" / "offline".

  What this module does NOT own:

    - DOM updates for any specific view. Views listen for the 'event'
      CustomEvent and update their own rows/columns. The module is
      intentionally ignorant of Log / Home / Actors structure.

  Reconnect strategy: EventSource's built-in reconnect retries on a
  fixed ~3s rhythm with no ceiling, which means an outage of an hour
  produces ~1200 retries per open tab. We close the stream on error
  and reopen it ourselves on a doubling delay (1s, 2s, 4s, …, capped
  at 30s) with jitter so a fleet of dashboards doesn't all reconnect
  on the same tick. The math lives in live-backoff.mjs and is
  duplicated here as computeBackoff because this script loads as a
  classic <script> rather than a module — the duplication is one short
  formula and the .mjs version is the unit-tested authority.
*/

(function () {
  "use strict";

  const STORAGE_KEY = "jobs.live.lastEventId";
  const HEARTBEAT_REFRESH_MS = 1000;
  const BACKOFF_BASE_MS = 1000;
  const BACKOFF_CAP_MS = 30000;
  const BACKOFF_JITTER = 0.2;

  // Mirror of live-backoff.mjs#computeBackoff. Keep them in sync.
  function computeBackoff(attempts) {
    const n = Math.max(0, attempts | 0);
    const raw = BACKOFF_BASE_MS * Math.pow(2, n);
    const capped = Math.min(BACKOFF_CAP_MS, raw);
    const j = BACKOFF_JITTER * (2 * Math.random() - 1);
    const ms = Math.round(capped * (1 + j));
    return Math.max(Math.floor(BACKOFF_BASE_MS / 2), ms);
  }

  // Every event type the store can emit. Listening for 'message' is
  // not enough: an SSE frame carrying `event: <name>` dispatches only
  // to a listener registered for that exact name, so an event type
  // missing here never reaches any live module at all — the row simply
  // never arrives. TestLiveSubscribesToEveryEmittedEventType (Go)
  // scrapes the recordEvent call sites in internal/job and fails if
  // this list falls behind them.
  const KNOWN_EVENT_TYPES = [
    "created",
    "claimed",
    "claim_expired",
    "released",
    "done",
    "reopened",
    "canceled",
    "noted",
    "blocked",
    "unblocked",
    "labeled",
    "unlabeled",
    "moved",
    "reparented",
    "edited",
    "criteria_added",
    "criterion_state",
    "found_in_set",
    "found_in_cleared",
    "kind_changed",
    "focus_set",
    "focus_released",
    "purged",
    "heartbeat",
  ];

  function saveLastID(id) {
    try {
      localStorage.setItem(STORAGE_KEY, String(id));
    } catch (_) {
      // Private browsing / storage quotas — safe to ignore; reconnect
      // still works within a single page life via EventSource's own
      // Last-Event-ID tracking.
    }
  }

  function loadLastID() {
    try {
      const v = localStorage.getItem(STORAGE_KEY);
      if (!v) return null;
      const n = parseInt(v, 10);
      return Number.isFinite(n) && n > 0 ? n : null;
    } catch (_) {
      return null;
    }
  }

  class LiveRegion extends HTMLElement {
    constructor() {
      super();
      this.es = null;
      this.lastEventAt = 0; // wall-clock ms of most recent event
      this.connectionState = "connecting";
      this.retryAttempts = 0;
      this.retryHandle = null;
      this.baseSrc = null;
    }

    connectedCallback() {
      this.baseSrc = this.getAttribute("src") || "/events";
      this.connect();
      this.startHeartbeatTick();

      // navigator.onLine distinguishes "your laptop's wifi dropped"
      // from "server is briefly unreachable but network is fine" —
      // both surface as EventSource errors otherwise.
      this.onOnline = () => {
        if (this.connectionState === "offline" || this.connectionState === "reconnecting") {
          this.setConnection("reconnecting");
          // Network just came back — retry now instead of waiting out
          // whatever delay the backoff timer was sitting on.
          if (this.retryHandle) {
            clearTimeout(this.retryHandle);
            this.retryHandle = null;
          }
          this.connect();
        }
      };
      this.onOffline = () => {
        this.setConnection("offline");
        // Stop retrying while the OS knows we're offline; the online
        // listener will kick a fresh connect attempt when we're back.
        if (this.retryHandle) {
          clearTimeout(this.retryHandle);
          this.retryHandle = null;
        }
      };
      window.addEventListener("online", this.onOnline);
      window.addEventListener("offline", this.onOffline);
      if (typeof navigator !== "undefined" && navigator.onLine === false) {
        this.setConnection("offline");
      }
    }

    disconnectedCallback() {
      if (this.es) this.es.close();
      this.es = null;
      if (this.tickHandle) clearInterval(this.tickHandle);
      if (this.retryHandle) clearTimeout(this.retryHandle);
      if (this.onOnline) window.removeEventListener("online", this.onOnline);
      if (this.onOffline) window.removeEventListener("offline", this.onOffline);
    }

    // connect builds a fresh URL (including the latest persisted
    // last-event-id) and opens the stream. Used both at mount time and
    // on every reconnect attempt so we always resume from the right id.
    connect() {
      const url = new URL(this.baseSrc, window.location.origin);
      const resume = loadLastID();
      if (resume) {
        url.searchParams.set("since", String(resume));
      }
      this.openStream(url.toString());
    }

    openStream(url) {
      this.es = new EventSource(url);

      this.es.addEventListener("open", () => {
        this.retryAttempts = 0;
        this.setConnection("connected");
      });
      this.es.addEventListener("error", () => {
        // Tear down the failed stream and schedule our own backoff
        // retry. EventSource's built-in retry runs every ~3s with no
        // ceiling — fine for a blip, painful during a real outage. By
        // closing here we own the cadence: 1s, 2s, 4s, … capped at 30s.
        if (this.es) {
          this.es.close();
          this.es = null;
        }
        this.setConnection("reconnecting");
        this.scheduleRetry();
      });

      for (const type of KNOWN_EVENT_TYPES) {
        this.es.addEventListener(type, (ev) => this.handleFrame(ev));
      }
      // Catch-all for any event type we didn't pre-register. SSE
      // dispatch to 'message' only fires when no event: field is
      // present, so this picks up server frames that omit it.
      this.es.addEventListener("message", (ev) => this.handleFrame(ev));
    }

    scheduleRetry() {
      if (this.retryHandle) clearTimeout(this.retryHandle);
      const delay = computeBackoff(this.retryAttempts);
      this.retryAttempts++;
      this.retryHandle = setTimeout(() => {
        this.retryHandle = null;
        // Skip while the OS reports offline — onOnline will trigger
        // a reconnect when connectivity returns.
        if (typeof navigator !== "undefined" && navigator.onLine === false) {
          return;
        }
        this.connect();
      }, delay);
    }

    handleFrame(ev) {
      let data = null;
      try {
        data = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
      if (data && typeof data.id === "number") {
        saveLastID(data.id);
      }
      this.lastEventAt = Date.now();
      this.dispatchEvent(new CustomEvent("event", { detail: data }));
    }

    setConnection(state) {
      if (state === this.connectionState) return;
      this.connectionState = state;
      this.dispatchEvent(new CustomEvent("connection", { detail: state }));
      renderHeartbeat(state, this.lastEventAt);
    }

    startHeartbeatTick() {
      renderHeartbeat(this.connectionState, this.lastEventAt);
      this.tickHandle = setInterval(() => {
        renderHeartbeat(this.connectionState, this.lastEventAt);
      }, HEARTBEAT_REFRESH_MS);
    }
  }

  // renderHeartbeat updates both footer slots.
  //   [data-heartbeat] gets the "last event Ns ago" rhythm — it's the
  //                    "is work still happening?" signal.
  //   [data-connection] gets the SSE connection state word —
  //                    "Connected" / "Reconnecting" / "Offline" — it's
  //                    the "is the stream alive?" signal. The two
  //                    disagree during an active outage, which is
  //                    exactly when a viewer most wants to tell them
  //                    apart.
  function renderHeartbeat(state, lastEventAt) {
    const hb = document.querySelector("[data-heartbeat]");
    if (hb) {
      const label = hb.querySelector("span:last-child") || hb;
      if (state === "connected") {
        label.textContent = lastEventAt
          ? "last event " + agoLabel(Date.now() - lastEventAt)
          : "listening";
      } else if (state === "reconnecting") {
        label.textContent = "reconnecting…";
      } else if (state === "offline") {
        label.textContent = "offline";
      } else {
        label.textContent = "connecting…";
      }
      hb.dataset.state = state;
    }

    const conn = document.querySelector("[data-connection]");
    if (conn) {
      const text =
        state === "connected" ? "Connected" :
        state === "reconnecting" ? "Reconnecting" :
        state === "offline" ? "Offline" :
        "Connecting";
      conn.textContent = text;
      conn.dataset.state = state;
    }
  }

  function agoLabel(ms) {
    const s = Math.max(0, Math.round(ms / 1000));
    if (s < 60) return s + "s ago";
    const m = Math.floor(s / 60);
    if (m < 60) return m + "m ago";
    const h = Math.floor(m / 60);
    return h + "h ago";
  }

  if (!customElements.get("live-region")) {
    customElements.define("live-region", LiveRegion);
  }

  // Expose for test scripts / console poking.
  window.JobsLive = { KNOWN_EVENT_TYPES: KNOWN_EVENT_TYPES };
})();
