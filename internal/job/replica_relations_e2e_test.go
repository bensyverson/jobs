package job

import (
	"database/sql"
	"slices"
	"testing"
)

// The claims and relations rows of the merge-rule table in
// project/2026-09-01-git-native-event-log.md: claimed, released, heartbeat,
// labeled/unlabeled, blocked/unblocked, moved/reparented, criteria_added,
// criterion_state, found_in and kind_changed. The task-lifecycle rows are in
// replica_rules_e2e_test.go and the harness is in replica_e2e_test.go.

// `claimed` against `claimed`: the one lossy case. The earlier (ts, rep) holds
// the task on both machines and the later is released with reason lost-merge.
func TestMergeRule_ClaimedVersusClaimed(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "contended") })

	p.A.do(func(db *sql.DB) {
		if err := RunClaim(db, task, "4h", "", "ben", false); err != nil {
			t.Fatalf("claim on A: %v", err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if err := RunClaim(db, task, "4h", "", "sam", false); err != nil {
			t.Fatalf("claim on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "claimed_by", "ben")
		if n := countEvents(t, db, task, EventReleased, string(ReleaseLostMerge)); n == 0 {
			t.Fatalf("no lost-merge release was recorded")
		}
	})
	for _, r := range []*replica{p.A, p.B} {
		if n := r.countLogEvents(EventReleased, string(ReleaseLostMerge)); n == 0 {
			t.Fatalf("%s wrote no lost-merge release to its log", r.name)
		}
	}

	// Settling: a second exchange carries each side's repair to the other and
	// repairs nothing further, because the repair names the claim it undid.
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "claimed_by", "ben")
	})
}

// `released`, `claim_expired`, `heartbeat`: latest wins, and a heartbeat is
// replicated — an extension made on one machine is visible on the other.
func TestMergeRule_ReleasedAndHeartbeat(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "held") })

	// A short window, because a heartbeat sets a fresh DefaultClaimTTL rather
	// than adding to what is left: heartbeating a claim longer than the
	// default shortens it.
	p.A.do(func(db *sql.DB) {
		if err := RunClaim(db, task, "5m", "", "ben", false); err != nil {
			t.Fatalf("claim on A: %v", err)
		}
	})
	p.exchange()

	var beforeHeartbeat string
	p.B.do(func(db *sql.DB) {
		wantField(t, db, task, "claimed_by", "ben")
		beforeHeartbeat = taskField(t, db, task, "claim_expires_at")
	})

	// A extends the claim; B, which never touched it, sees the extension.
	p.tick()
	p.A.do(func(db *sql.DB) {
		if _, err := RunHeartbeat(db, []string{task}, "ben"); err != nil {
			t.Fatalf("heartbeat on A: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "claimed_by", "ben")
		if got := taskField(t, db, task, "claim_expires_at"); got <= beforeHeartbeat {
			t.Fatalf("claim_expires_at = %s, want later than %s — the heartbeat did not replicate", got, beforeHeartbeat)
		}
	})

	// And the latest transition still wins: B releases it.
	p.tick()
	p.B.do(func(db *sql.DB) {
		if err := RunRelease(db, task, "", "ben"); err != nil {
			t.Fatalf("release on B: %v", err)
		}
	})
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "claimed_by", "")
	})
}

