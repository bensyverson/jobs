---
title: Criteria as tests
weight: 2
---

[Acceptance criteria](../../concepts/criteria/) are short sentences attached to a task that `job done` makes you account for before it will close. Written well, they are something more useful: the first draft of the leaf's unit tests, decided by whoever planned the work, before anyone writes code.

This recipe walks one leaf from plan to close with that discipline, and ends with what makes a criterion good or bad.

## Why it works

An agent that gets a leaf with a prose description has to derive the test plan itself — read the prose, guess at the edges, hope it covered them. An agent that gets a leaf with criteria doesn't: the planner already did that divergent thinking, and each row maps to a test case. One agent, after its first run through a fully criteria-bearing plan, put it this way: *"I wasn't inventing what to test. The spec named it… the red was already written for me."*

The close is the other half. Marking each row at `done` makes you re-read it as a question — *did I actually do this one?* In that same run, one criterion almost slipped: the CSS hook existed but the visible label didn't. Restating the criterion at close caught it.

## 1. Plan the criteria with the leaf

The criteria go in the plan, next to the description (see [Writing great plans](../great-plans/)):

```yaml
- title: Token bucket in internal/ratelimit
  ref: bucket
  desc: |
    A pure, clock-injected token bucket: `Allow(key) (ok bool,
    retryAfter time.Duration)`. No HTTP in this package — the
    middleware is a separate leaf.
  criteria:
    - a fresh key allows exactly `burst` calls, then refuses
    - a refused call reports the wait until the next token
    - tokens refill at `rate` per second against an injected clock
```

Three rows, three behaviors, each checkable by a test that either passes or doesn't. None of them says *how* — no "use a mutex", no "store buckets in a map". The implementation is the claimant's call; the behavior is the planner's.

## 2. Claim, and read the criteria as the red list

```sh
job claim jxs6sI 2h
```

```text
Claimed: jxs6sI "Token bucket in internal/ratelimit" (expires in 2h) as=alice

ID:           jxs6sI
Title:        Token bucket in internal/ratelimit
Status:       claimed
Claim:        claimed by alice, expires in 2h
Parent:       lQPrYh (Rate-limit the public API)
Blocks:       XRZTSR
Created:      2026-09-26 17:32

Description:
  A pure, clock-injected token bucket: `Allow(key) (ok bool, retryAfter time.Duration)`. No HTTP in this package — the middleware is a separate leaf.
Criteria: 3 pending — mark each before close, or use --force-close-with-pending
  Wcy [ ] a fresh key allows exactly `burst` calls, then refuses
  vjB [ ] a refused call reports the wait until the next token
  fKO [ ] tokens refill at `rate` per second against an injected clock
```

The three-character ids (`Wcy`, `vjB`, `fKO`) are the criteria's handles — you'll use them instead of quoting whole sentences through the shell.

## 3. Write one failing test per row

Each criterion becomes a test named for it. In Go, for example:

```go
func TestFreshKeyAllowsExactlyBurstThenRefuses(t *testing.T) {
	b := ratelimit.New(ratelimit.Config{Rate: 1, Burst: 3}, fakeclock.At(t0))
	for i := range 3 {
		if ok, _ := b.Allow("k"); !ok {
			t.Fatalf("call %d refused; want the first 3 allowed", i+1)
		}
	}
	if ok, _ := b.Allow("k"); ok {
		t.Fatal("call 4 allowed; want it refused once the burst is spent")
	}
}

func TestRefusedCallReportsWaitUntilNextToken(t *testing.T) { /* … */ }
func TestTokensRefillAtRateAgainstInjectedClock(t *testing.T) { /* … */ }
```

Run them and watch all three fail before writing the bucket. A test that is green before the code exists tests nothing — rewrite it until it is red for the right reason.

## 4. Mark rows as they go green — and add the one you missed

You don't have to wait for the close. As each test passes, mark its row:

```sh
job edit jxs6sI --set-criterion "Wcy=passed"
job edit jxs6sI --set-criterion "vjB=passed"
```

`edit` is not claim-guarded, so a reviewer can mark rows too, and the marks show up live in `job show` and the dashboard.

