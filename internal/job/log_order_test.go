package job

import (
	"database/sql"
	"testing"
)

// TestLogOrder_CriterionSetByIntegratorAfterAgentReleaseSortsAfterIt pins the
// scenario reported against leaf hWckZ4 (part 2): an agent on one replica
// claims, notes and releases a leaf; its log is carried to a second replica
// (a git merge); the integrator then sets a criterion and closes on that
// second replica. `job log` was reported to show the resulting
// criterion_state/done pair sorted BEFORE the agent's earlier note and
// release, though they happened after.
//
// This does not reproduce: the hybrid logical clock (internal/eventlog.Clock)
// plus observeLogClock (rebuild.go) guarantee the integrator's next envelope
// sorts after every event its rebuild just ingested, tie or no tie. This test
// pins that guarantee for exactly the reported shape, including the tightest
// case — no wall-clock separation between the merge and the integrator's
// write — so a future change that weakens the guarantee fails here first.
func TestLogOrder_CriterionSetByIntegratorAfterAgentReleaseSortsAfterIt(t *testing.T) {
	p := newPair(t)

	var leaf string
	p.seed(func(db *sql.DB) {
		root := MustAdd(t, db, "", "Root")
		leaf = MustAdd(t, db, root, "Leaf")
		if _, err := RunAddCriteria(db, leaf, []Criterion{{Label: "tests pass"}}, "claude"); err != nil {
			t.Fatalf("add criteria: %v", err)
		}
	})

	p.A.do(func(db *sql.DB) {
		if err := RunClaim(db, leaf, "2h", "", "agent-x", false); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if err := RunNote(db, leaf, "did the work", nil, "agent-x"); err != nil {
			t.Fatalf("note: %v", err)
		}
		if err := RunRelease(db, leaf, "", "agent-x"); err != nil {
			t.Fatalf("release: %v", err)
		}
	})

	// No tick: the merge and the integrator's close happen at the same
	// simulated instant, the tightest version of the race. A one-way carry
	// rather than a full exchange, so the integrator's very next `job`
	// invocation is the one that both ingests the agent's log (a rebuild)
	// and writes the criterion and done events — exactly what
	// `job done --criterion ...` does as the first command after a
	// `git pull`.
	carryLog(t, p.A.dir, p.B.dir)

	p.B.do(func(db *sql.DB) {
		if _, err := RunSetCriterion(db, leaf, "tests pass", CriterionPassed, "claude"); err != nil {
			t.Fatalf("set criterion: %v", err)
		}
		if _, _, err := RunDone(db, []string{leaf}, false, "closing", nil, "claude", false, ""); err != nil {
			t.Fatalf("done: %v", err)
		}
	})

	// Carry B's close back to A so both sides hold the full history before
	// the ordering check — otherwise A simply hasn't seen the criterion/done
	// events yet, which would be a setup gap, not the bug under test.
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		events, err := GetEventsForTaskTree(db, leaf)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		idx := make(map[string]int, len(events))
		var order []string
		for i, e := range events {
			idx[e.EventType] = i
			order = append(order, e.EventType)
		}
		for _, want := range []string{"noted", "released", "criterion_state", "done"} {
			if _, ok := idx[want]; !ok {
				t.Fatalf("missing %q event, got order %v", want, order)
			}
		}
		if idx["criterion_state"] < idx["noted"] {
			t.Errorf("criterion_state sorted before noted: order %v", order)
		}
		if idx["criterion_state"] < idx["released"] {
			t.Errorf("criterion_state sorted before released: order %v", order)
		}
		if idx["done"] < idx["criterion_state"] {
			t.Errorf("done sorted before criterion_state: order %v", order)
		}
	})
}
