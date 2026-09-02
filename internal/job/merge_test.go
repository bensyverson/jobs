package job

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// mergeClock drives CurrentNowFunc so the two sides of a merge can be given
// deliberately different `updated_at` values; the real clock's one-second
// resolution would otherwise tie every edit made in the same test.
type mergeClock struct{ at time.Time }

func newMergeClock(t *testing.T) *mergeClock {
	t.Helper()
	orig := CurrentNowFunc
	c := &mergeClock{at: time.Unix(1_700_000_000, 0)}
	CurrentNowFunc = func() time.Time { return c.at }
	t.Cleanup(func() { CurrentNowFunc = orig })
	return c
}

func (c *mergeClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func mustOpenAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := CreateDB(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// copyDBFile duplicates a closed SQLite database, sidecars included, the way
// a human copying `.jobs.db` to another machine would.
func copyDBFile(t *testing.T, src, dst string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(src + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", src+suffix, err)
		}
		if err := os.WriteFile(dst+suffix, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", dst+suffix, err)
		}
	}
}

// divergedPair seeds one database, closes it, copies it, and hands both back
// open — the exact situation `job merge` exists for.
func divergedPair(t *testing.T, seed func(db *sql.DB)) (local, other *sql.DB, localPath, otherPath string) {
	t.Helper()
	dir := t.TempDir()
	localPath = filepath.Join(dir, "local.jobs.db")
	otherPath = filepath.Join(dir, "other.jobs.db")

	seedDB, err := CreateDB(localPath)
	if err != nil {
		t.Fatalf("create seed: %v", err)
	}
	if seed != nil {
		seed(seedDB)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}
	copyDBFile(t, localPath, otherPath)

	return mustOpenAt(t, localPath), mustOpenAt(t, otherPath), localPath, otherPath
}

func labelsOf(t *testing.T, db *sql.DB, shortID string) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT l.name FROM task_labels l
		JOIN tasks t ON t.id = l.task_id
		WHERE t.short_id = ? ORDER BY l.name`, shortID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func blockersOf(t *testing.T, db *sql.DB, blockedShortID string) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT blocker.short_id FROM blocks b
		JOIN tasks blocker ON blocker.id = b.blocker_id
		JOIN tasks blocked ON blocked.id = b.blocked_id
		WHERE blocked.short_id = ? ORDER BY blocker.short_id`, blockedShortID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func criteriaOf(t *testing.T, db *sql.DB, shortID string) map[string]string {
	t.Helper()
	rows, err := db.Query(`
		SELECT c.short_id, c.label, c.state FROM task_criteria c
		JOIN tasks t ON t.id = c.task_id
		WHERE t.short_id = ?`, shortID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sid sql.NullString
		var label, state string
		if err := rows.Scan(&sid, &label, &state); err != nil {
			t.Fatal(err)
		}
		out[sid.String] = label + "=" + state
	}
	return out
}

func foundInOf(t *testing.T, db *sql.DB, shortID string) string {
	t.Helper()
	var src string
	err := db.QueryRow(`
		SELECT source.short_id FROM found_in f
		JOIN tasks t ON t.id = f.task_id
		JOIN tasks source ON source.id = f.source_id
		WHERE t.short_id = ?`, shortID).Scan(&src)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// eventTuples renders every event as the identity tuple the merge dedups on,
// so a test can assert both presence and the absence of duplicates.
func eventTuples(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT COALESCE(t.short_id, ''), e.event_type, e.actor, COALESCE(e.detail, ''), e.created_at
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid, typ, actor, detail string
		var at int64
		if err := rows.Scan(&sid, &typ, &actor, &detail, &at); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s|%d", sid, typ, actor, detail, at))
	}
	return out
}

// logicalDump renders every merged table in a stable, row-id-free form. Two
// databases with the same dump hold the same content even when SQLite has
// rewritten their pages, which is what "unchanged" has to mean for a re-merge.
func logicalDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	queries := []struct{ name, query string }{
		{"tasks", `SELECT t.short_id, COALESCE(p.short_id,''), t.title, t.description, t.status,
			t.sort_key, COALESCE(t.claimed_by,''), COALESCE(t.claim_expires_at,0),
			COALESCE(t.completion_note,''), t.created_at, t.updated_at,
			COALESCE(t.deleted_at,0), t.kind
			FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id ORDER BY t.short_id`},
		{"labels", `SELECT t.short_id, l.name FROM task_labels l JOIN tasks t ON t.id = l.task_id
			ORDER BY t.short_id, l.name`},
		{"blocks", `SELECT br.short_id, bd.short_id FROM blocks b
			JOIN tasks br ON br.id = b.blocker_id JOIN tasks bd ON bd.id = b.blocked_id
			ORDER BY br.short_id, bd.short_id`},
		{"criteria", `SELECT t.short_id, COALESCE(c.short_id,''), c.label, c.state, c.sort_key,
			c.created_at, c.updated_at FROM task_criteria c JOIN tasks t ON t.id = c.task_id
			ORDER BY t.short_id, c.short_id, c.label`},
		{"found_in", `SELECT t.short_id, s.short_id FROM found_in f
			JOIN tasks t ON t.id = f.task_id JOIN tasks s ON s.id = f.source_id ORDER BY t.short_id`},
		{"users", `SELECT name FROM users ORDER BY name`},
	}
	for _, q := range queries {
		b.WriteString("== " + q.name + "\n")
		rows, err := db.Query(q.query)
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			cells := make([]any, len(cols))
			vals := make([]sql.NullString, len(cols))
			for i := range cells {
				cells[i] = &vals[i]
			}
			if err := rows.Scan(cells...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = v.String
			}
			b.WriteString(strings.Join(parts, "|") + "\n")
		}
		rows.Close()
	}
	tuples := eventTuples(t, db)
	sort.Strings(tuples)
	b.WriteString("== events\n" + strings.Join(tuples, "\n") + "\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// Two databases that were never one database have no common prefix, and
// merging them would interleave unrelated histories. Refuse.
func TestMerge_RefusesUnrelatedDatabases(t *testing.T) {
	newMergeClock(t)
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.db")
	bPath := filepath.Join(dir, "b.db")
	a := mustOpenAt(t, aPath)
	b := mustOpenAt(t, bPath)
	MustAdd(t, a, "", "Alpha")
	MustAdd(t, b, "", "Beta")

	if _, err := RunMerge(a, bPath, false); err == nil {
		t.Fatal("expected unrelated databases to be refused")
	} else if !strings.Contains(err.Error(), "unrelated") {
		t.Fatalf("error should name the problem, got: %v", err)
	}
	_ = b
}

// A task created only in the other database arrives whole: row, labels,
// blocks, criteria, provenance and events.
func TestMerge_TaskOnlyInOtherArrivesWhole(t *testing.T) {
	clock := newMergeClock(t)
	var seedID string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		seedID = MustAdd(t, db, "", "Shared root")
	})

	clock.advance(time.Minute)
	newID := MustAdd(t, other, "", "Only over there")
	if _, err := RunLabelAdd(other, newID, []string{"recovery", "store"}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunBlock(other, newID, seedID, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAddCriteria(other, newID, []Criterion{{Label: "It merges"}}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunSetFoundIn(other, newID, seedID, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunNote(other, newID, "a note from the other side", nil, TestActor); err != nil {
		t.Fatal(err)
	}

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	task, err := GetTaskByShortID(local, newID)
	if err != nil || task == nil {
		t.Fatalf("task %s did not arrive: %v", newID, err)
	}
	if task.Title != "Only over there" {
		t.Errorf("title = %q", task.Title)
	}
	if got := labelsOf(t, local, newID); len(got) != 2 || got[0] != "recovery" || got[1] != "store" {
		t.Errorf("labels = %v", got)
	}
	if got := blockersOf(t, local, newID); len(got) != 1 || got[0] != seedID {
		t.Errorf("blockers = %v, want [%s]", got, seedID)
	}
	if got := criteriaOf(t, local, newID); len(got) != 1 {
		t.Errorf("criteria = %v", got)
	}
	if got := foundInOf(t, local, newID); got != seedID {
		t.Errorf("found_in = %q, want %q", got, seedID)
	}
	notes := 0
	for _, tup := range eventTuples(t, local) {
		if strings.HasPrefix(tup, newID+"|noted|") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("note events for %s = %d, want 1", newID, notes)
	}

	if len(report.OnlyInOther) != 1 || report.OnlyInOther[0].ShortID != newID {
		t.Errorf("report.OnlyInOther = %+v", report.OnlyInOther)
	}
	if !report.Changed {
		t.Error("report should say something changed")
	}
}

// A task the local side alone created must survive the merge untouched and
// be named in the report.
func TestMerge_TaskOnlyInLocalSurvives(t *testing.T) {
	clock := newMergeClock(t)
	local, _, _, otherPath := divergedPair(t, func(db *sql.DB) {
		MustAdd(t, db, "", "Shared root")
	})

	clock.advance(time.Minute)
	mine := MustAdd(t, local, "", "Only over here")

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if task, _ := GetTaskByShortID(local, mine); task == nil {
		t.Fatal("local-only task was lost")
	}
	if len(report.OnlyInLocal) != 1 || report.OnlyInLocal[0].ShortID != mine {
		t.Errorf("report.OnlyInLocal = %+v", report.OnlyInLocal)
	}
}

// A task edited on both sides keeps the later edit and the union of labels
// and blocks.
func TestMerge_BothSidesEdited(t *testing.T) {
	clock := newMergeClock(t)
	var shared, other1 string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Original title")
		other1 = MustAdd(t, db, "", "Blocker candidate")
	})

	clock.advance(time.Minute)
	title := "Local edit"
	if err := RunEdit(local, shared, &title, nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelAdd(local, shared, []string{"cli"}, TestActor); err != nil {
		t.Fatal(err)
	}

	clock.advance(time.Minute)
	otherTitle := "Other edit, later"
	if err := RunEdit(other, shared, &otherTitle, nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelAdd(other, shared, []string{"store"}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunBlock(other, shared, other1, TestActor); err != nil {
		t.Fatal(err)
	}

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	task := MustGet(t, local, shared)
	if task.Title != otherTitle {
		t.Errorf("title = %q, want the later edit %q", task.Title, otherTitle)
	}
	got := labelsOf(t, local, shared)
	if len(got) != 2 || got[0] != "cli" || got[1] != "store" {
		t.Errorf("labels = %v, want the union [cli store]", got)
	}
	if b := blockersOf(t, local, shared); len(b) != 1 || b[0] != other1 {
		t.Errorf("blockers = %v", b)
	}

	var found *MergedTask
	for i := range report.Merged {
		if report.Merged[i].ShortID == shared {
			found = &report.Merged[i]
		}
	}
	if found == nil {
		t.Fatalf("report.Merged missing %s: %+v", shared, report.Merged)
	}
	if found.RowWinner != MergeSideOther {
		t.Errorf("RowWinner = %q, want other", found.RowWinner)
	}
	if len(found.LabelsAdded) != 1 || found.LabelsAdded[0] != "store" {
		t.Errorf("LabelsAdded = %v", found.LabelsAdded)
	}
}

// The local side's edit wins when it is the later one.
func TestMerge_LocalEditWinsWhenLater(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Original title")
	})

	clock.advance(time.Minute)
	otherTitle := "Other edit, earlier"
	if err := RunEdit(other, shared, &otherTitle, nil, TestActor); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	localTitle := "Local edit, later"
	if err := RunEdit(local, shared, &localTitle, nil, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := MustGet(t, local, shared).Title; got != localTitle {
		t.Errorf("title = %q, want %q", got, localTitle)
	}
}

// Notes from both sides survive and none is duplicated — including the notes
// that were already in the shared prefix.
func TestMerge_NotesUnionedWithoutDuplicates(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
		if err := RunNote(db, shared, "note before the copy", nil, TestActor); err != nil {
			t.Fatal(err)
		}
	})

	clock.advance(time.Minute)
	if err := RunNote(local, shared, "local note", nil, TestActor); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	if err := RunNote(other, shared, "other note", nil, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}

	seen := map[string]int{}
	for _, tup := range eventTuples(t, local) {
		if strings.Contains(tup, "|noted|") {
			seen[tup]++
		}
	}
	if len(seen) != 3 {
		t.Errorf("distinct note events = %d, want 3: %v", len(seen), seen)
	}
	for tup, n := range seen {
		if n != 1 {
			t.Errorf("note event duplicated %d times: %s", n, tup)
		}
	}
}

// A live claim on the other side survives when this side did not close the
// task.
func TestMerge_LiveClaimFromOtherSurvives(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
	})

	clock.advance(time.Minute)
	if err := RunClaim(other, shared, "30m", "", "agent-far", false); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	task := MustGet(t, local, shared)
	if task.ClaimedBy == nil || *task.ClaimedBy != "agent-far" {
		t.Errorf("claimed_by = %v, want agent-far", task.ClaimedBy)
	}
}