// `labeled` / `unlabeled`: set membership, and the latest event for the pair
// wins.
func TestMergeRule_LabeledAndUnlabeled(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) {
		task = MustAdd(t, db, "", "labelled")
		if _, err := RunLabelAdd(db, task, []string{"alpha"}, TestActor); err != nil {
			t.Fatal(err)
		}
	})

	p.A.do(func(db *sql.DB) {
		if _, err := RunLabelAdd(db, task, []string{"beta"}, "ben"); err != nil {
			t.Fatalf("label on A: %v", err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if _, err := RunLabelRemove(db, task, []string{"alpha"}, "sam"); err != nil {
			t.Fatalf("unlabel on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		if got := labelsOf(t, db, task); !slices.Equal(got, []string{"beta"}) {
			t.Fatalf("labels = %v, want [beta]: the later unlabeled should have removed alpha", got)
		}
	})
}

// `blocked` / `unblocked`: the same set-membership rule, in both orders.
func TestMergeRule_BlockedAndUnblocked(t *testing.T) {
	t.Run("the later unblock wins", func(t *testing.T) {
		p := newPair(t)
		var blocked, blocker string
		p.seed(func(db *sql.DB) {
			blocked = MustAdd(t, db, "", "waiting")
			blocker = MustAdd(t, db, "", "in the way")
			if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
				t.Fatal(err)
			}
		})

		p.A.do(func(db *sql.DB) {
			if err := RunNote(db, blocked, "still waiting", nil, "ben"); err != nil {
				t.Fatal(err)
			}
		})
		p.tick()
		p.B.do(func(db *sql.DB) {
			if err := RunUnblock(db, blocked, blocker, "sam"); err != nil {
				t.Fatalf("unblock on B: %v", err)
			}
		})
		p.exchange()

		p.bothSides(func(t *testing.T, db *sql.DB) {
			if n := blockerCount(t, db, blocked); n != 0 {
				t.Fatalf("blockers = %d, want 0 after the later unblock", n)
			}
		})
	})

	t.Run("the later block wins", func(t *testing.T) {
		p := newPair(t)
		var blocked, blocker string
		p.seed(func(db *sql.DB) {
			blocked = MustAdd(t, db, "", "waiting")
			blocker = MustAdd(t, db, "", "in the way")
			if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
				t.Fatal(err)
			}
		})

		// A unblocks. B, which cannot see that, unblocks and blocks again —
		// so the last event for the pair is B's `blocked`.
		p.A.do(func(db *sql.DB) {
			if err := RunUnblock(db, blocked, blocker, "ben"); err != nil {
				t.Fatalf("unblock on A: %v", err)
			}
		})
		p.tick()
		p.B.do(func(db *sql.DB) {
			if err := RunUnblock(db, blocked, blocker, "sam"); err != nil {
				t.Fatalf("unblock on B: %v", err)
			}
		})
		p.tick()
		p.B.do(func(db *sql.DB) {
			if err := RunBlock(db, blocked, blocker, "sam"); err != nil {
				t.Fatalf("block on B: %v", err)
			}
		})
		p.exchange()

		p.bothSides(func(t *testing.T, db *sql.DB) {
			if n := blockerCount(t, db, blocked); n != 1 {
				t.Fatalf("blockers = %d, want 1 after the later block", n)
			}
		})
	})
}

