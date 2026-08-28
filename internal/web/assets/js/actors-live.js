/*
  Actors board live updates (/actors, data-actors-board). One column
  per actor; one card per (actor, task) pair. The latest
  state-changing event sets the card's verb tint; `noted` events fold
  into a "N notes" badge.

  Self-guarded: without the board marker the module is a no-op, so it
  can be loaded from the shared layout without per-page wiring.

  The single-actor page (/actors/<name>) is NOT here — it lives in
  actor-single-live.mjs, which renders its event rows through the
  shared log-row.mjs rather than a second private renderer.

  What this module does NOT own: any DOM outside the board marker.
*/

(function () {
  "use strict";

  // Event types whose verb sets the card's tint. Mirrors
  // handlers.stateChangingTypes server-side.
  const STATE_CHANGING = new Set([
    "created", "claimed", "done", "blocked",
    "unblocked", "released", "canceled",
  ]);

  document.addEventListener("DOMContentLoaded", init);

  function init() {
    const live = document.querySelector("live-region");
    if (!live) return;

    const board = document.querySelector("[data-actors-board]");
    if (!board) return;

    live.addEventListener("event", (ev) => {
      const data = ev.detail;
      if (!data || data.id == null) return;
      applyBoardEvent(board, data);
    });
  }

  function applyBoardEvent(board, e) {
    if (!e.actor) return;
    const col = ensureColumn(board, e.actor);
    if (!col) return;
    const stream = col.querySelector(".c-actor-col__stream");
    if (!stream) return;

    const taskShortID = e.task_id || "";
    const cardKey = e.actor + ":" + taskShortID;
    let card = stream.querySelector('[data-actor-task="' + cssEscape(cardKey) + '"]');

    if (e.event_type === "noted") {
      if (!card) {
        // No existing card to attach the note to; ignore. The next
        // state-changing event for this pair will create one.
        return;
      }
      bumpNoteCount(card);
      paintIfAvailable(card);
      return;
    }

    if (!STATE_CHANGING.has(e.event_type)) return;

    const eventAt = epochSecondsOf(e.created_at);
    if (card) {
      const prev = parseInt(card.getAttribute("data-event-at") || "0", 10);
      if (eventAt < prev) return; // older than what's shown — ignore
      updateCard(card, e, eventAt);
    } else {
      card = renderCard(e, eventAt);
      stream.prepend(card);
    }
    placeCardInBand(stream, card, e);
    paintIfAvailable(card);
  }

  function ensureColumn(board, actor) {
    let col = board.querySelector('[data-actor="' + cssEscape(actor) + '"]');
    if (col) return col;
    // First-time appearance: build a fresh column. SSR-rendered
    // columns include avatar + name + status; we only render enough
    // here to host new cards. A page reload fills in the rest.
    col = document.createElement("section");
    col.className = "c-actor-col";
    col.setAttribute("data-actor", actor);
    col.setAttribute("aria-labelledby", "a-" + actor);

    const header = document.createElement("header");
    header.className = "c-actor-col__header";
    const avatar = document.createElement("a");
    avatar.className = "c-avatar c-avatar-lg";
    avatar.href = "/actors/" + encodeURIComponent(actor);
    avatar.setAttribute("data-actor", actor);
    avatar.setAttribute("aria-label", "Open " + actor);
    header.appendChild(avatar);

    const stack = document.createElement("div");
    stack.className = "stack stack-gap-xs";
    stack.style.minWidth = "0";
    const nameLink = document.createElement("a");
    nameLink.id = "a-" + actor;
    nameLink.className = "c-actor-col__name";
    nameLink.href = "/actors/" + encodeURIComponent(actor);
    nameLink.setAttribute("data-actor", actor);
    nameLink.textContent = actor;
    stack.appendChild(nameLink);
    const status = document.createElement("span");
    status.className = "c-actor-col__status";
    status.textContent = "just joined";
    stack.appendChild(status);
    header.appendChild(stack);
    col.appendChild(header);

    const stream = document.createElement("div");
    stream.className = "c-actor-col__stream";
    col.appendChild(stream);
    board.prepend(col);
    return col;
  }

  function renderCard(e, eventAt) {
    const card = document.createElement("article");
    card.className = "c-actor-card c-actor-card--" + safeClass(e.event_type);
    card.setAttribute("data-actor-task", e.actor + ":" + (e.task_id || ""));
    card.setAttribute("data-event-at", String(eventAt));

    const meta = document.createElement("div");
    meta.className = "c-actor-card__meta";
    const verb = document.createElement("span");
    verb.className = "c-log-row__verb c-log-row__verb--" + safeClass(e.event_type);
    verb.textContent = e.event_type;
    meta.appendChild(verb);
    const time = document.createElement("time");
    time.textContent = "just now";
    meta.appendChild(time);
    card.appendChild(meta);

    const titleRow = document.createElement("div");
    titleRow.className = "c-actor-card__title-row";
    const idPill = document.createElement("span");
    idPill.className = "c-id-pill";
    idPill.textContent = e.task_id || "";
    titleRow.appendChild(idPill);
    const title = document.createElement("h4");
    title.className = "c-actor-card__title";
    title.textContent = e.task_title || "";
    titleRow.appendChild(title);
    card.appendChild(titleRow);

    const link = document.createElement("a");
    link.className = "c-row-link";
    link.href = "/tasks/" + encodeURIComponent(e.task_id || "");
    link.setAttribute("aria-label", "Open task " + (e.task_id || ""));
    card.appendChild(link);
    return card;
  }

  function updateCard(card, e, eventAt) {
    card.classList.forEach((cls) => {
      if (cls.indexOf("c-actor-card--") === 0 && cls !== "c-actor-card") {
        card.classList.remove(cls);
      }
    });
    card.classList.add("c-actor-card--" + safeClass(e.event_type));
    card.setAttribute("data-event-at", String(eventAt));

    const verb = card.querySelector(".c-actor-card__meta > .c-log-row__verb");
    if (verb) {
      verb.className = "c-log-row__verb c-log-row__verb--" + safeClass(e.event_type);
      verb.textContent = e.event_type;
    }
    const time = card.querySelector(".c-actor-card__meta > time");
    if (time) time.textContent = "just now";
  }

  function bumpNoteCount(card) {
    let badge = card.querySelector(".c-actor-card__notes");
    if (!badge) {
      badge = document.createElement("span");
      badge.className = "c-actor-card__notes";
      badge.setAttribute("data-note-count", "1");
      badge.textContent = "1 note";
      const meta = card.querySelector(".c-actor-card__meta");
      const time = meta && meta.querySelector("time");
      if (meta) {
        if (time) meta.insertBefore(badge, time);
        else meta.appendChild(badge);
      }
      return;
    }
    const next = (parseInt(badge.getAttribute("data-note-count") || "0", 10) || 0) + 1;
    badge.setAttribute("data-note-count", String(next));
    badge.textContent = next === 1 ? "1 note" : next + " notes";
  }

  // placeCardInBand keeps the DOM band invariant: claim cards (DOM
  // first, visual bottom thanks to column-reverse) followed by
  // history. The 'claimed' verb promotes a card to the claim band;
  // 'released'/'done'/'canceled' demote it. Other transitions leave
  // the band alone — a 'blocked' edge during a claim doesn't cancel
  // the claim itself.
  function placeCardInBand(stream, card, e) {
    if (e.event_type === "claimed") {
      card.setAttribute("data-claim", "1");
      stream.prepend(card);
      return;
    }
    if (e.event_type === "released" || e.event_type === "done" || e.event_type === "canceled") {
      card.removeAttribute("data-claim");
    }
    if (card.getAttribute("data-claim") === "1") {
      stream.prepend(card); // refresh claim ordering newest-first
      return;
    }
    // History card — newest-first immediately after the claim band.
    const claimEnd = lastClaimChild(stream);
    if (claimEnd && claimEnd.nextSibling) {
      stream.insertBefore(card, claimEnd.nextSibling);
    } else if (!claimEnd) {
      stream.prepend(card);
    } else {
      stream.appendChild(card);
    }
  }

  function lastClaimChild(stream) {
    const claims = stream.querySelectorAll('[data-claim="1"]');
    return claims.length ? claims[claims.length - 1] : null;
  }

  // --- helpers --------------------------------------------------------

  function paintIfAvailable(node) {
    if (window.JobsColors && typeof window.JobsColors.paint === "function") {
      window.JobsColors.paint(node);
    }
  }

  function safeClass(s) {
    return String(s || "").replace(/[^a-zA-Z0-9_-]/g, "");
  }

  // CSS.escape isn't universal; provide a tiny fallback. Only matters
  // for actor names with selector-special characters, which the API
  // doesn't currently emit but might in future.
  function cssEscape(s) {
    if (window.CSS && typeof window.CSS.escape === "function") {
      return window.CSS.escape(s);
    }
    return String(s).replace(/["\\]/g, "\\$&");
  }

  function epochSecondsOf(rfc3339) {
    if (!rfc3339) return Math.floor(Date.now() / 1000);
    const t = Date.parse(rfc3339);
    return isNaN(t) ? Math.floor(Date.now() / 1000) : Math.floor(t / 1000);
  }
})();
