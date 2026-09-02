// Tests for internal/web/assets/js/prose.mjs — the client-side twin of
// internal/job/prose.go. Both read internal/job/testdata/prose_cases.json,
// and TestProseParity_GoAndJSAgree (handlers) runs this module from Go over
// the same inputs, so the two renderers cannot drift.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { renderProseHTML, parseProse } from "../assets/js/prose.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const cases = JSON.parse(
  readFileSync(join(here, "..", "..", "job", "testdata", "prose_cases.json"), "utf8"),
);

test("fixtures are present", () => {
  assert.ok(cases.length > 0);
});

for (const c of cases) {
  test(`renderProseHTML: ${c.name}`, () => {
    assert.equal(renderProseHTML(c.input), c.html);
  });
}

test("parseProse: structure of a mixed document", () => {
  const blocks = parseProse("intro\n\n- a\n  - b\n\n```go\nx\n```");
  assert.equal(blocks.length, 3);
  assert.deepEqual(blocks[0], { kind: "paragraph", lines: ["intro"] });
  assert.equal(blocks[1].kind, "list");
  assert.equal(blocks[1].ordered, false);
  assert.equal(blocks[1].items.length, 1);
  assert.equal(blocks[1].items[0][1].kind, "list");
  assert.deepEqual(blocks[2], { kind: "code", fence: "```", info: "go", lines: ["x"] });
});

test("renderProseHTML: null and undefined render empty", () => {
  assert.equal(renderProseHTML(null), "");
  assert.equal(renderProseHTML(undefined), "");
});
