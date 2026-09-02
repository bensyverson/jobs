package job

import (
	"testing"
)

// The default identity and strict mode are machine-local: they live in
// .jobs/local.json beside the cache, not in the config table, which nothing
// reads or writes any more.

// --- Default identity ---

func TestConfig_GetDefaultIdentity_Empty(t *testing.T) {
	db := SetupTestDB(t)
	got, err := GetDefaultIdentity(db)
	if err != nil {
		t.Fatalf("GetDefaultIdentity: %v", err)
	}
	if got != "" {
		t.Errorf("fresh DB default identity = %q, want empty", got)
	}
}

func TestConfig_SetDefaultIdentity_Persists(t *testing.T) {
	db := SetupTestDB(t)
	if err := SetDefaultIdentity(db, "claude"); err != nil {
		t.Fatalf("SetDefaultIdentity: %v", err)
	}
	got, err := GetDefaultIdentity(db)
	if err != nil {
		t.Fatalf("GetDefaultIdentity: %v", err)
	}
	if got != "claude" {
		t.Errorf("default identity = %q, want %q", got, "claude")
	}
}

func TestConfig_SetDefaultIdentity_Overwrites(t *testing.T) {
	db := SetupTestDB(t)
	if err := SetDefaultIdentity(db, "alice"); err != nil {
		t.Fatalf("set alice: %v", err)
	}
	if err := SetDefaultIdentity(db, "bob"); err != nil {
		t.Fatalf("set bob: %v", err)
	}
	got, _ := GetDefaultIdentity(db)
	if got != "bob" {
		t.Errorf("default identity = %q, want bob (last write wins)", got)
	}
}

// --- Strict mode ---

func TestConfig_IsStrict_DefaultOff(t *testing.T) {
	db := SetupTestDB(t)
	strict, err := IsStrict(db)
	if err != nil {
		t.Fatalf("IsStrict: %v", err)
	}
	if strict {
		t.Errorf("fresh DB strict = true, want false (permissive default)")
	}
}

func TestConfig_SetStrict_Toggle(t *testing.T) {
	db := SetupTestDB(t)
	if err := SetStrict(db, true); err != nil {
		t.Fatalf("SetStrict(true): %v", err)
	}
	if strict, _ := IsStrict(db); !strict {
		t.Errorf("after SetStrict(true): IsStrict = false")
	}
	if err := SetStrict(db, false); err != nil {
		t.Fatalf("SetStrict(false): %v", err)
	}
	if strict, _ := IsStrict(db); strict {
		t.Errorf("after SetStrict(false): IsStrict = true")
	}
}

// --- Schema migration ---

// The table stays in the schema (there is no migration dropping it), but it
// is inert.
func TestConfig_Table_ExistsAfterOpen(t *testing.T) {
	db := SetupTestDB(t)
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='config'").Scan(&name)
	if err != nil {
		t.Fatalf("config table missing after migration: %v", err)
	}
}

// bH0 — the identity must not be written to the cache, or deleting the cache
// would lose it.
func TestConfig_Table_IsNeverWritten(t *testing.T) {
	db := SetupTestDB(t)
	if err := SetDefaultIdentity(db, "alice"); err != nil {
		t.Fatalf("SetDefaultIdentity: %v", err)
	}
	if err := SetStrict(db, true); err != nil {
		t.Fatalf("SetStrict: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM config").Scan(&n); err != nil {
		t.Fatalf("count config rows: %v", err)
	}
	if n != 0 {
		t.Errorf("config rows after setting the identity: got %d, want 0", n)
	}
}
