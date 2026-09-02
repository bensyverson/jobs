/*
  Client-side twin of internal/job/prose.go.

  Descriptions and notes are markdown prose: a single newline inside a
  paragraph is a soft break, a blank line ends a paragraph, list items
  keep their own line, fenced code is verbatim, and inline syntax is left
  as written. The scrubber rebuilds plan rows from replayed events, so it
  needs the same renderer the server uses. Keep this file a line-for-line
  port of prose.go — internal/job/testdata/prose_cases.json is run
  through both, and TestProseParity_GoAndJSAgree diffs them on inputs
  beyond the fixtures.
*/

import { escapeHTML } from "./scrub-util.mjs";

// parseProse splits text into blocks:
//   { kind: "paragraph", lines }             lines split at hard breaks
//   { kind: "code", fence, info, lines }     verbatim
//   { kind: "list", ordered, start, loose, items }  items are block arrays
export function parseProse(text) {
  if (text == null) return [];
  text = String(text).replace(/\r\n/g, "\n");
  if (text.trim() === "") return [];
  return parseLines(text.split("\n"));
}

function leadingSpaces(line) {
  let n = 0;
  while (n < line.length && line[n] === " ") n++;
  return n;
}

function markerAt(line) {
  const indent = leadingSpaces(line);
  const rest = line.slice(indent);
  if (rest === "") return null;
  const c = rest[0];
  if (c === "-" || c === "*" || c === "+") {
    if (rest.length > 1 && rest[1] === " " && rest.slice(2).trim() !== "") {
      const body = rest.slice(2).replace(/^ +/, "");
      return { ordered: false, number: 0, indent, content: indent + rest.length - body.length, text: body };
    }
    return null;
  }
  let digits = 0;
  while (digits < rest.length && digits < 9 && rest[digits] >= "0" && rest[digits] <= "9") digits++;
  if (digits === 0 || digits + 1 >= rest.length) return null;
  const sep = rest[digits];
  if (sep !== "." && sep !== ")") return null;
  if (rest[digits + 1] !== " " || rest.slice(digits + 2).trim() === "") return null;
  const body = rest.slice(digits + 2).replace(/^ +/, "");
  return {
    ordered: true,
    number: parseInt(rest.slice(0, digits), 10),
    indent,
    content: indent + rest.length - body.length,
    text: body,
  };
}

function fenceAt(line) {
  const indent = leadingSpaces(line);
  const rest = line.slice(indent);
  if (rest === "") return null;
  const c = rest[0];
  if (c !== "`" && c !== "~") return null;
  let n = 0;
  while (n < rest.length && rest[n] === c) n++;
  if (n < 3) return null;
  return { fence: rest.slice(0, n), info: rest.slice(n).trim(), indent };
}

function closesFence(line, fence) {
  const t = line.trim();
  if (t.length < fence.length) return false;
  for (const ch of t) if (ch !== fence[0]) return false;
  return true;
}

function dedent(line, n) {
  let i = 0;
  while (i < n && i < line.length && line[i] === " ") i++;
  return line.slice(i);
}

const isBlank = (line) => line.trim() === "";

function parseLines(lines) {
  const blocks = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (isBlank(line)) {
      i++;
      continue;
    }
    const f = fenceAt(line);
    if (f) {
      const block = { kind: "code", fence: f.fence, info: f.info, lines: [] };
      i++;
      while (i < lines.length && !closesFence(lines[i], f.fence)) {
        block.lines.push(dedent(lines[i], f.indent));
        i++;
      }
      i++;
      blocks.push(block);
      continue;
    }
    const m = markerAt(line);
    if (m) {
      const [block, next] = parseList(lines, i, m);
      blocks.push(block);
      i = next;
      continue;
    }
    const para = { kind: "paragraph", lines: [] };
    let current = [];
    const flush = () => {
      if (current.length > 0) {
        para.lines.push(current.join(" "));
        current = [];
      }
    };
    while (i < lines.length && !isBlank(lines[i])) {
      let l = lines[i];
      if (fenceAt(l) || markerAt(l)) break;
      let hard = l.endsWith("  ");
      l = l.trim();
      if (l.endsWith("\\")) {
        hard = true;
        l = l.slice(0, -1).replace(/[ \t]+$/, "");
      }
      current.push(l);
      if (hard) flush();
      i++;
    }
    flush();
    blocks.push(para);
  }
  return blocks;
}

function parseList(lines, i, first) {
  const block = { kind: "list", ordered: first.ordered, start: first.number, loose: false, items: [] };
  while (i < lines.length) {
    const m = markerAt(lines[i]);
    if (!m || m.ordered !== block.ordered) break;
    const item = [m.text];
    i++;
    let pendingBlank = 0;
    while (i < lines.length) {
      const l = lines[i];
      if (isBlank(l)) {
        pendingBlank++;
        i++;
        continue;
      }
      if (leadingSpaces(l) >= m.content) {
        if (pendingBlank > 0) block.loose = true;
        for (; pendingBlank > 0; pendingBlank--) item.push("");
        item.push(dedent(l, m.content));
        i++;
        continue;
      }
      break;
    }
    block.items.push(parseLines(item));
    if (i < lines.length) {
      const next = markerAt(lines[i]);
      if (!next || next.ordered !== block.ordered || next.indent >= m.content) break;
      if (pendingBlank > 0) block.loose = true;
    }
  }
  return [block, i];
}

// renderProseHTML renders text as escaped HTML blocks, byte-for-byte what
// job.RenderProseHTML emits.
export function renderProseHTML(text) {
  const out = [];
  renderBlocks(out, parseProse(text), false);
  return out.join("");
}

function renderBlocks(out, blocks, tight) {
  for (const b of blocks) {
    switch (b.kind) {
      case "code":
        out.push("<pre><code");
        if (b.info !== "") out.push(` class="language-${escapeHTML(b.info)}"`);
        out.push(">");
        for (const l of b.lines) out.push(escapeHTML(l), "\n");
        out.push("</code></pre>");
        break;
      case "list": {
        const tag = b.ordered ? "ol" : "ul";
        out.push("<" + tag);
        if (b.ordered && b.start !== 1) out.push(` start="${b.start}"`);
        out.push(">");
        for (const item of b.items) {
          out.push("<li>");
          renderBlocks(out, item, !b.loose);
          out.push("</li>");
        }
        out.push("</" + tag + ">");
        break;
      }
      default:
        if (!tight) out.push("<p>");
        out.push(b.lines.map(escapeHTML).join("<br>"));
        if (!tight) out.push("</p>");
    }
  }
}
