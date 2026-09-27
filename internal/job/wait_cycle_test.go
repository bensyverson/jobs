package job

import (
	"database/sql"
	"strings"
	"testing"
)

// A task waits on each of its blockers and, because a parent closes only when
// its last open child does, on each of its open children. A cycle in that
// graph is a permanent deadlock. Both `block add` and `import` refuse to write
// an edge that closes one, and name every task on it.

// assertImportRefused runs the import in both modes and checks that each
// fails with a circular-dependency error naming every fragment, having
// written nothing.
func assertImportRefused(t *testing.T, db *sql.DB, body, parent string, fragments ...string) {
	t.Helper()
	path := writeTempPlan(t, body)
	for _, dryRun := range []bool{true, false} {
		tasksBefore := countRows(t, db, "tasks")
		eventsBefore := countRows(t, db, "events")
		blocksBefore := countRows(t, db, "blocks")

		_, err := RunImport(db, path, parent, dryRun, "alice")
		if err == nil {
			t.Fatalf("dryRun=%v: expected a circular-dependency error, got nil", dryRun)
		}
		msg := err.Error()
		if !strings.Contains(msg, "circular dependency") {
			t.Errorf("dryRun=%v: error should say circular dependency: %q", dryRun, msg)
		}
		for _, f := range fragments {
			if !strings.Contains(msg, f) {
				t.Errorf("dryRun=%v: error should name %q: %q", dryRun, f, msg)
			}
		}
		if n := countRows(t, db, "tasks"); n != tasksBefore {
			t.Errorf("dryRun=%v: tasks %d → %d; a refused import writes nothing", dryRun, tasksBefore, n)
		}
		if n := countRows(t, db, "events"); n != eventsBefore {
			t.Errorf("dryRun=%v: events %d → %d; a refused import writes nothing", dryRun, eventsBefore, n)
		}
		if n := countRows(t, db, "blocks"); n != blocksBefore {
			t.Errorf("dryRun=%v: blocks %d → %d; a refused import writes nothing", dryRun, blocksBefore, n)
		}
	}
}

func TestImport_RefusesTwoTaskRefCycle(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Alpha\n" +
		"    ref: alpha\n" +
		"    blockedBy: [beta]\n" +
		"  - title: Beta\n" +
		"    ref: beta\n" +
		"    blockedBy: [alpha]\n" +
		"```\n"
	assertImportRefused(t, db, body, "",
		"tasks[0] (ref alpha) blocked by tasks[1] (ref beta)",
		"tasks[1] (ref beta) blocked by tasks[0] (ref alpha)",
	)
}

func TestImport_RefusesThreeTaskCycleByTitle(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Alpha\n" +
		"    blockedBy: [Gamma]\n" +
		"  - title: Beta\n" +
		"    blockedBy: [Alpha]\n" +
		"  - title: Gamma\n" +
		"    blockedBy: [Beta]\n" +
		"```\n"
	assertImportRefused(t, db, body, "",
		`tasks[0] "Alpha"`, `tasks[1] "Beta"`, `tasks[2] "Gamma"`,
	)
}

func TestImport_RefusesSelfBlock(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Alpha\n" +
		"    ref: alpha\n" +
		"    blockedBy: [alpha]\n" +
		"```\n"
	assertImportRefused(t, db, body, "", "tasks[0] (ref alpha) blocked by tasks[0] (ref alpha)")
}

func TestImport_RefusesLeafBlockedByItsAncestor(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Root\n" +
		"    ref: root\n" +
		"    children:\n" +
		"      - title: Middle\n" +
		"        children:\n" +
		"          - title: Leaf\n" +
		"            ref: leaf\n" +
		"            blockedBy: [root]\n" +
		"```\n"
	assertImportRefused(t, db, body, "",
		"tasks[0].children[0].children[0] (ref leaf) blocked by tasks[0] (ref root)",
		"tasks[0] (ref root) parent of tasks[0].children[0]",
	)
}

// A leaf blocked on a sibling subtree whose own leaf waits on the first leaf:
// the subtree cannot close until its child does, and its child waits on the
// leaf that waits on the subtree.
func TestImport_RefusesCycleThroughAnotherSubtree(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Alpha\n" +
		"    ref: alpha\n" +
		"    blockedBy: [group]\n" +
		"  - title: Group\n" +
		"    ref: group\n" +
		"    children:\n" +
		"      - title: Inner\n" +
		"        ref: inner\n" +
		"        blockedBy: [alpha]\n" +
		"```\n"
	assertImportRefused(t, db, body, "", "(ref alpha)", "(ref group)", "(ref inner)")
}

