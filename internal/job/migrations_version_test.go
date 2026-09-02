package job

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/migrations"
)

// highestShippedVersion reports the largest version among the embedded
// migrations, so these tests never hardcode a number that goes stale the next
// time a migration lands.
func highestShippedVersion(t *testing.T) int {
	t.Helper()
	names, err := fs.Glob(migrations.FS(), "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded migrations")
	}
	highest := 0
	for _, name := range names {
		v, err := parseMigrationVersion(name)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if v > highest {
			highest = v
		}
	}
	return highest
}

func TestRunMigrations_RefusesDatabaseAheadOfBinary(t *testing.T) {
	db := openFreshSqlite(t)
	if err := RunMigrations(db, migrations.FS()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	shipped := highestShippedVersion(t)
	future := shipped + 1
	if _, err := db.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)", future,
	); err != nil {
		t.Fatalf("seed future migration: %v", err)
	}

	err := RunMigrations(db, migrations.FS())
	if err == nil {
		t.Fatal("expected RunMigrations to refuse a database ahead of the binary")
	}
	var ahead *SchemaAheadError
	if !errors.As(err, &ahead) {
		t.Fatalf("expected *SchemaAheadError, got %T: %v", err, err)
	}
	if ahead.DBVersion != future {
		t.Errorf("DBVersion = %d, want %d", ahead.DBVersion, future)
	}
	if ahead.BinaryVersion != shipped {
		t.Errorf("BinaryVersion = %d, want %d", ahead.BinaryVersion, shipped)
	}
	msg := err.Error()
	for _, want := range []string{
		"older than the database",
		"make install",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if !strings.Contains(msg, "schema") {
		t.Errorf("error %q does not name the schema versions", msg)
	}
}

func TestRunMigrations_DatabaseAtBinaryVersionIsAccepted(t *testing.T) {
	db := openFreshSqlite(t)
	if err := RunMigrations(db, migrations.FS()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := RunMigrations(db, migrations.FS()); err != nil {
		t.Fatalf("second RunMigrations on an up-to-date database: %v", err)
	}
}

func TestOpenDB_RefusesCacheAheadOfBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".jobs.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	future := highestShippedVersion(t) + 1
	if _, err := db.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)", future,
	); err != nil {
		t.Fatalf("seed future migration: %v", err)
	}
	db.Close()

	_, err = OpenDB(path)
	if _, ok := errors.AsType[*SchemaAheadError](err); !ok {
		t.Fatalf("expected *SchemaAheadError from OpenDB, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the cache path %q", err.Error(), path)
	}
}
