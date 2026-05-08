---
title: Web
weight: 5
---

One verb: `serve`. It starts the read-only browser dashboard described in [Web dashboard](../../web-dashboard/).

## `serve`

```sh
job serve                                  # 127.0.0.1:7823
job serve --bind 127.0.0.1:8080            # different loopback port
job serve --bind 0.0.0.0:7823              # listen on all interfaces (explicit opt-in)
```

What's worth knowing that the help text only hints at:

- **Loopback by default.** The default bind is `127.0.0.1:7823`, which means the dashboard isn't reachable from another machine until you say so. Binding to all interfaces requires writing the address out — `--bind 0.0.0.0:7823` — by design.
- **Quiet port walk on the default.** When `7823` is busy and you didn't pass `--bind`, `serve` walks the next 20 ports upward and binds the first free one, then prints the chosen URL. This is the convenience case for "I already have an instance running and I just want a second window."
- **Loud failure on an explicit bind.** When you pass `--bind` and the port is busy, `serve` fails immediately. You asked for that port specifically; silently binding a different one would be wrong.
- **Read-only.** No write paths exist on the HTTP surface. Closing a task from the dashboard isn't possible — that's deliberate; the CLI is the only writer. Stopping the server is `Ctrl-C`.

The dashboard's own contents — what each view shows, who it's for — live on [Web dashboard](../../web-dashboard/). For the JSON-lines event stream that powers it (and that you can consume directly), see the [Machine interface](../../machine-interface/) section.