func TestImport_RefusesBlockOnParentFlagTarget(t *testing.T) {
	db := SetupTestDB(t)
	top := MustAdd(t, db, "", "Top")
	parent := MustAdd(t, db, top, "Parent")
	for _, ancestor := range []string{parent, top} {
		body := "```yaml\n" +
			"tasks:\n" +
			"  - title: Imported\n" +
			"    blockedBy: [" + ancestor + "]\n" +
			"```\n"
		assertImportRefused(t, db, body, parent, `tasks[0] "Imported" blocked by `+ancestor)
	}
}

// The plan task waits on an existing task that already waits on the --parent
// target, which cannot close until the plan task does.
func TestImport_RefusesCycleThroughExistingTasks(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	other := MustAdd(t, db, "", "Other")
	if err := RunBlock(db, other, parent, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Imported\n" +
		"    blockedBy: [" + other + "]\n" +
		"```\n"
	assertImportRefused(t, db, body, parent,
		`tasks[0] "Imported" blocked by `+other,
		other+" blocked by "+parent,
		parent+` parent of tasks[0] "Imported"`,
	)
}

// The mirror is not a deadlock: the parent waits on its child either way, and
// the edge drops when the child closes.
func TestImport_AllowsParentBlockedByItsOwnChild(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Root\n" +
		"    blockedBy: [leaf]\n" +
		"    children:\n" +
		"      - title: Leaf\n" +
		"        ref: leaf\n" +
		"```\n"
	if _, err := RunImport(db, writeTempPlan(t, body), "", false, "alice"); err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if n := countRows(t, db, "blocks"); n != 1 {
		t.Errorf("blocks = %d, want 1", n)
	}
}

func TestRunBlock_RefusesBlockOnOwnAncestor(t *testing.T) {
	db := SetupTestDB(t)
	top := MustAdd(t, db, "", "Top")
	mid := MustAdd(t, db, top, "Mid")
	leaf := MustAdd(t, db, mid, "Leaf")

	for _, ancestor := range []string{mid, top} {
		err := RunBlock(db, leaf, ancestor, TestActor)
		if err == nil {
			t.Fatalf("expected blocking %s on its ancestor %s to be refused", leaf, ancestor)
		}
		if !strings.Contains(err.Error(), "circular dependency") ||
			!strings.Contains(err.Error(), leaf+" blocked by "+ancestor) ||
			!strings.Contains(err.Error(), " parent of "+leaf) {
			t.Errorf("error should name the chain: %q", err)
		}
	}
	if n := countRows(t, db, "blocks"); n != 0 {
		t.Errorf("blocks = %d, want 0", n)
	}
}

func TestRunBlock_RefusesCycleThroughAnotherSubtree(t *testing.T) {
	db := SetupTestDB(t)
	alpha := MustAdd(t, db, "", "Alpha")
	group := MustAdd(t, db, "", "Group")
	inner := MustAdd(t, db, group, "Inner")
	if err := RunBlock(db, inner, alpha, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	err := RunBlock(db, alpha, group, TestActor)
	if err == nil {
		t.Fatal("expected the cycle through Group's child to be refused")
	}
	want := alpha + " blocked by " + group + ", " + group + " parent of " + inner + ", " + inner + " blocked by " + alpha
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestRunBlock_CycleErrorNamesTheChain(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	if err := RunBlock(db, a, b, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	err := RunBlock(db, b, a, TestActor)
	if err == nil {
		t.Fatal("expected cycle to be refused")
	}
	want := b + " blocked by " + a + ", " + a + " blocked by " + b
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// The mirror of the ancestor rule is legitimate, if redundant.
func TestRunBlock_AllowsAncestorBlockedByDescendant(t *testing.T) {
	db := SetupTestDB(t)
	top := MustAdd(t, db, "", "Top")
	leaf := MustAdd(t, db, top, "Leaf")
	if err := RunBlock(db, top, leaf, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
}

// A closed child no longer holds its parent open, so it is not a path.
func TestRunBlock_ClosedChildIsNotAPath(t *testing.T) {
	db := SetupTestDB(t)
	alpha := MustAdd(t, db, "", "Alpha")
	group := MustAdd(t, db, "", "Group")
	inner := MustAdd(t, db, group, "Inner")
	_ = MustAdd(t, db, group, "Keeps group open")
	if err := RunBlock(db, inner, alpha, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	if _, _, _, err := RunCancel(db, []string{inner}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}
	if err := RunBlock(db, alpha, group, TestActor); err != nil {
		t.Fatalf("RunBlock after the child closed: %v", err)
	}
}
