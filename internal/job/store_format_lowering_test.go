package job

import (
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// What can lower a file's declared store format.
//
// Binaries in the field read a file's format off its latest `replica` line
// that carries one, so a line declaring LESS than an earlier one reopens the
// hole the format closes: to them the file reads as the older format while
// holding types that format does not have. These tests hold every writer to
// never leaving such a line, and this binary to refusing a file that has one.

// A `replica` line with no format field — every line written before the field
// existed — declares nothing, so it cannot lower a declaration made before it.
// Binaries in the field read it the same way: fileStoreFormat has skipped an
// absent field since the format was introduced.
func TestAReplicaLineWithoutAFormatDoesNotLowerTheDeclaration(t *testing.T) {
	evs := []eventlog.Envelope{
		replicaLine(t, 1, StoreFormat),
		replicaLine(t, 2, 0),
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("declared format = %d, want %d: a line without the field declared less", got, StoreFormat)
	}
}

// A file whose latest declaration is lower than an earlier one was written to
// by a newer binary and then declared by an older one — a downgrade, since a
// binary only ever declares its own format. It holds whatever the newer binary
// wrote, so this binary refuses it on the highest format it ever declared.
func TestABinaryRefusesAFileThatEverDeclaredMoreThanItKnows(t *testing.T) {
	evs := []eventlog.Envelope{
		replicaLine(t, 1, StoreFormat+1),
		replicaLine(t, 2, StoreFormat),
	}
	err := checkStoreFormatFor(StoreFormat, "x.jsonl", evs)
	var ahead *StoreFormatAheadError
	if !errors.As(err, &ahead) {
		t.Fatalf("check = %v, want a StoreFormatAheadError", err)
	}
	if ahead.LogFormat != StoreFormat+1 {
		t.Fatalf("refusal names format %d, want %d", ahead.LogFormat, StoreFormat+1)
	}
}

// The file's declaration — what binaries in the field read, and what this
// binary re-declares against before it appends — is still the latest. A file
// whose latest line declares less than this binary writes owes a
// re-declaration even though it once declared the current format, because
// that is the only way the binaries in the field stop reading it as the older
// one.
func TestAFileDeclaredDownwardStillOwesARedeclaration(t *testing.T) {
	evs := []eventlog.Envelope{
		replicaLine(t, 1, StoreFormat),
		replicaLine(t, 2, StoreFormat-1),
	}
	if got := declarationOf(evs).Format; got != StoreFormat-1 {
		t.Fatalf("declared format = %d, want the latest, %d", got, StoreFormat-1)
	}
	if _, ok := owedReplicaEvent(declarationOf(evs), EventCreated, "", ""); !ok {
		t.Fatalf("a file whose latest line declares format %d owes no re-declaration", StoreFormat-1)
	}
}

// An unpositioned `replica` or `snapshot` row in a cache is another replica's
// bookkeeping, left there by a `job merge` from before merge stopped
// transcribing it. Adoption translates unpositioned rows into legacy lines,
// and a legacy `replica` line lands after the file's own declaration — where
// its format, and its label, would become the file's. Neither is history, so
// adoption carries neither into the log.
func TestAdoptionCarriesNoBookkeepingIntoTheLog(t *testing.T) {
	newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	path := legacyCache(t, dir, func(db *sql.DB) {
		MustAdd(t, db, "", "Alpha root")
		foreign, err := json.Marshal(ReplicaPayload{Label: "someone else", Format: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE events SET detail = ? WHERE event_type = ?",
			string(foreign), string(EventReplica)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events (event_type, actor, detail, created_at, ts)
			VALUES (?, 'adopt', '{"tasks":[]}', 1700000000, 1700000000000)`,
			string(EventSnapshot)); err != nil {
			t.Fatal(err)
		}
	})
	report, err := adopt(path)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	evs, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(path), report.Rep))
	if err != nil {
		t.Fatalf("read adopted file: %v", err)
	}
	for _, e := range evs {
		if e.Legacy && (EventType(e.Type) == EventReplica || EventType(e.Type) == EventSnapshot) {
			t.Errorf("the adopted file carries a legacy %q line: %s", e.Type, e.Data)
		}
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("the adopted file declares format %d, want %d", got, StoreFormat)
	}
	if got := declarationOf(evs).Label; got == "someone else" {
		t.Fatalf("the adopted file is labelled with another replica's name")
	}
	if report.Tasks != 1 {
		t.Fatalf("report = %+v, want one task", report)
	}
}

// Every `replica` payload this binary writes is built by newReplicaPayload,
// which stamps the current format; a literal assembled anywhere else could
// leave the field out, or carry an older value, and lower the file it lands
// in. A zero value (a "nothing owed" return) declares nothing and is allowed.
func TestOnlyNewReplicaPayloadBuildsAReplicaPayload(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == "newReplicaPayload" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || len(lit.Elts) == 0 {
					return true
				}
				if ident, ok := lit.Type.(*ast.Ident); ok && ident.Name == "ReplicaPayload" {
					t.Errorf("%s builds a ReplicaPayload outside newReplicaPayload", fset.Position(lit.Pos()))
				}
				return true
			})
		}
	}
}
