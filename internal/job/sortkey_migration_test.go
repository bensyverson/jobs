package job

import (
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/bensyverson/jobs/internal/migrations"
)

// legacyMigrationsFS is the migration set as it stood before sort keys —
// everything up to and including 0008. Tests use it to build a database that
// still carries integer sort_order columns, which is what the backfill has
// to convert.
func legacyMigrationsFS(t *testing.T) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	names, err := fs.Glob(migrations.FS(), "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	for _, name := range names {
		v, err := parseMigrationVersion(name)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if v > 8 {
			continue
		}
		data, err := fs.ReadFile(migrations.FS(), name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = &fstest.MapFile{Data: data}
	}
	return out
}

// orderingDump renders one line per task as "<parent>/<task>" in the order
// the given ORDER BY expression produces, so two dumps can be compared byte
// for byte.
func orderingDump(t *testing.T, db *sql.DB, orderBy string) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT COALESCE(p.short_id, ''), t.short_id
		FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id
		ORDER BY ` + orderBy)
	if err != nil {
		t.Fatalf("ordering dump (%s): %v", orderBy, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var parent, short string
		if err := rows.Scan(&parent, &short); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, parent+"/"+short)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func criteriaDump(t *testing.T, db *sql.DB, orderBy string) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT t.short_id, c.label
		FROM task_criteria c JOIN tasks t ON t.id = c.task_id
		ORDER BY ` + orderBy)
	if err != nil {
		t.Fatalf("criteria dump (%s): %v", orderBy, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var task, label string
		if err := rows.Scan(&task, &label); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, task+"/"+label)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}

func TestSortKeyBackfill_DerivesKeysFromSortOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := RunMigrations(raw, legacyMigrationsFS(t)); err != nil {
		t.Fatalf("legacy migrations: %v", err)
	}
	if !hasColumn(t, raw, "tasks", "sort_order") {
		t.Fatalf("test premise broken: legacy schema has no tasks.sort_order")
	}

	// Two roots, each with children whose sort_order is deliberately not the
	// insertion order, so a backfill that ignored sort_order would be caught.
	type seed struct {
		short  string
		parent any
		order  int
	}
	seeds := []seed{
		{"root-b", nil, 7},
		{"root-a", nil, 2},
		{"kid-3", "root-a", 30},
		{"kid-1", "root-a", 10},
		{"kid-2", "root-a", 20},
		{"solo", "root-b", 0},
	}
	ids := map[string]int64{}
	for _, s := range seeds {
		var parentID any
		if s.parent != nil {
			parentID = ids[s.parent.(string)]
		}
		var id int64
		err := raw.QueryRow(`
			INSERT INTO tasks (short_id, parent_id, title, description, status, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, '', 'available', ?, 1, 1) RETURNING id`,
			s.short, parentID, s.short, s.order).Scan(&id)
		if err != nil {
			t.Fatalf("seed %s: %v", s.short, err)
		}
		ids[s.short] = id
	}
	for i, label := range []string{"third", "first", "second"} {
		order := []int{50, 10, 20}[i]
		if _, err := raw.Exec(`
			INSERT INTO task_criteria (task_id, label, state, sort_order, created_at, updated_at)
			VALUES (?, ?, 'pending', ?, 1, 1)`, ids["kid-1"], label, order); err != nil {
			t.Fatalf("seed criterion %s: %v", label, err)
		}
	}

	before := orderingDump(t, raw, "t.parent_id, t.sort_order, t.id")
	beforeCriteria := criteriaDump(t, raw, "c.task_id, c.sort_order, c.id")
	raw.Close()

	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB (migrating): %v", err)
	}
	defer db.Close()

	after := orderingDump(t, db, "t.parent_id, t.sort_key, t.id")
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("task ordering changed across the migration:\nbefore %v\nafter  %v", before, after)
	}
	afterCriteria := criteriaDump(t, db, "c.task_id, c.sort_key, c.id")
	if fmt.Sprint(beforeCriteria) != fmt.Sprint(afterCriteria) {
		t.Fatalf("criteria ordering changed across the migration:\nbefore %v\nafter  %v", beforeCriteria, afterCriteria)
	}

	var blanks int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE sort_key = ''").Scan(&blanks); err != nil {
		t.Fatalf("count blank task keys: %v", err)
	}
	if blanks != 0 {
		t.Fatalf("%d tasks still carry an empty sort_key", blanks)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM task_criteria WHERE sort_key = ''").Scan(&blanks); err != nil {
		t.Fatalf("count blank criterion keys: %v", err)
	}
	if blanks != 0 {
		t.Fatalf("%d criteria still carry an empty sort_key", blanks)
	}

	if hasColumn(t, db, "tasks", "sort_order") {
		t.Errorf("tasks.sort_order survived the migration")
	}
	if hasColumn(t, db, "task_criteria", "sort_order") {
		t.Errorf("task_criteria.sort_order survived the migration")
	}

	// Re-opening a migrated database must be a no-op, not a second backfill.
	db.Close()
	db2, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB (second time): %v", err)
	}
	defer db2.Close()
	again := orderingDump(t, db2, "t.parent_id, t.sort_key, t.id")
	if fmt.Sprint(after) != fmt.Sprint(again) {
		t.Fatalf("ordering changed on re-open:\nfirst  %v\nsecond %v", after, again)
	}
}

// realDatabasePath is this repo's own tracker database. The migration has to
// preserve its listing order exactly; nothing else in the suite exercises a
// tree this large or this irregular.
const realDatabasePath = "/Users/ben/git/Jobs/.jobs.db"

func TestSortKeyMigration_RealDatabaseOrderingUnchanged(t *testing.T) {
	if _, err := os.Stat(realDatabasePath); err != nil {
		t.Skipf("real database not present at %s: %v", realDatabasePath, err)
	}
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "copy.jobs.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := realDatabasePath + suffix
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFileForTest(src, copyPath+suffix); err != nil {
			t.Fatalf("copy %s: %v", src, err)
		}
	}

	raw, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	if !hasColumn(t, raw, "tasks", "sort_order") {
		raw.Close()
		t.Skip("the real database has already been migrated to sort keys")
	}
	before := orderingDump(t, raw, "t.parent_id, t.sort_order, t.id")
	beforeCriteria := criteriaDump(t, raw, "c.task_id, c.sort_order, c.id")
	raw.Close()
	if len(before) == 0 {
		t.Fatal("real database dump is empty")
	}

	db, err := OpenDB(copyPath)
	if err != nil {
		t.Fatalf("OpenDB (migrating the copy): %v", err)
	}
	defer db.Close()

	after := orderingDump(t, db, "t.parent_id, t.sort_key, t.id")
	if len(before) != len(after) {
		t.Fatalf("task count changed: %d before, %d after", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("task ordering diverged at row %d: %q before, %q after", i, before[i], after[i])
		}
	}
	afterCriteria := criteriaDump(t, db, "c.task_id, c.sort_key, c.id")
	if len(beforeCriteria) != len(afterCriteria) {
		t.Fatalf("criterion count changed: %d before, %d after", len(beforeCriteria), len(afterCriteria))
	}
	for i := range beforeCriteria {
		if beforeCriteria[i] != afterCriteria[i] {
			t.Fatalf("criteria ordering diverged at row %d: %q before, %q after", i, beforeCriteria[i], afterCriteria[i])
		}
	}
	t.Logf("verified %d tasks and %d criteria from %s", len(after), len(afterCriteria), realDatabasePath)
}

func copyFileForTest(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
