# Mobile dashboard: design exploration

*2026-08-30. A `.pen` design for a phone-sized Jobs dashboard, drawn with `woodcase` rather than as HTML prototypes. The file is [designs/2026-08-30-mobile-dashboard.pen](designs/2026-08-30-mobile-dashboard.pen); `woodcase serve project/designs/2026-08-30-mobile-dashboard.pen --port 7340` renders it live, and `woodcase shot <file> Now --theme mode=dark --out now.png` renders one screen. Exploration only — nothing here is implemented, and DESIGN.md is unchanged until a direction is chosen.*

## The brief, as pinned

One reader — the human away from the desk — with three questions in order: *is it alive, is anything stuck, who is doing what.* Today the dashboard only degrades to a single column below 720px (`components.css`, "Narrow-viewport fallback"); the graph and scrubber are hidden and the rest stacks. This design treats the phone as its own surface with its own hierarchy, not a squashed desktop.

## Choices

- **Tokens are DESIGN.md's.** The `.pen` carries the same palette as themed variables (`mode=dark` / `mode=light`), the same status axis, the same teal chrome. Inter and JetBrains Mono stand in for `system-ui` / `ui-monospace`, which a design file cannot name.
- **Signature: the pulse masthead.** The 60-minute activity histogram becomes the title's underline — a thin full-width strip under "Jobs", with the `● Live · 4s ago` pill beside the title and the footer's raw counts (`18 events · last 60m`, `3 actors · 5 wip`) as its caption. On the desktop the footer is the "is it alive" affordance; on a phone the top of the screen is the only thing guaranteed to be seen, so the heartbeat moves there.
- **Needs you, then Working now, then Just now.** Signals as rows, not cards: a blocked task (status pill + `waiting on` + blocker id), the oldest todo. Actor rows carry their current claim and its age; a long claim's age is `signal-warn` orange, which keeps amber and orange in different sections rather than adjacent — a DESIGN.md rule that a "stuck" list would have broken.
- **Tabs move to the bottom.** Now · Plan · Actors · Log in the thumb zone. This consciously overrides DESIGN.md's "top-nav only" for phones; the rule was written against sidebars and desktop chrome, and a bottom tab bar is the platform convention. Record it in DESIGN.md if adopted.
- **The peek sheet becomes a bottom sheet** over the current view, same contents as the desktop sheet (id, kind, title, status + actor, waiting-on, criteria, latest note as a code block, "Open full page").
- **Plan** is the same indented tree at 44px rows: caret · status glyph · title · right-hand `done/total` on parents, actor avatar on claimed leaves, a status pill on blocked ones.
- **Log** keeps the range tabs and the chip strips (with the `+N more` overflow chip); rows become two lines — `age · actor · verb · id · meta` over the title — because the desktop's six-column grid has no width to live in.
- **Actors** stacks the desktop's columns into cards per actor (avatar-lg, name, `1 claim · seen 4s ago`) with the same Trello-style task cards beneath; idle actors dim to 0.72 as on the desktop.

## What was left out, deliberately

The task map (graph) and the history scrubber. Both are viewport gestures the desktop earns; on a phone the question is "what is happening", not "how is it related" or "what happened at 14:02".

## Open questions for review

1. Is a bottom tab bar the right override, or should the phone keep the top tabs and treat the masthead as the whole header?
2. Do the raw counts belong in the masthead caption, or does that re-create the "scoreboard" DESIGN.md warns against?
3. The Actors cards use full-chroma borders for `claimed` / `blocked`; the desktop uses a tinted one. Quieter?
