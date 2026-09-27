// Tests for internal/web/assets/js/chart-panel-url.mjs — which server
// fragment the <chart-panel> element fetches for a page URL. The
// element holds no counting rules (reporting decision 12): it only
// asks GET /home/panel for the range and cursor the address bar names.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  PANEL_FRAGMENT_PATH,
  PANEL_LINK_SELECTOR,
  panelFragmentURL,
  isPlainClick,
} from "../assets/js/chart-panel-url.mjs";

test("panelFragmentURL: the default view asks for the bare fragment", () => {
  assert.equal(panelFragmentURL("/"), PANEL_FRAGMENT_PATH);
  assert.equal(PANEL_FRAGMENT_PATH, "/home/panel");
});

test("panelFragmentURL: carries ?range= and ?at=", () => {
  assert.equal(panelFragmentURL("/?range=1d&at=1700000000-r1-4"), "/home/panel?range=1d&at=1700000000-r1-4");
});

// The Burn-up · Activity toggle is gone (chart panel revision, decision
// 3): both charts always render, so a stale ?chart= is not forwarded.
test("panelFragmentURL: drops a stale ?chart=", () => {
  assert.equal(panelFragmentURL("/?chart=activity"), "/home/panel");
  assert.equal(panelFragmentURL("/?at=1-a-2&chart=activity&range=7d"), "/home/panel?range=7d&at=1-a-2");
});

test("PANEL_LINK_SELECTOR: only the range tabs swap in place", () => {
  assert.equal(PANEL_LINK_SELECTOR, ".c-chart-panel__ranges a[href]");
});

test("panelFragmentURL: drops what the panel does not read", () => {
  assert.equal(panelFragmentURL("/?preview=abc12&range=all#top"), "/home/panel?range=all");
});

test("panelFragmentURL: accepts an absolute href", () => {
  assert.equal(panelFragmentURL("http://127.0.0.1:7823/?at=1-a-2"), "/home/panel?at=1-a-2");
});

test("isPlainClick: only an unmodified primary click is intercepted", () => {
  const base = { button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, defaultPrevented: false };
  assert.equal(isPlainClick(base), true);
  for (const k of ["metaKey", "ctrlKey", "shiftKey", "altKey", "defaultPrevented"]) {
    assert.equal(isPlainClick({ ...base, [k]: true }), false, k);
  }
  assert.equal(isPlainClick({ ...base, button: 1 }), false);
});