// The other side closing the task beats a live claim here, and the report
// says the claim was dropped.
func TestMerge_ClosedSideBeatsLiveClaim(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
	})

	clock.advance(time.Minute)
	if err := RunClaim(local, shared, "30m", "", "agent-here", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	MustDone(t, other, shared)

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	task := MustGet(t, local, shared)
	if task.Status != "done" {
		t.Errorf("status = %q, want done", task.Status)
	}
	if task.ClaimedBy != nil {
		t.Errorf("claim survived a close: %v", *task.ClaimedBy)
	}
	if len(report.DroppedClaims) != 1 || report.DroppedClaims[0].Actor != "agent-here" {
		t.Errorf("DroppedClaims = %+v", report.DroppedClaims)
	}
}

// Criteria match across databases by short id: the later edit wins, and a
// criterion added on one side only is copied.
func TestMerge_CriteriaMergeByShortID(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	var seeded []Criterion
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
		var err error
		seeded, err = RunAddCriteria(db, shared, []Criterion{{Label: "First"}}, TestActor)
		if err != nil {
			t.Fatal(err)
		}
	})

	clock.advance(time.Minute)
	if _, err := RunSetCriterion(other, shared, seeded[0].ShortID, CriterionPassed, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAddCriteria(other, shared, []Criterion{{Label: "Second"}}, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := criteriaOf(t, local, shared)
	if len(got) != 2 {
		t.Fatalf("criteria = %v, want 2", got)
	}
	if v := got[seeded[0].ShortID]; !strings.HasSuffix(v, "=passed") {
		t.Errorf("criterion %s = %q, want the later state", seeded[0].ShortID, v)
	}
}

// --dry-run prints the report and leaves both files byte-identical.
func TestMerge_DryRunWritesNothing(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, localPath, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
	})

	clock.advance(time.Minute)
	if err := RunNote(other, shared, "other note", nil, TestActor); err != nil {
		t.Fatal(err)
	}
	MustAdd(t, other, "", "Other only")

	// Quiesce both handles so the on-disk bytes are the whole truth.
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	beforeLocal := mustReadFile(t, localPath)
	beforeOther := mustReadFile(t, otherPath)

	report, err := RunMerge(local, otherPath, true)
	if err != nil {
		t.Fatalf("dry-run merge: %v", err)
	}
	if !report.DryRun {
		t.Error("report should say it was a dry run")
	}
	if !report.Changed {
		t.Error("dry run should still report the changes it would make")
	}
	if md := report.Markdown(); !strings.Contains(md, "dry run") {
		t.Errorf("markdown report should mention the dry run:\n%s", md)
	}

	if got := mustReadFile(t, otherPath); got != beforeOther {
		t.Error("dry run wrote to the other database")
	}
	if got := mustReadFile(t, localPath); got != beforeLocal {
		t.Error("dry run wrote to the local database")
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// Merging the same pair twice changes nothing.
func TestMerge_SecondMergeIsANoOp(t *testing.T) {
	clock := newMergeClock(t)
	var shared, blocker string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
		blocker = MustAdd(t, db, "", "Blocker")
	})

	clock.advance(time.Minute)
	localTitle := "Local edit"
	if err := RunEdit(local, shared, &localTitle, nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunNote(local, shared, "local note", nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelAdd(local, shared, []string{"cli"}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunClaim(local, blocker, "30m", "", "agent-here", false); err != nil {
		t.Fatal(err)
	}

	clock.advance(time.Minute)
	otherTitle := "Other edit"
	if err := RunEdit(other, shared, &otherTitle, nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunNote(other, shared, "other note", nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelAdd(other, shared, []string{"store"}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunBlock(other, shared, blocker, TestActor); err != nil {
		t.Fatal(err)
	}
	otherOnly := MustAdd(t, other, "", "Other only")
	if _, err := RunLabelAdd(other, otherOnly, []string{"recovery"}, TestActor); err != nil {
		t.Fatal(err)
	}
	MustDone(t, other, blocker)

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	afterFirst := logicalDump(t, local)

	second, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if second.Changed {
		t.Errorf("second merge reported changes:\n%s", second.Markdown())
	}
	if got := logicalDump(t, local); got != afterFirst {
		t.Errorf("second merge altered the database.\n--- after first ---\n%s\n--- after second ---\n%s", afterFirst, got)
	}
	if md := second.Markdown(); !strings.Contains(md, "Nothing changed") {
		t.Errorf("second report should say nothing changed:\n%s", md)
	}
}

// The report is machine-readable too, and JSON carries the same facts.
func TestMerge_ReportJSON(t *testing.T) {
	clock := newMergeClock(t)
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		MustAdd(t, db, "", "Shared")
	})
	clock.advance(time.Minute)
	newID := MustAdd(t, other, "", "Other only")

	report, err := RunMerge(local, otherPath, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), newID) {
		t.Errorf("JSON report should name %s:\n%s", newID, data)
	}
	if !strings.Contains(string(data), `"dry_run": true`) {
		t.Errorf("JSON report should carry dry_run:\n%s", data)
	}
}

// A missing file is an ordinary user error, not a panic.
func TestMerge_MissingFile(t *testing.T) {
	newMergeClock(t)
	local, _, _, _ := divergedPair(t, nil)
	if _, err := RunMerge(local, filepath.Join(t.TempDir(), "nope.db"), false); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// The claim survives even when the other side's row loses: a claim is a lease
// held by someone right now, not a field that the later edit gets to clear.
func TestMerge_ClaimSurvivesOnTheLosingSide(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
	})

	clock.advance(time.Minute)
	if err := RunClaim(other, shared, "30m", "", "agent-far", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	title := "Local edit, later"
	if err := RunEdit(local, shared, &title, nil, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	task := MustGet(t, local, shared)
	if task.Title != title {
		t.Errorf("title = %q, want the later local edit", task.Title)
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != "agent-far" {
		t.Errorf("claimed_by = %v, want agent-far to keep its lease", task.ClaimedBy)
	}
	if task.Status != "claimed" {
		t.Errorf("status = %q, want claimed", task.Status)
	}
}

// Only a *live* claim is carried onto a row that did not have one: an
// expired lease on the losing side is not resurrected.
func TestMerge_ExpiredClaimIsNotAdopted(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared")
	})

	clock.advance(time.Minute)
	if err := RunClaim(other, shared, "30m", "", "agent-far", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	title := "Local edit, long after the lease lapsed"
	if err := RunEdit(local, shared, &title, nil, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := MustGet(t, local, shared).ClaimedBy; got != nil {
		t.Errorf("claimed_by = %v, want no claim: the lease had expired", *got)
	}
}

// Criterion ids are unique per task (migration 0008), so two tasks may both
// hold criterion "abc". Merge must key criteria by task and id, and an
// update to one task's "abc" must leave the other task's alone.
func TestMerge_CriteriaAreKeyedByTaskAndShortID(t *testing.T) {
	clock := newMergeClock(t)
	var a, b string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		a = MustAdd(t, db, "", "A")
		b = MustAdd(t, db, "", "B")
		for _, sid := range []string{a, b} {
			task := MustGet(t, db, sid)
			now := CurrentNowFunc().Unix()
			if _, err := db.Exec(
				`INSERT INTO task_criteria (task_id, short_id, label, created_at, updated_at)
				 VALUES (?, 'abc', 'shared id', ?, ?)`, task.ID, now, now,
			); err != nil {
				t.Fatal(err)
			}
		}
	})

	clock.advance(time.Minute)
	if _, err := RunSetCriterion(other, b, "abc", CriterionPassed, TestActor); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := criteriaOf(t, local, a); got["abc"] != "shared id=pending" {
		t.Errorf("task A criteria = %v, want abc still pending", got)
	}
	if got := criteriaOf(t, local, b); got["abc"] != "shared id=passed" {
		t.Errorf("task B criteria = %v, want abc passed", got)
	}
	if got := criteriaOf(t, local, a); len(got) != 1 {
		t.Errorf("task A criteria = %v, want exactly one", got)
	}
}
