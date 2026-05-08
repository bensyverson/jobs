---
title: Web dashboard
weight: 6
---

`job serve` runs a read-only web UI for humans to watch agents work. It's a foreground process that binds `127.0.0.1:7823` by default and never accepts a write — the CLI is the only path that mutates the database.

<img src="../screenshots/home.png" alt="Jobs dashboard — Home view" width="860">

## Who it's for

The dashboard is for the human watching agents work. The CLI remains the surface agents reach for; the dashboard is the second monitor: ambient signal, no claims, no closes, no edits. Open it once at the start of a session, leave it visible, glance over when something changes.

## The four views

| View       | URL          | What it shows                                                                                  |
|------------|--------------|------------------------------------------------------------------------------------------------|
| Home       | `/`          | Activity histogram (last 60m), three alarm cards, active claims, recent completions.           |
| Plan       | `/plan`      | The full task tree, scoped (`/plan/{id}`) or filtered by label (`/labels/{name}`).             |
| Actors     | `/actors`    | Column-per-actor board — one stack of cards per identity, freshest at the bottom. Click through to `/actors/{name}` for a single actor's stream. |
| Log        | `/log`       | Linear stream of every event in the database, filterable by actor, label, type.               |

Two auxiliary pages — `/tasks/{id}` (single task with peek view at `/tasks/{id}/peek`) and `/search` — round out the click paths but aren't usually entry points.

## Live updates

Every view subscribes to `/events` over Server-Sent Events. New events arrive in the open browser without a reload, and reconnects are seamless via `Last-Event-ID`. The same endpoint is documented as a public API on the [Machine interface](../machine-interface/http-api/) page — if you want to consume the live stream from your own tooling, that's the page to read.

## Running it

```sh
job serve                                 # 127.0.0.1:7823
job serve --bind 127.0.0.1:8080           # different loopback port
job serve --bind 0.0.0.0:7823             # expose on all interfaces (explicit opt-in)
job serve --db ./other.jobs.db            # serve a database other than ./.jobs.db
```

Worth knowing:

- **Loopback by default.** Reaching the dashboard from another machine requires `--bind 0.0.0.0:<port>` (or a specific interface). The default is local-only by design — there is no authentication on the HTTP surface.
- **Quiet port walk on the default.** When `7823` is busy and `--bind` was omitted, `serve` walks the next 20 ports upward and binds the first free one. Useful when you want a second window without thinking.
- **Loud failure on an explicit bind.** When `--bind` was passed and that port is busy, `serve` errors out — you asked for that port specifically.
- **`--db` follows the same global flag as the CLI.** Point at any `.jobs.db` file; the dashboard will serve whatever's in it. Stop with `Ctrl-C`.

For the full reference on `serve` and the global flags, see [Reference → Web](../reference/web/).
