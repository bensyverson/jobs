package job

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// l5SEtr — a replica is a machine, and a machine deserves a name. Every log
// file opens with a `replica` event carrying the label, host, path and user of
// the checkout that owns it; `job replicas` lists them, `job replica rename`
// changes one, and the readers show a foreign replica's label beside its id.

// replicaStoreAt opens a cache in a fresh directory and hands back its path
// too, since these tests read the log files beside it.
func replicaStoreAt(t *testing.T, dir string) (*sql.DB, string) {
	t.Helper()
	cache := filepath.Join(dir, ".jobs.db")
	db, err := OpenDB(cache)
	if err != nil {
		t.Fatalf("open %s: %v", cache, err)
	}
	t.Cleanup(func() { db.Close() })
	return db, cache
}

// replicaEventsIn reads a store's log files and returns every `replica` line,
// in log order.
func replicaEventsIn(t *testing.T, cache string) []eventlog.Envelope {
	t.Helper()
	events, err := eventlog.ReadAll(eventlog.StoreDir(cache))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	eventlog.Sort(events)
	var out []eventlog.Envelope
	for _, e := range events {
		if EventType(e.Type) == EventReplica {
			out = append(out, e)
		}
	}
	return out
}

func decodeReplica(t *testing.T, e eventlog.Envelope) ReplicaPayload {
	t.Helper()
	var p ReplicaPayload
	if err := json.Unmarshal(e.Data, &p); err != nil {
		t.Fatalf("decode replica payload: %v", err)
	}
	return p
}

func TestReplicaEvent_IsTheFirstLineOfAFreshLog(t *testing.T) {
	quietNotices(t)
	dir := t.TempDir()
	db, cache := replicaStoreAt(t, dir)

	if _, err := RunAdd(db, "", "First task", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}

	events, err := eventlog.ReadAll(eventlog.StoreDir(cache))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	eventlog.Sort(events)
	if len(events) == 0 {
		t.Fatal("the log is empty after a write")
	}
	first := events[0]
	if EventType(first.Type) != EventReplica {
		t.Fatalf("first log line is %s, want %s", first.Type, EventReplica)
	}
	if first.Seq != 1 {
		t.Errorf("the replica event is seq %d, want 1", first.Seq)
	}

	p := decodeReplica(t, first)
	host, _ := os.Hostname()
	if p.Host == "" || p.Host != host {
		t.Errorf("host = %q, want %q", p.Host, host)
	}
	// SQLite reports the cache's real path, so the recorded checkout path is
	// symlink-resolved too (/var vs /private/var on macOS).
	wantPath, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	if p.Path != wantPath {
		t.Errorf("path = %q, want %q", p.Path, wantPath)
	}
	if u, err := user.Current(); err == nil && p.User != u.Username {
		t.Errorf("user = %q, want %q", p.User, u.Username)
	}
	if p.Label == "" {
		t.Error("label is empty; the default label is host plus checkout path")
	}
	if !strings.Contains(p.Label, filepath.Base(dir)) {
		t.Errorf("default label %q does not name the checkout directory", p.Label)
	}
}

func TestReplicaEvent_AppendedOnceToALogThatAlreadyHasLines(t *testing.T) {
	quietNotices(t)
	dir := t.TempDir()
	cache := filepath.Join(dir, ".jobs.db")

	// A log file whose first line is not a replica event: this repo's own
	// store, written before labels existed.
	rep, err := eventlog.NewReplicaID()
	if err != nil {
		t.Fatalf("mint replica id: %v", err)
	}
	state := &LocalState{Rep: rep}
	if err := state.Save(cache); err != nil {
		t.Fatalf("save local state: %v", err)
	}
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(cache), cache, rep)
	if err != nil {
		t.Fatalf("open appender: %v", err)
	}
	payload, err := json.Marshal(CreatedPayload{ShortID: "AbC12", Title: "Older", SortKey: "m"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	e := eventlog.Envelope{
		TS: CurrentNowFunc().UnixMilli(), Actor: "ben",
		Type: eventlog.Type(EventCreated), Task: "AbC12", Data: payload,
	}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		t.Fatalf("append: %v", err)
	}
	ap.Close()

	db, err := OpenDB(cache)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer db.Close()

	if err := RunNote(db, "AbC12", "one", nil, "ben"); err != nil {
		t.Fatalf("note: %v", err)
	}
	got := replicaEventsIn(t, cache)
	if len(got) != 1 {
		t.Fatalf("after the first write the log holds %d replica events, want 1", len(got))
	}
	if got[0].Rep != rep {
		t.Errorf("the replica event is for %s, want %s", got[0].Rep, rep)
	}
	if got[0].Seq != 2 {
		t.Errorf("the appended replica event is seq %d, want 2 (after the line already there)", got[0].Seq)
	}

	if err := RunNote(db, "AbC12", "two", nil, "ben"); err != nil {
		t.Fatalf("second note: %v", err)
	}
	if got := replicaEventsIn(t, cache); len(got) != 1 {
		t.Fatalf("a second write appended another replica event: %d in the log, want 1", len(got))
	}
}

