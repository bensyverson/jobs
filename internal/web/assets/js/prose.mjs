/*
  Client-side twin of internal/job/prose.go.

  Descriptions and notes are markdown prose: a single newline inside a
  paragraph is a soft break, a blank line ends a paragraph, list items
  keep their own line, and fenced code is verbatim. Paragraph and list-item
  text then gets an inline pass — code spans, links, and autolinks for the
  short ids a resolver recognises — while emphasis stays as written. The
  scrubber rebuilds plan rows from replayed events, so it needs the same
  renderer the server uses. Keep this file a line-for-line port of
  prose.go and prose_inline.go — internal/job/testdata/prose_cases.json is
  run through both, and TestProseParity_GoAndJSAgree diffs them on inputs
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

// --- inline pass (twin of internal/job/prose_inline.go) ---

// The shortest id that may link outside a code span. Criterion short ids
// are three characters and collide with ordinary words; task short ids are
// six, so only they link bare.
const BARE_ID_MIN_LEN = 4;

// linkFor reads one id out of the resolver. `links` is a plain object built
// by the caller, so a bare links[id] would find Object.prototype members —
// "toString" is a perfectly good candidate token.
function linkFor(links, id) {
  if (!links || !Object.prototype.hasOwnProperty.call(links, id)) return "";
  const url = links[id];
  return typeof url === "string" ? url : "";
}

const isIDChar = (c) =>
  (c >= "0" && c <= "9") || (c >= "a" && c <= "z") || (c >= "A" && c <= "Z");

function isCandidate(s) {
  if (s === "") return false;
  for (const c of s) if (!isIDChar(c)) return false;
  return true;
}

function runLength(s, i, c) {
  let n = 0;
  while (i + n < s.length && s[i + n] === c) n++;
  return n;
}

// closingBacktickRun finds the next run of exactly n backticks at or after
// `from`, or -1. A longer or shorter run neither closes nor is scanned into.
function closingBacktickRun(s, from, n) {
  let j = from;
  while (j < s.length) {
    if (s[j] !== "`") {
      j++;
      continue;
    }
    const m = runLength(s, j, "`");
    if (m === n) return j;
    j += m;
  }
  return -1;
}

// parseInlineLink reads [text](url) starting at the "[" at i. Brackets do
// not nest: the first "]" ends the text.
function parseInlineLink(s, i) {
  let close = s.indexOf("]", i + 1);
  if (close < 0) return null;
  if (close + 1 >= s.length || s[close + 1] !== "(") return null;
  const paren = s.indexOf(")", close + 2);
  if (paren < 0) return null;
  return { text: s.slice(i + 1, close), url: s.slice(close + 2, paren), end: paren + 1 };
}

// hasPrefixFold is startsWith, case-insensitive over ASCII only. A
// Unicode-aware toLowerCase would diverge from the Go twin.
function hasPrefixFold(s, prefix) {
  if (s.length < prefix.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    let c = s[i];
    if (c >= "A" && c <= "Z") c = c.toLowerCase();
    if (c !== prefix[i]) return false;
  }
  return true;
}

// urlAllowed: http(s) or a site-relative path. A protocol-relative
// "//host" is refused with every other scheme — it is an external target
// wearing a relative path's clothes.
function urlAllowed(url) {
  if (url.startsWith("//")) return false;
  if (url.startsWith("/")) return true;
  return hasPrefixFold(url, "http://") || hasPrefixFold(url, "https://");
}

function writeCodeSpan(out, content, links, allowLinks) {
  const url = allowLinks && isCandidate(content) ? linkFor(links, content) : "";
  if (url !== "") out.push(`<a href="${escapeHTML(url)}">`);
  out.push("<code>", escapeHTML(content), "</code>");
  if (url !== "") out.push("</a>");
}

// writeInlinePlain escapes a run of plain text, linking the candidate
// tokens the resolver recognises.
function writeInlinePlain(out, text, links, allowLinks) {
  if (text === "") return;
  if (!allowLinks || !links) {
    out.push(escapeHTML(text));
    return;
  }
  let start = 0;
  for (let i = 0; i <= text.length; i++) {
    if (i < text.length && isIDChar(text[i])) continue;
    if (i > start) {
      const token = text.slice(start, i);
      const url = token.length >= BARE_ID_MIN_LEN ? linkFor(links, token) : "";
      if (url !== "") {
        out.push(`<a href="${escapeHTML(url)}">`, token, "</a>");
      } else {
        out.push(token);
      }
    }
    if (i < text.length) out.push(escapeHTML(text[i]));
    start = i + 1;
  }
}

// renderInline appends text with the inline pass applied. With allowLinks
// false — inside link text — neither [](…) nor an id autolink fires, so no
// <a> can nest inside another.
function renderInline(out, text, links, allowLinks) {
  let plain = 0;
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    if (c === "`") {
      const n = runLength(text, i, "`");
      const close = closingBacktickRun(text, i + n, n);
      if (close < 0) {
        // Nothing closes this run: the backticks are literal, and skipping
        // past them stops a later run re-opening inside it.
        i += n;
        continue;
      }
      writeInlinePlain(out, text.slice(plain, i), links, allowLinks);
      writeCodeSpan(out, text.slice(i + n, close), links, allowLinks);
      i = close + n;
      plain = i;
      continue;
    }
    if (c === "[" && allowLinks) {
      const link = parseInlineLink(text, i);
      if (!link || !urlAllowed(link.url)) {
        i++;
        continue;
      }
      writeInlinePlain(out, text.slice(plain, i), links, allowLinks);
      out.push(`<a href="${escapeHTML(link.url)}">`);
      renderInline(out, link.text, links, false);
      out.push("</a>");
      i = link.end;
      plain = i;
      continue;
    }
    i++;
  }
  writeInlinePlain(out, text.slice(plain), links, allowLinks);
}

// renderProseHTML renders text as escaped HTML blocks, byte-for-byte what
// job.RenderProseHTML emits. `links` is a plain object mapping a short id
// to the URL the inline pass links it to; omit it to link nothing.
export function renderProseHTML(text, links = null) {
  const out = [];
  renderBlocks(out, parseProse(text), links, false);
  return out.join("");
}

function renderBlocks(out, blocks, links, tight) {
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
          renderBlocks(out, item, links, !b.loose);
          out.push("</li>");
        }
        out.push("</" + tag + ">");
        break;
      }
      default:
        if (!tight) out.push("<p>");
        b.lines.forEach((l, i) => {
          if (i > 0) out.push("<br>");
          renderInline(out, l, links, true);
        });
        if (!tight) out.push("</p>");
    }
  }
}
