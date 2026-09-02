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
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The store format, end to end.
//
// A log file declares the format it was written at in the `replica` event that
// opens it. An older binary meeting a newer file must refuse rather than apply
// the types it does not know as no-ops — the silent case the format exists to
// close.

// forgeReplicaFile writes a brand new replica file into r's store whose first
// line is a `replica` event declaring format. A format of 0 leaves the field
// out entirely, which is what every file written before the format existed
// looks like.
func (r *replica) forgeReplicaFile(format StoreFormatVersion) string {
	r.t.Helper()
	rep, err := eventlog.NewReplicaID()
	if err != nil {
		r.t.Fatalf("mint replica id: %v", err)
	}
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(r.cache()), r.cache(), rep)
	if err != nil {
		r.t.Fatalf("open appender for %s: %v", rep, err)
	}
	defer ap.Close()
	payload, err := json.Marshal(ReplicaPayload{Label: "forged", Format: format})
	if err != nil {
		r.t.Fatalf("marshal: %v", err)
	}
	e := eventlog.Envelope{
		TS:    CurrentNowFunc().UnixMilli(),
		Actor: "sam",
		Type:  eventlog.Type(EventReplica),
		Data:  payload,
	}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		r.t.Fatalf("append: %v", err)
	}
	return rep
}

// newReplicaWithWork is one store with a task in it, ready for a foreign file
// to be dropped beside its own.
func newReplicaWithWork(t *testing.T) *replica {
	t.Helper()
	quietNotices(t)
	r := &replica{t: t, name: "A", dir: t.TempDir()}
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "Local work") })
	return r
}

// (a) A file from the future is refused, by name and by both numbers.
func TestRebuildRefusesALogFileAheadOfTheBinary(t *testing.T) {
	r := newReplicaWithWork(t)
	rep := r.forgeReplicaFile(StoreFormat + 1)

	err := r.tryOpen()
	if err == nil {
		t.Fatal("opening a store holding a file from the future succeeded; it must refuse")
	}
	var ahead *StoreFormatAheadError
	if !errors.As(err, &ahead) {
		t.Fatalf("error is %T (%v), want *StoreFormatAheadError", err, err)
	}
	if ahead.LogFormat != StoreFormat+1 || ahead.BinaryFormat != StoreFormat {
		t.Fatalf("error carries log %d / binary %d, want %d / %d",
			ahead.LogFormat, ahead.BinaryFormat, StoreFormat+1, StoreFormat)
	}
	msg := err.Error()
	for _, want := range []string{
		rep + ".jsonl",
		strconv.Itoa(int(StoreFormat + 1)),
		strconv.Itoa(int(StoreFormat)),
		"older than the log",
		"make install",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not mention %q", msg, want)
		}
	}
}

// (b) A file with no format field at all — every file written before this
// landed — reads as format 1 and rebuilds.
func TestRebuildAcceptsALogFileWithNoFormatField(t *testing.T) {
	r := newReplicaWithWork(t)
	rep := r.forgeReplicaFile(0)

	line := firstLine(t, filepath.Join(eventlog.LogDir(eventlog.StoreDir(r.cache())), rep+".jsonl"))
	if strings.Contains(line, "format") {
		t.Fatalf("the forged file declares a format: %s", line)
	}
	if err := r.tryOpen(); err != nil {
		t.Fatalf("a file with no format field must rebuild: %v", err)
	}
}

// (c) A file at exactly this binary's format rebuilds.
func TestRebuildAcceptsALogFileAtTheCurrentFormat(t *testing.T) {
	r := newReplicaWithWork(t)
	r.forgeReplicaFile(StoreFormat)
	if err := r.tryOpen(); err != nil {
		t.Fatalf("a file at the current format must rebuild: %v", err)
	}
}

// A replica this binary writes declares the current format.
func TestOwnLogFileDeclaresTheCurrentFormat(t *testing.T) {
	r := newReplicaWithWork(t)
	events := r.logEvents()
	found := false
	for _, e := range events {
		if EventType(e.Type) != EventReplica {
			continue
		}
		var p ReplicaPayload
		if err := json.Unmarshal(e.Data, &p); err != nil {
			t.Fatalf("decode replica payload: %v", err)
		}
		if p.Format != StoreFormat {
			t.Fatalf("our own replica event declares format %d, want %d", p.Format, StoreFormat)
		}
		found = true
	}
	if !found {
		t.Fatal("no replica event in our own log file")
	}
}