func TestReplicaName_FromLocalStateIsHonouredWhenTheReplicaIsMinted(t *testing.T) {
	quietNotices(t)
	dir := t.TempDir()
	cache := filepath.Join(dir, ".jobs.db")
	if err := (&LocalState{ReplicaName: "ben-mbp"}).Save(cache); err != nil {
		t.Fatalf("save local state: %v", err)
	}
	db, err := OpenDB(cache)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer db.Close()
	if _, err := RunAdd(db, "", "First task", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := replicaEventsIn(t, cache)
	if len(got) != 1 {
		t.Fatalf("log holds %d replica events, want 1", len(got))
	}
	if p := decodeReplica(t, got[0]); p.Label != "ben-mbp" {
		t.Errorf("label = %q, want the name recorded at init", p.Label)
	}
}

func TestRunReplicaRename_AppendsANewEventAndTheLatestWins(t *testing.T) {
	quietNotices(t)
	dir := t.TempDir()
	db, cache := replicaStoreAt(t, dir)
	if _, err := RunAdd(db, "", "First task", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := RunReplicaRename(db, "the laptop", "ben")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if res.Label != "the laptop" {
		t.Errorf("rename reported %q, want %q", res.Label, "the laptop")
	}
	events := replicaEventsIn(t, cache)
	if len(events) != 2 {
		t.Fatalf("log holds %d replica events after a rename, want 2 — a rename is history", len(events))
	}
	if p := decodeReplica(t, events[1]); p.Label != "the laptop" {
		t.Errorf("the second replica event's label is %q, want %q", p.Label, "the laptop")
	}

	names, err := LoadReplicaNames(db)
	if err != nil {
		t.Fatalf("LoadReplicaNames: %v", err)
	}
	if got := names.Labels[res.Rep]; got != "the laptop" {
		t.Errorf("the latest label for %s is %q, want %q", res.Rep, got, "the laptop")
	}

	list, err := RunReplicas(db)
	if err != nil {
		t.Fatalf("RunReplicas: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("RunReplicas returned %d rows, want 1", len(list))
	}
	if list[0].Label != "the laptop" {
		t.Errorf("listing shows label %q, want %q", list[0].Label, "the laptop")
	}
	if !list[0].IsLocal {
		t.Error("the only replica in a single-checkout store is not marked as this checkout")
	}
}

func TestRunReplicas_ListsBothSidesOfATwoStoreExchange(t *testing.T) {
	p := newPair(t)
	p.seed(func(db *sql.DB) {
		if _, err := RunAdd(db, "", "Shared root", "", "", nil, "ben"); err != nil {
			t.Fatalf("add: %v", err)
		}
	})
	p.B.do(func(db *sql.DB) {
		if err := RunNote(db, firstShortID(t, db), "from B", nil, "sam"); err != nil {
			t.Fatalf("note on B: %v", err)
		}
	})
	p.tick()
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		list, err := RunReplicas(db)
		if err != nil {
			t.Fatalf("RunReplicas: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("RunReplicas returned %d replicas, want 2", len(list))
		}
		locals := 0
		for _, r := range list {
			if r.Label == "" {
				t.Errorf("replica %s has no label", r.Rep)
			}
			if r.Host == "" {
				t.Errorf("replica %s has no host", r.Rep)
			}
			if r.Path == "" {
				t.Errorf("replica %s has no path", r.Rep)
			}
			if r.Events == 0 {
				t.Errorf("replica %s has no events", r.Rep)
			}
			if r.LastEvent == 0 {
				t.Errorf("replica %s has no last-event time", r.Rep)
			}
			if r.IsLocal {
				locals++
			}
		}
		if locals != 1 {
			t.Errorf("%d replicas marked as this checkout, want exactly 1", locals)
		}
	})
}

// firstShortID is the only task in a freshly seeded store.
func firstShortID(t *testing.T, db *sql.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRow("SELECT short_id FROM tasks LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("read a task: %v", err)
	}
	return id
}

func TestRenderStatus_ShowsTheReplicaLabelBesideTheID(t *testing.T) {
	s := &StatusSummary{
		Store: &StoreStatus{Rep: "arMAXc", Label: "ben-mbp:~/git/Jobs", Files: 1, Events: 3, State: StoreInSync},
	}
	var buf bytes.Buffer
	RenderStatus(&buf, s)
	want := `Store: replica arMAXc "ben-mbp:~/git/Jobs" · 1 log file, 3 events · cache in sync`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("status does not carry the labelled store line.\n got: %s\nwant: %s", buf.String(), want)
	}
}

func TestRenderEventLogMarkdown_NamesOnlyForeignReplicas(t *testing.T) {
	names := ReplicaNames{Local: "aaaaaa", Labels: map[string]string{
		"aaaaaa": "mine:~/here",
		"bbbbbb": "theirs:~/there",
	}}
	events := []EventEntry{
		{ShortID: "AbC12", EventType: "noted", Actor: "ben", Detail: `{"text":"local"}`, Rep: "aaaaaa"},
		{ShortID: "AbC12", EventType: "noted", Actor: "sam", Detail: `{"text":"foreign"}`, Rep: "bbbbbb"},
		{ShortID: "AbC12", EventType: "noted", Actor: "kim", Detail: `{"text":"unlabelled"}`, Rep: "cccccc"},
	}
	var buf bytes.Buffer
	RenderEventLogMarkdown(&buf, events, names)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered %d lines, want 3", len(lines))
	}
	if strings.Contains(lines[0], "mine:~/here") {
		t.Errorf("a local event names its own replica: %s", lines[0])
	}
	if !strings.Contains(lines[1], "theirs:~/there") {
		t.Errorf("a foreign event does not carry its replica's label: %s", lines[1])
	}
	if !strings.Contains(lines[2], "cccccc") {
		t.Errorf("a foreign event with no label does not fall back to the replica id: %s", lines[2])
	}
}
