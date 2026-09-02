# Descriptions and notes are markdown prose

2026-09-02

## Why

Agents hard-wrap the text they write into descriptions and notes. The console already tolerates it: `unwrapProse` in `internal/job/format.go` joins single newlines, keeps blank lines as paragraph breaks, and keeps bullet lines on their own line. The dashboard does the opposite — three CSS rules (`.c-note`, `.c-progress-note__body`, `.c-plan-row__desc`) use `white-space: pre-wrap`, so every hard wrap becomes a visible line break, and a 72-column note reads as a ragged column on a wide monitor. The two surfaces disagree, and nothing defines what a note's newlines mean.

The fix is to adopt what CommonMark already says about paragraphs: a single newline inside a paragraph is a soft break, a blank line ends a paragraph, list items are one per line, fenced code is verbatim. Declaring "descriptions and notes are markdown" costs nothing today and commits to nothing: text written under this subset stays correct if a full renderer ever lands. Hard-wrapping agents become harmless instead of something to police in briefs.

## Decisions

- **Parse at display time, never normalize at write.** The event store keeps what was written; unwrapping is lossy for code blocks.
- **A small block parser in the core, no markdown dependency.** Paragraphs, bullet and numbered lists (with indented continuation lines), fenced code kept verbatim, and markdown hard breaks (trailing backslash or two spaces). It returns typed blocks; one text renderer for the console and one HTML renderer for the dashboard consume the same list. Inline syntax (backticks, emphasis, links) stays literal on both surfaces.
- **The dashboard's client-side renderer gets a twin** in `assets/js/prose.mjs` for the plan scrubber, which rebuilds rows from replayed events. Both parsers read one fixture file so they cannot drift.
- **Full markdown is parked** in `project/backlog.md`. Un-park: someone wants inline code or links rendered in the dashboard. Goldmark would be the choice (it is what Hugo uses for the docs site).
- **`DESIGN.md` said notes render as monospace code blocks.** The CSS had already moved to body font; this plan makes the design doc match the rendering it describes.

## Tasks

```yaml
tasks:
  - title: Descriptions and notes are markdown prose
    desc: |
      Adopt CommonMark paragraph rules for every description, note, completion note and cancel reason: single newlines are soft, blank lines separate paragraphs, list items keep their line, fenced code is verbatim. One block parser in `internal/job`, a text renderer for the console, an HTML renderer for the dashboard, and a JS twin for the plan scrubber. See project/2026-09-02-prose-rendering.md.
    labels: [prose]
    children:
      - title: Block parser and console renderer
        desc: |
          Add `internal/job/prose.go`: `ParseProse(string) []ProseBlock` with block kinds paragraph, list (ordered/unordered, items carry their own paragraphs), and code (fenced, verbatim). Handle indented continuation lines inside a list item, hard breaks (trailing backslash or two trailing spaces), and fences that never close. Add `RenderProseText` and replace `unwrapProse` with it in `format.go`. Fixtures live in `internal/job/testdata/prose_cases.json` (input, blocks, text, html) so the JS twin can read the same cases.
        labels: [prose, core]
        criteria:
          - Fixture-driven Go tests cover paragraphs, lists with continuation, numbered lists, fences, unclosed fences, hard breaks and trailing whitespace
          - unwrapProse is gone and the existing unwrap tests pass against the new renderer
          - A wrapped bullet's continuation line stays inside the bullet
      - title: Dashboard HTML renderer
        desc: |
          Add `RenderProseHTML` in `internal/job` and expose it as a `prose` template func in `internal/web/templates`. Use it for `.c-note`, `.c-progress-note__body` and `.c-plan-row__desc` in task, peek and plan templates. Drop `white-space: pre-wrap` from those rules and style `.c-prose` children (p, ul, ol, pre) per DESIGN.md; update DESIGN.md's note rendering claim. Escape all text; no raw HTML passes through.
        labels: [prose, web]
        blockedBy: [Block parser and console renderer]
        criteria:
          - The fixture html column matches RenderProseHTML output for every case
          - A hard-wrapped description renders as one flowing paragraph on /tasks/{id}, the peek sheet and /plan
          - Text containing <script> renders escaped
      - title: Client-side prose twin for the scrubber
        desc: |
          Add `internal/web/assets/js/prose.mjs` exporting `renderProseHTML(text)` producing the same markup as the Go renderer, and use it in `plan-scrub-render.mjs` for row descriptions. A node test loads `internal/job/testdata/prose_cases.json` and checks every html case; a Go parity test fails if the two renderers diverge on the fixtures. Register the module in the layout importmap.
        labels: [prose, web]
        blockedBy: [Dashboard HTML renderer]
        criteria:
          - node test passes every fixture case
          - Go parity test runs the JS twin over the fixtures and matches
          - Scrubbed plan rows render descriptions as paragraphs, not pre-wrap text
      - title: Document the prose contract
        desc: |
          Add `docs/content/docs/concepts/prose.md` stating the contract (markdown paragraph rules, what is and is not rendered, hard breaks), link it from the concepts index and the note/description reference; park full markdown in project/backlog.md with its un-park condition.
        labels: [prose, docs]
        criteria:
          - Concepts page exists and is linked from the concepts index
          - backlog.md carries the full-markdown entry with an un-park condition
```