// The latest replica event wins, so a rename at the current format after an
// earlier line from the future still refuses: the file's declared format is
// the newest one it carries.
func TestFileFormatTakesTheLatestReplicaEvent(t *testing.T) {
	evs := []eventlog.Envelope{
		replicaLine(t, 1, StoreFormat),
		replicaLine(t, 2, StoreFormat+3),
	}
	if got := fileStoreFormat(evs); got != StoreFormat+3 {
		t.Fatalf("declared format = %d, want %d", got, StoreFormat+3)
	}
	// And with no replica event at all, format 1.
	if got := fileStoreFormat(nil); got != 1 {
		t.Fatalf("a file with no replica event = %d, want 1", got)
	}
}

// `job replicas` says what format each replica writes at, which is where a
// reader goes after a refusal to see who is ahead.
func TestReplicasReportTheStoreFormat(t *testing.T) {
	r := newReplicaWithWork(t)
	db, err := OpenDB(r.cache())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	list, err := RunReplicas(db)
	if err != nil {
		t.Fatalf("RunReplicas: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("RunReplicas returned %d rows, want 1", len(list))
	}
	if list[0].Format != StoreFormat {
		t.Fatalf("replica reports format %d, want %d", list[0].Format, StoreFormat)
	}
	var out strings.Builder
	RenderReplicas(&out, list)
	if want := "format " + strconv.Itoa(int(StoreFormat)); !strings.Contains(out.String(), want) {
		t.Fatalf("the listing does not mention %q:\n%s", want, out.String())
	}
}

// firstLine reads the first line of a log file — the `replica` event.
func firstLine(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return line
}

func replicaLine(t *testing.T, seq uint64, format StoreFormatVersion) eventlog.Envelope {
	t.Helper()
	data, err := json.Marshal(ReplicaPayload{Label: "x", Format: format})
	if err != nil {
		t.Fatal(err)
	}
	return eventlog.Envelope{Seq: seq, Type: eventlog.Type(EventReplica), Data: data}
}

// (d) The golden vocabulary.
//
// The event types are read out of the source of event_payloads.go rather than
// from a second hand-written list, so the diff that adds a constant is the
// diff this test sees. Adding a type therefore fails here until it is listed
// under a NEW key in storeFormatAdded — and the highest key must be
// StoreFormat, so the constant is bumped in the same diff.
func TestStoreFormatCoversEveryEventType(t *testing.T) {
	declared := map[EventType]StoreFormatVersion{}
	for format, types := range storeFormatAdded {
		if format < 1 || format > StoreFormat {
			t.Fatalf("storeFormatAdded has a key %d outside 1..%d", format, StoreFormat)
		}
		for _, ty := range types {
			if prior, dup := declared[ty]; dup {
				t.Fatalf("%q is listed under format %d and %d", ty, prior, format)
			}
			declared[ty] = format
		}
	}
	for f := StoreFormatVersion(1); f <= StoreFormat; f++ {
		if _, ok := storeFormatAdded[f]; !ok {
			t.Fatalf("storeFormatAdded has no entry for format %d; every format needs one, empty if it added no type", f)
		}
	}
	if _, ok := storeFormatAdded[StoreFormat]; !ok {
		t.Fatalf("storeFormatAdded has no entry for the current format %d", StoreFormat)
	}

	live := eventTypesInSource(t)
	var missing, unknown []string
	for _, ty := range live {
		if _, ok := declared[ty]; !ok {
			missing = append(missing, string(ty))
		}
	}
	for ty := range declared {
		if !slices.Contains(live, ty) {
			unknown = append(unknown, string(ty))
		}
	}
	slices.Sort(missing)
	slices.Sort(unknown)
	if len(missing) > 0 {
		t.Fatalf("event types %v are not in storeFormatAdded: bump StoreFormat and list them under the new format", missing)
	}
	if len(unknown) > 0 {
		t.Fatalf("storeFormatAdded lists %v, which are not event type constants any more", unknown)
	}

	// applyTable can only name types the vocabulary declares.
	for ty := range applyTable {
		if _, ok := declared[ty]; !ok {
			t.Fatalf("applyTable handles %q, which is not in storeFormatAdded", ty)
		}
	}
}

// eventTypesInSource reads every `X EventType = "y"` constant out of
// event_payloads.go. Go cannot enumerate a const block at run time, and a
// hand-copied list would be exactly the second place this test exists to
// avoid.
func eventTypesInSource(t *testing.T) []EventType {
	t.Helper()
	const src = "event_payloads.go"
	file, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	var out []EventType
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "EventType" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				out = append(out, EventType(value))
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("found no EventType constants in %s", src)
	}
	return out
}