// `moved` / `reparented`: both carry a parent and a sort key, the latest wins,
// and no sibling's key is touched by either.
func TestMergeRule_MovedAndReparented(t *testing.T) {
	p := newPair(t)
	var root, other, x, y string
	p.seed(func(db *sql.DB) {
		root = MustAdd(t, db, "", "the first root")
		other = MustAdd(t, db, "", "the second root")
		x = MustAdd(t, db, root, "X")
		y = MustAdd(t, db, root, "Y")
	})

	var siblingKey string
	p.A.do(func(db *sql.DB) {
		siblingKey = taskField(t, db, y, "sort_key")
		if err := RunMove(db, x, "before", y, "ben"); err != nil {
			t.Fatalf("move on A: %v", err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if err := RunReparent(db, x, other, "", "", "sam"); err != nil {
			t.Fatalf("reparent on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		var parent string
		if err := db.QueryRow(`SELECT COALESCE(p.short_id,'') FROM tasks t
			LEFT JOIN tasks p ON p.id = t.parent_id WHERE t.short_id = ?`, x).Scan(&parent); err != nil {
			t.Fatalf("read parent: %v", err)
		}
		if parent != other {
			t.Fatalf("X's parent = %q, want %q — the later reparent should win", parent, other)
		}
		if got := taskField(t, db, y, "sort_key"); got != siblingKey {
			t.Fatalf("Y's sort key changed from %q to %q; no writer may touch a sibling", siblingKey, got)
		}
	})
}

// `criteria_added` is idempotent by criterion short id; `criterion_state` is
// latest wins. One machine writes the criteria, the other passes one after
// pulling them.
func TestMergeRule_CriteriaAddedAndCriterionState(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "with criteria") })

	var crits []Criterion
	p.A.do(func(db *sql.DB) {
		var err error
		crits, err = RunAddCriteria(db, task, []Criterion{{Label: "the first"}, {Label: "the second"}}, "ben")
		if err != nil {
			t.Fatalf("criteria on A: %v", err)
		}
	})
	p.exchange()
	p.tick()

	p.B.do(func(db *sql.DB) {
		if _, err := RunSetCriterion(db, task, crits[0].ShortID, CriterionPassed, "sam"); err != nil {
			t.Fatalf("pass a criterion on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		rows, err := db.Query(`SELECT c.short_id, c.state FROM task_criteria c JOIN tasks t ON t.id = c.task_id
			WHERE t.short_id = ? ORDER BY c.sort_key`, task)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		states := map[string]string{}
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				t.Fatal(err)
			}
			states[id] = state
		}
		if len(states) != 2 {
			t.Fatalf("criteria = %v, want both and no duplicates", states)
		}
		if states[crits[0].ShortID] != string(CriterionPassed) {
			t.Fatalf("the criterion B passed reads %q here", states[crits[0].ShortID])
		}
		if states[crits[1].ShortID] != string(CriterionPending) {
			t.Fatalf("the untouched criterion reads %q here", states[crits[1].ShortID])
		}
	})
}

// `found_in_set`, `found_in_cleared` and `kind_changed`: latest wins.
func TestMergeRule_FoundInAndKind(t *testing.T) {
	p := newPair(t)
	var issue, sourceA, sourceB, kindy string
	p.seed(func(db *sql.DB) {
		issue = MustAdd(t, db, "", "the bug")
		sourceA = MustAdd(t, db, "", "found here first")
		sourceB = MustAdd(t, db, "", "found here later")
		// Seeded as an issue on both sides, because a kind_changed event is
		// only emitted when the kind actually changes: two replicas can only
		// disagree about the kind if they both start from the same one.
		kindy = MustAdd(t, db, "", "task or issue?")
		if _, err := RunSetKind(db, kindy, KindIssue, TestActor); err != nil {
			t.Fatal(err)
		}
	})

	p.A.do(func(db *sql.DB) {
		if err := RunSetFoundIn(db, issue, sourceA, "ben"); err != nil {
			t.Fatalf("found-in on A: %v", err)
		}
		if _, err := RunSetKind(db, kindy, KindTask, "ben"); err != nil {
			t.Fatalf("kind on A: %v", err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if err := RunSetFoundIn(db, issue, sourceB, "sam"); err != nil {
			t.Fatalf("found-in on B: %v", err)
		}
		// B goes to task and back again, so its latest kind_changed disagrees
		// with A's.
		if _, err := RunSetKind(db, kindy, KindTask, "sam"); err != nil {
			t.Fatalf("kind on B: %v", err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if _, err := RunSetKind(db, kindy, KindIssue, "sam"); err != nil {
			t.Fatalf("kind back on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		var source string
		if err := db.QueryRow(`SELECT s.short_id FROM found_in f
			JOIN tasks t ON t.id = f.task_id JOIN tasks s ON s.id = f.source_id
			WHERE t.short_id = ?`, issue).Scan(&source); err != nil {
			t.Fatalf("read found_in: %v", err)
		}
		if source != sourceB {
			t.Fatalf("found_in source = %q, want the later %q", source, sourceB)
		}
		wantField(t, db, kindy, "kind", string(KindIssue))
	})

	// Clearing it later wins over both.
	p.tick()
	p.A.do(func(db *sql.DB) {
		if err := RunClearFoundIn(db, issue, "ben"); err != nil {
			t.Fatalf("clear found-in on A: %v", err)
		}
	})
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM found_in f JOIN tasks t ON t.id = f.task_id
			WHERE t.short_id = ?`, issue).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("found_in rows = %d after the later clear", n)
		}
	})
}
