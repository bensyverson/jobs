package job

import (
	"strings"
	"testing"
)

// W19Ae — Warn on ambiguous or lossy `job import` block selection. `job import`
// silently picks the first yaml/yml/unlabeled fence carrying a top-level
// `tasks:` key and silently drops keys outside the grammar. These tests make
// both observable via ImportResult.Warnings, without blocking a valid import.

func warningsJoined(ws []string) string { return strings.Join(ws, "\n") }

// 4Z0 — more than one `tasks:` block warns and names the block used.
func TestImport_MultipleTasksBlocks_Warns(t *testing.T) {
	db := SetupTestDB(t)
	body := "# Plan\n\n" +
		"An illustration of the output shape:\n\n" +
		"```yaml\n" +
		"tasks:\n" +
		"  - title: Decoy from illustration\n" +
		"```\n\n" +
		"The actual plan:\n\n" +
		"```yaml\n" +
		"tasks:\n" +
		"  - title: Real task\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	// Selection is unchanged: the FIRST block wins (that's the foot-gun the
	// warning surfaces).
	if len(res.Tasks) != 1 || res.Tasks[0].Title != "Decoy from illustration" {
		t.Fatalf("expected the first block to be imported, got %+v", res.Tasks)
	}
	w := warningsJoined(res.Warnings)
	if !strings.Contains(w, "found 2") || !strings.Contains(w, "tasks:") {
		t.Errorf("warning should report 2 candidate `tasks:` blocks: %q", w)
	}
	if !strings.Contains(w, "line") {
		t.Errorf("warning should name the chosen block by line: %q", w)
	}
	if !strings.Contains(w, "ignoring") {
		t.Errorf("warning should say the others are ignored: %q", w)
	}
}

// YOw — a block with keys outside the grammar warns those keys were ignored.
func TestImport_NonGrammarKeys_Warns(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"orient:\n" +
		"  target: abc12\n" +
		"tasks:\n" +
		"  - title: Real task\n" +
		"    status: available\n" +
		"    children:\n" +
		"      - title: Child\n" +
		"        notes: [a note]\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	// The valid tasks still import.
	if len(res.Tasks) != 2 {
		t.Fatalf("expected 2 imported tasks, got %d", len(res.Tasks))
	}
	w := warningsJoined(res.Warnings)
	if !strings.Contains(w, "import grammar") {
		t.Errorf("warning should mention the import grammar: %q", w)
	}
	for _, key := range []string{"orient", "status", "notes"} {
		if !strings.Contains(w, key) {
			t.Errorf("warning should name ignored key %q: %q", key, w)
		}
	}
}

// 9ff (a) — warnings appear under --dry-run and never block.
func TestImport_Warnings_UnderDryRun(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"version: 3\n" +
		"tasks:\n" +
		"  - title: Real task\n" +
		"```\n" +
		"```yaml\n" +
		"tasks:\n" +
		"  - title: Second block\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", true, "alice")
	if err != nil {
		t.Fatalf("dry-run RunImport: %v", err)
	}
	if !res.DryRun {
		t.Error("expected DryRun true")
	}
	if len(res.Warnings) < 2 {
		t.Errorf("expected both warnings (multi-block + non-grammar key) under dry-run, got %v", res.Warnings)
	}
	// Dry-run wrote nothing.
	if n := countRows(t, db, "tasks"); n != 0 {
		t.Errorf("dry-run must not write; found %d task rows", n)
	}
}

// 9ff (b) — warnings never block an otherwise valid import.
func TestImport_Warnings_DoNotBlockRealImport(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"orient: ignore-me\n" +
		"tasks:\n" +
		"  - title: Real task\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport must not fail on a valid plan with extra keys: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a non-grammar-key warning")
	}
	if n := countRows(t, db, "tasks"); n != 1 {
		t.Errorf("expected the valid task to import; found %d rows", n)
	}
}

// Regression: a clean single-block plan with only grammar keys emits NO warnings.
func TestImport_CleanPlan_NoWarnings(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Clean task\n" +
		"    desc: nothing odd here\n" +
		"    labels: [a]\n" +
		"    children:\n" +
		"      - title: Clean child\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("clean plan should emit no warnings, got %v", res.Warnings)
	}
}
