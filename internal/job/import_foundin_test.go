package job

import (
	"strings"
	"testing"
)

// 9s2qL — `foundIn: <ref|title|short-id>` in the import grammar. One value,
// resolved exactly as one blockedBy entry is, recorded after both tasks exist.

func TestImport_FoundInByRef(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Ship v1\n" +
		"    children:\n" +
		"      - title: Wire it into the router\n" +
		"        ref: router\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"    children:\n" +
		"      - title: Router drops the trailing slash\n" +
		"        foundIn: router\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	source := res.Tasks[1].ID // Wire it into the router
	bug := res.Tasks[3].ID    // Router drops the trailing slash

	got, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if got == nil || got.ShortID != source {
		t.Fatalf("found-in source = %v, want %s", got, source)
	}
	if res.Tasks[3].FoundIn != source {
		t.Errorf("echo found_in = %q, want %s", res.Tasks[3].FoundIn, source)
	}

	// The mirror reads back from the source.
	surfaced, err := GetSurfaced(db, source)
	if err != nil {
		t.Fatalf("GetSurfaced: %v", err)
	}
	if len(surfaced) != 1 || surfaced[0].ShortID != bug {
		t.Errorf("surfaced = %v, want [%s]", surfaced, bug)
	}
}

// A forward reference: the foundIn target appears later in the document than
// the task that names it, so it only resolves after both rows exist.
func TestImport_FoundInForwardReference(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"    children:\n" +
		"      - title: Router drops the trailing slash\n" +
		"        foundIn: router\n" +
		"  - title: Ship v1\n" +
		"    children:\n" +
		"      - title: Wire it into the router\n" +
		"        ref: router\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	got, err := GetFoundInSource(db, res.Tasks[1].ID)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if got == nil || got.ShortID != res.Tasks[3].ID {
		t.Fatalf("found-in source = %v, want %s", got, res.Tasks[3].ID)
	}
}

func TestImport_FoundInByTitle(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Wire it into the router\n" +
		"  - title: Router drops the trailing slash\n" +
		"    foundIn: Wire it into the router\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	got, err := GetFoundInSource(db, res.Tasks[1].ID)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if got == nil || got.ShortID != res.Tasks[0].ID {
		t.Fatalf("found-in source = %v, want %s", got, res.Tasks[0].ID)
	}
}

func TestImport_FoundInByExistingShortID(t *testing.T) {
	db := SetupTestDB(t)
	existing := MustAdd(t, db, "", "Wire it into the router")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Router drops the trailing slash\n" +
		"    foundIn: " + existing + "\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	got, err := GetFoundInSource(db, res.Tasks[0].ID)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if got == nil || got.ShortID != existing {
		t.Fatalf("found-in source = %v, want %s", got, existing)
	}
}

func TestImport_FoundInAmbiguousTitle_Errors(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Wire it up\n" +
		"  - title: Wire it up\n" +
		"  - title: Router drops the trailing slash\n" +
		"    foundIn: Wire it up\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	if !strings.Contains(err.Error(), "tasks[2]") || !strings.Contains(err.Error(), "foundIn") {
		t.Errorf("error should name the row and the key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "matches multiple tasks") {
		t.Errorf("error should read like the blockedBy ambiguity error, got: %v", err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("failed import wrote tasks: %d vs %d", got, before)
	}
}

func TestImport_FoundInUnresolvable_Errors(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Router drops the trailing slash\n" +
		"    foundIn: nope\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected an unresolvable-reference error")
	}
	if !strings.Contains(err.Error(), "tasks[0]") || !strings.Contains(err.Error(), "foundIn") {
		t.Errorf("error should name the row and the key, got: %v", err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("failed import wrote tasks: %d vs %d", got, before)
	}
}

func TestImport_FoundInSelf_Errors(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Router drops the trailing slash\n" +
		"    ref: bug\n" +
		"    foundIn: bug\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected a self-reference error")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("error should name the row, got: %v", err)
	}
	if !strings.Contains(err.Error(), "itself") {
		t.Errorf("error should say a task cannot be found in itself, got: %v", err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("failed import wrote tasks: %d vs %d", got, before)
	}
}

func TestImport_FoundInDryRun(t *testing.T) {
	db := SetupTestDB(t)
	existing := MustAdd(t, db, "", "Pre-existing leaf")
	before := countRows(t, db, "found_in")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Wire it into the router\n" +
		"    ref: router\n" +
		"  - title: Router drops the trailing slash\n" +
		"    foundIn: router\n" +
		"  - title: Another bug\n" +
		"    foundIn: " + existing + "\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", true, "alice")
	if err != nil {
		t.Fatalf("RunImport dry: %v", err)
	}
	if res.Tasks[1].FoundIn != "<new-1>" {
		t.Errorf("dry-run found_in = %q, want <new-1>", res.Tasks[1].FoundIn)
	}
	if res.Tasks[2].FoundIn != existing {
		t.Errorf("dry-run found_in = %q, want %s", res.Tasks[2].FoundIn, existing)
	}
	if got := countRows(t, db, "found_in"); got != before {
		t.Errorf("dry-run wrote found_in rows: %d vs %d", got, before)
	}
}
