package job

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// importedEventRow is one `imported` event as the cache holds it.
type importedEventRow struct {
	taskShort string
	actor     string
	payload   ImportedPayload
}

func importedEvents(t *testing.T, db *sql.DB) []importedEventRow {
	t.Helper()
	rows, err := db.Query(`SELECT t.short_id, e.actor, COALESCE(e.detail, '')
		FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE e.event_type = ? ORDER BY e.id`, string(EventImported))
	if err != nil {
		t.Fatalf("query imported events: %v", err)
	}
	defer rows.Close()
	var out []importedEventRow
	for rows.Next() {
		var r importedEventRow
		var detail string
		if err := rows.Scan(&r.taskShort, &r.actor, &detail); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(detail), &r.payload); err != nil {
			t.Fatalf("decode imported payload %q: %v", detail, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

const twoRootPlan = "```yaml\n" +
	"tasks:\n" +
	"  - title: Ship v1\n" +
	"    children:\n" +
	"      - title: Write tests\n" +
	"      - title: Fix CI\n" +
	"        children:\n" +
	"          - title: Pin the runner\n" +
	"  - title: Ship v2\n" +
	"```\n"

// A forest-root import records one imported event per top-level task, none on
// the children, with the source's base name and the subtree's counts.
func TestImport_RecordsImportedEventPerTopLevelTask(t *testing.T) {
	db := SetupTestDB(t)
	dir := filepath.Join(t.TempDir(), "plans", "q3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "roadmap.md")
	if err := os.WriteFile(path, []byte(twoRootPlan), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	shipV1, shipV2 := res.Tasks[0].ID, res.Tasks[4].ID

	got := importedEvents(t, db)
	if len(got) != 2 {
		t.Fatalf("imported events = %d (%+v), want 2 — one per top-level task", len(got), got)
	}
	want := []importedEventRow{
		{taskShort: shipV1, actor: "alice", payload: ImportedPayload{Source: "roadmap.md", Tasks: 4, Leaves: 2}},
		{taskShort: shipV2, actor: "alice", payload: ImportedPayload{Source: "roadmap.md", Tasks: 1, Leaves: 1}},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("imported[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A --parent import records the event on each imported top-level task, naming
// the parent, and never on the --parent target itself.
func TestImport_ParentRecordsImportedOnTopLevelTasksNotTheParent(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Existing root")
	path := writeTempPlan(t, twoRootPlan)

	res, err := RunImport(db, path, parent, false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	got := importedEvents(t, db)
	if len(got) != 2 {
		t.Fatalf("imported events = %d (%+v), want 2", len(got), got)
	}
	for _, e := range got {
		if e.taskShort == parent {
			t.Errorf("imported event recorded on the --parent target %s", parent)
		}
		if e.payload.ParentID != parent {
			t.Errorf("imported on %s: parent_id = %q, want %q", e.taskShort, e.payload.ParentID, parent)
		}
		if e.payload.Source != "plan.md" {
			t.Errorf("imported on %s: source = %q, want plan.md", e.taskShort, e.payload.Source)
		}
	}
	if got[0].taskShort != res.Tasks[0].ID || got[1].taskShort != res.Tasks[4].ID {
		t.Errorf("imported events on %s, %s; want %s, %s",
			got[0].taskShort, got[1].taskShort, res.Tasks[0].ID, res.Tasks[4].ID)
	}
}

// The parent is omitted from the payload of a forest-root import, not
// written as an empty string.
func TestImportedPayload_OmitsParentAtForestRoot(t *testing.T) {
	b, err := json.Marshal(ImportedPayload{Source: "plan.md", Tasks: 1, Leaves: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"source":"plan.md","tasks":1,"leaves":1}`; got != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
}

// The imported event is part of the log, so a rebuild of the cache from the
// log reproduces it exactly.
func TestImport_ImportedEventSurvivesRebuild(t *testing.T) {
	source := SetupTestDB(t)
	parent := MustAdd(t, source, "", "Existing root")
	if _, err := RunImport(source, writeTempPlan(t, twoRootPlan), "", false, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := RunImport(source, writeTempPlan(t, twoRootPlan), parent, false, "alice"); err != nil {
		t.Fatal(err)
	}

	events, err := cachedEnvelopes(source)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := CreateDB(filepath.Join(t.TempDir(), "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if err := rebuildFrom(rebuilt, events); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if n := len(importedEvents(t, rebuilt)); n != 4 {
		t.Errorf("rebuilt cache holds %d imported events, want 4", n)
	}
	if want, got := applyDump(t, source), applyDump(t, rebuilt); want != got {
		t.Errorf("rebuild differs.\n--- original ---\n%s\n--- rebuilt ---\n%s", want, got)
	}
}

func TestFormatEventDescription_Imported(t *testing.T) {
	cases := []struct {
		name, detail, want string
	}{
		{"forest root", `{"source":"roadmap.md","tasks":4,"leaves":2}`, "imported from roadmap.md (4 tasks, 2 leaves)"},
		{"nested", `{"source":"plan.md","parent_id":"AbC12","tasks":3,"leaves":2}`, "imported from plan.md under AbC12 (3 tasks, 2 leaves)"},
		{"singular", `{"source":"plan.md","tasks":1,"leaves":1}`, "imported from plan.md (1 task, 1 leaf)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatEventDescription(string(EventImported), tc.detail); got != tc.want {
				t.Errorf("FormatEventDescription = %q, want %q", got, tc.want)
			}
		})
	}
}
