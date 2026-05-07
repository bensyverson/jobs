#!/usr/bin/env bash
# verify-getting-started.sh
#
# Re-runs the canonical walkthrough captured in
# docs/content/docs/getting-started/first-plan.md against a fresh, throwaway
# .jobs.db and prints the output for human review.
#
# This is a drift detector, not a deterministic regression test: Jobs assigns
# random short IDs at creation time, so the *exact* output cannot match the
# committed doc. What you're checking when you re-run this is that the
# *shapes* still match — section headers, line counts, message formats. If
# the binary's output format changes, eyeballing this script's output
# against first-plan.md surfaces the drift.
#
# Usage:
#   scripts/verify-getting-started.sh
#
# Requires `job` on PATH.

set -euo pipefail

if ! job --help >/dev/null 2>&1; then
  echo "error: 'job' not runnable on PATH. Run 'make install' (or 'go install ./cmd/job') first." >&2
  exit 1
fi

scratch="$(mktemp -d "${TMPDIR:-/tmp}/jobs-verify-getting-started-XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
db="$scratch/.jobs.db"
plan="$scratch/plan.md"

cat > "$plan" <<'PLAN'
# Add /healthz endpoint

We want a minimal liveness probe so the load balancer can take an
unhealthy node out of rotation. The handler is small; wiring it into
the router has to wait until the handler exists and passes its tests.

```yaml
tasks:
  - title: Add /healthz endpoint
    ref: healthz
    labels: [release, p1]
    children:
      - title: Write the handler
        ref: handler
        desc: |
          200 OK with a JSON body of `{"status":"ok"}`. No auth, no DB
          touch — the probe must stay cheap.
        criteria:
          - returns 200 status code
          - response body is valid JSON
      - title: Wire it into the router
        blockedBy: [handler]
        labels: [glue]
```
PLAN

step() {
  echo
  echo "=== $1 ==="
}

step "1. job init --default-identity alice --gitignore"
job --db "$db" init --default-identity alice --gitignore

step "2. job import plan.md --dry-run"
job --db "$db" import "$plan" --dry-run

step "3. job import plan.md"
import_output="$(job --db "$db" import "$plan")"
echo "$import_output"

# Pull the leaf IDs out of the import echo so we can drive the rest of
# the sequence without hard-coding random IDs. Layout: parent on line 1,
# leaves on lines 2 and 3.
handler_id="$(echo "$import_output" | sed -n '2p' | awk '{print $1}')"
router_id="$(echo "$import_output" | sed -n '3p' | awk '{print $1}')"

step "4. job status"
job --db "$db" status

step "5. job claim --next"
job --db "$db" claim --next

step "6. job done $handler_id --all-passed --claim-next -m '...'"
job --db "$db" done "$handler_id" --all-passed --claim-next \
  -m 'Returns 200 OK with the expected JSON body.'

step "7. job done $router_id -m '...'  (parent should auto-close)"
job --db "$db" done "$router_id" \
  -m 'Mounted on the default router; smoke-tested via curl.'

step "8. job status (final)"
job --db "$db" status

echo
echo "Done. Compare the shapes above against docs/content/docs/getting-started/first-plan.md."
