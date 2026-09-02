---
title: Descriptions and notes
weight: 10
---

Every free-text body a task carries — its description, each progress note, the completion note, a cancel reason — is **markdown prose**. Jobs stores exactly what you wrote and reflows it when it renders, so a hard-wrapped note reads as paragraphs in `job show` and in the dashboard instead of as a ragged column.

The rules are the ones CommonMark already uses for paragraphs, so text written for Jobs is correct markdown anywhere else:

- **A single newline is a soft break.** Lines inside a paragraph join with a space. Wrap at 72 columns if your editor wants to; nobody will see it.
- **A blank line ends a paragraph.**
- **A list item keeps its line.** `- `, `* ` and `+ ` open a bullet; `1. ` or `1) ` open a numbered item. Indent a continuation or a nested list under the item's text. An unindented line straight after a list starts a new paragraph — so `Remaining:` written under a bullet list is a heading for the next list, not part of the last bullet.
- **A fenced block is verbatim.** Three backticks or tildes open it; whitespace inside is preserved and it renders in monospace.
- **A hard break is a trailing backslash or two trailing spaces**, as in markdown, for the rare line that must break where it is.

Nothing you write is ever interpreted as HTML. Emphasis (`*bold*`, `_italic_`) is left as written on both surfaces.

````sh
job note abc12 -F - <<'NOTE'
Traced the flake to the sweep: it fires on every
read, so two readers race the same expiry.

Fix options:
- take the sweep off the read path
- keep it, but make the expiry write idempotent

Reproduce with:

```sh
go test ./internal/job -run Expiry -count=20
```
NOTE
````

That note renders as two paragraphs, a two-item list and a code block. `job show` prints it reflowed and indented under the note's header; the dashboard's task page, peek sheet and plan rows render real paragraphs, lists and `<pre>` blocks.

The same holds for the `desc` field of an [imported plan](../../plan-grammar/): YAML's `|` block keeps your newlines, and Jobs reflows them at display time.

## Inline

The dashboard runs a second pass over paragraph and list-item text for the three things that carry information rather than decoration. `job show` does none of it — the console prints inline syntax exactly as you typed it.

- **Code spans.** A run of backticks opens a span that the next run of the same length closes, and the content renders in monospace, escaped. A run that nothing closes is left as the backticks you typed.
- **Links.** `[text](url)` becomes a link when the target is an `http` or `https` URL, or a site-relative path starting with `/` (`/plan?show=all`). Any other target — a `javascript:` URL, a `//host` shorthand, a bare word — stays on the page as the characters you wrote. Link text itself renders code spans, and nothing more.
- **Ids.** A task id that the store recognises becomes a link to its task, written bare or in backticks. A criterion id links only in backticks: at three characters it would otherwise turn ordinary words into links, so `Bp7` links and Bp7 does not. The link lands on that criterion's row on its task's page — unless two tasks each carry a criterion with those three characters, in which case there is no single destination and it stays plain. An id-shaped word that names nothing stays plain text too, so nothing you write acquires a link by accident.

Fenced blocks are exempt from all of it: a task id inside a code block is text, like everything else in there.