Working the tests will surface a behavior nobody wrote down. Here, the first draft keyed buckets by route instead of by API key. That is a criterion, not a footnote — add it, then write its test:

```sh
job edit jxs6sI --criterion "two keys never share a bucket"
job show jxs6sI
```

```text
Criteria: 2 pending — mark each before close, or use --force-close-with-pending
  Wcy [x] a fresh key allows exactly `burst` calls, then refuses
  vjB [x] a refused call reports the wait until the next token
  fKO [ ] tokens refill at `rate` per second against an injected clock
  jhK [ ] two keys never share a bucket
```

Now the criterion list — and the next person to read this leaf — knows about the bug class you just closed.

## 5. Close, marking what's left

If you close on autopilot, the close refuses and quotes back what's missing:

```sh
job done jxs6sI -m "Bucket landed."
```

```text
Error: cannot close: 2 pending criteria
  jxs6sI "Token bucket in internal/ratelimit":
    [ ] tokens refill at `rate` per second against an injected clock
    [ ] two keys never share a bucket
Override: --force-close-with-pending
```

That refusal is the point: it is the moment to go and look, not to reach for an override. With the last two tests green, mark them in the close itself:

```sh
job done jxs6sI --criterion fKO=passed --criterion jhK=passed -F done-note.md
```

```text
Done: jxs6sI "Token bucket in internal/ratelimit" as=alice
  note: 200 chars · "Bucket landed in internal/ratelimit with a fake clock; one…"
  Next: XRZTSR "Middleware returns 429 with Retry-After"
  Parent lQPrYh: 1 of 4 complete
```

When every remaining row really did pass, `--all-passed` does the same in one flag, and the ack says how many rows it marked. Use it after you've checked, not instead of checking.

## Passed, skipped, failed — and what each one means at close

A criterion is `pending`, `passed`, `skipped` or `failed`. Only `pending` blocks the close; the other three are records of what happened.

- **`passed`** — the behavior exists and something (usually a test) shows it.
- **`skipped`** — the criterion no longer applies: the plan changed, or a sibling leaf owns it now. Say why in the close note.
- **`failed`** — you checked and it isn't true. `done` **will close** over a failed row; it renders as `[!]` in `show`. That's deliberate: sometimes you ship the part that works and file the rest. When you do, file the gap as an issue (`job issue "…"`) so it isn't just a glyph on a closed task. When the work simply isn't finished, don't close — keep the leaf open, or [reopen](../recovery/#reopen-the-work-wasnt-finished) it.

`--force-close-with-pending` closes with rows still pending and records them as a waiver on the `done` event (`criteria_waived` in `job log --format=json`). It is for "we're shipping without verifying this, on purpose" — not for "I forgot to mark them".

## What makes a good criterion

The test for a criterion is the test for a unit test: could two people independently agree whether it passed?

| Instead of | Write | Why |
|---|---|---|
| handles rate limiting correctly | a refused request gets 429 and a Retry-After header in whole seconds | "correctly" can't be checked; a status and a header can |
| passes lint and tests | *(two rows, or neither)* | two assertions in one row can't be half-marked; and "tests pass" is true of every close, so it says nothing about this leaf |
| uses a sync.Map for the buckets | two keys never share a bucket | name the behavior, not the implementation — the implementation can change without the criterion rotting |
| run the integration suite | requests under /internal/ never touch the bucket | outcome, not steps |
| works well under load | 1,000 concurrent keys allow within 1ms p99 on the bench | if it matters, give it a number; if you can't, it isn't a criterion yet |

A few more habits:

- **Three to six rows per leaf** is typical. Ten rows usually means the leaf is two leaves.
- **Leave criteria off a leaf with nothing checkable** — a prose rewrite, a spike. An invented criterion teaches everyone to mark rows without reading them.
- **If a row is hard to mark honestly, it's the wrong granularity.** Split it or delete it.
- **Criteria describe the leaf, not the parent.** A parent closes when its children do, and that auto-close does not look at the parent's own criteria — put each one on the leaf that will satisfy it.

See also: [Acceptance criteria](../../concepts/criteria/), [`done`](../../reference/execution/#done), [`edit`](../../reference/planning/#edit).
