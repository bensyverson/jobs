package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/handlers"
)

// preview is zero-config: it lists and serves the component catalog with no
// database, so pointing --db at a path that does not exist must not create one.
func noStore(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "absent.db")
}

func TestPreview_ListJSONIsTheCatalogIndex(t *testing.T) {
	dbFile := noStore(t)
	stdout, _, err := runCLI(t, dbFile, "preview", "--list", "--format=json")
	if err != nil {
		t.Fatalf("preview --list --format=json: %v", err)
	}
	var got []handlers.PreviewListing
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	want := handlers.PreviewIndex()
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("listed %d components, want %d (non-zero)", len(got), len(want))
	}
	if got[0].Component != want[0].Component || len(got[0].States) != len(want[0].States) {
		t.Errorf("first listing = %+v, want %+v", got[0], want[0])
	}
	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Errorf("preview created a store at %s", dbFile)
	}
}

func TestPreview_ListTextNamesEveryStateURL(t *testing.T) {
	stdout, _, err := runCLI(t, noStore(t), "preview", "--list")
	if err != nil {
		t.Fatalf("preview --list: %v", err)
	}
	for _, c := range handlers.PreviewIndex() {
		for _, s := range c.States {
			if !strings.Contains(stdout, s.URL) {
				t.Errorf("text listing lacks %s:\n%s", s.URL, stdout)
			}
		}
	}
}

func TestPreview_RefusesAnUnknownFormat(t *testing.T) {
	if _, _, err := runCLI(t, noStore(t), "preview", "--list", "--format=xml"); err == nil {
		t.Error("preview --format=xml: want an error")
	}
}
