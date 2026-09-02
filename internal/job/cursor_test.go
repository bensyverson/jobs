package job

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// A rebuild renumbers events.id: a foreign event that sorts earlier than
// anything local shifts every later row id by one. Every cursor that
// addresses an event therefore has to be its log position, not its row id.

// dropForeignLog writes a one-event log file for a replica that is not this
// one, dated far enough back that its event sorts before every local event.
func dropForeignLog(t *testing.T, dir, rep, shortID string) {
	t.Helper()
	cache := filepath.Join(dir, ".jobs.db")
	payload, err := json.Marshal(CreatedPayload{ShortID: shortID, Title: "from elsewhere", SortKey: "aaaaaa"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	e := eventlog.Envelope{
		V: 1, Rep: rep, Seq: 1, TS: 1,
		Actor: "other", Type: eventlog.Type(EventCreated), Task: shortID, Data: payload,
	}
	line, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	logDir := eventlog.LogDir(eventlog.StoreDir(cache))
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", logDir, err)
	}
	path := filepath.Join(logDir, rep+eventlog.LogExt)
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func positionsOf(events []EventEntry) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Position().String()
	}
	return out
}

func shortIDsOf(events []EventEntry) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ShortID
	}
	return out
}

func TestEventsAfterPositionAreUnchangedByARebuildThatRenumbersRows(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)

	for _, title := range []string{"one", "two", "three"} {
		if _, err := RunAdd(db, "", title, "", "", nil, "ben"); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
	}

	all, err := GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d events, want 3", len(all))
	}
	cursor := all[0].Position()

	before, err := GetEventsAfterPosition(db, "", cursor)
	if err != nil {
		t.Fatalf("after position: %v", err)
	}
	if got := shortIDsOf(before); len(got) != 2 {
		t.Fatalf("before the rebuild: %v, want the two events after the cursor", got)
	}

	// A pull brings in another replica's file whose one event sorts before
	// everything local. Reopening rebuilds the cache and renumbers row ids.
	dropForeignLog(t, dir, "ZZZZZZ", "frgn01")
	db, sync := reopenStore(t, db, dir)
	if sync.State != StoreRebuilt {
		t.Fatalf("state = %q, want %q", sync.State, StoreRebuilt)
	}

	after, err := GetEventsAfterPosition(db, "", cursor)
	if err != nil {
		t.Fatalf("after position (post-rebuild): %v", err)
	}
	if got, want := positionsOf(after), positionsOf(before); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the cursor moved across the rebuild:\n got %v\nwant %v", got, want)
	}
	if got := shortIDsOf(after); len(got) != 2 {
		t.Errorf("post-rebuild the cursor returned %v — the tail replayed or skipped", got)
	}
}

func TestEventsAfterPositionOrdersAndPagesFromTheReturnedCursor(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	for _, title := range []string{"a", "b", "c", "d"} {
		if _, err := RunAdd(db, "", title, "", "", nil, "ben"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	var cursor eventlog.Position
	var seen []string
	for range 10 {
		batch, err := GetEventsAfterPosition(db, "", cursor)
		if err != nil {
			t.Fatalf("after position: %v", err)
		}
		if len(batch) == 0 {
			break
		}
		seen = append(seen, shortIDsOf(batch)...)
		cursor = batch[len(batch)-1].Position()
	}
	if len(seen) != 4 {
		t.Fatalf("polling from the returned cursor saw %d events, want 4 exactly once each: %v", len(seen), seen)
	}
}

func TestEventEntryPositionUsesTheRowIDForLegacyRows(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "positioned", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A legacy row is what adoption leaves behind: no replica, no seq, and
	// ts derived from created_at. Insert one directly.
	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks LIMIT 1`).Scan(&taskID); err != nil {
		t.Fatalf("task id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (task_id, event_type, actor, detail, created_at, ts, rep, seq)
		 VALUES (?, 'noted', 'ben', '{}', 1700000000, 1700000000000, '', 0)`, taskID); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	events, err := GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var legacy *EventEntry
	for i := range events {
		if events[i].EventType == "noted" {
			legacy = &events[i]
		}
	}
	if legacy == nil {
		t.Fatal("the legacy row is missing from the event list")
	}
	p := legacy.Position()
	if !p.Legacy() {
		t.Errorf("Position() = %+v, want a legacy position", p)
	}
	if p.Seq != uint64(legacy.ID) {
		t.Errorf("Position().Seq = %d, want the row id %d", p.Seq, legacy.ID)
	}

	// The legacy row is dated 2023, so it sorts before the created event
	// this test just wrote: a cursor at it must return that one event and
	// no more.
	after, err := GetEventsAfterPosition(db, "", p)
	if err != nil {
		t.Fatalf("after legacy position: %v", err)
	}
	if len(after) != 1 || after[0].EventType != "created" {
		t.Errorf("a cursor at the legacy row returned %v, want just the created event", shortIDsOf(after))
	}
}

func TestFormatEventLogJSONLinesCarriesThePosition(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "framed", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	events, err := GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var buf strings.Builder
	if err := FormatEventLogJSONLines(&buf, events); err != nil {
		t.Fatalf("format: %v", err)
	}
	var frame struct {
		ID       int64  `json:"id"`
		Position string `json:"position"`
	}
	line := strings.TrimSpace(buf.String())
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if frame.Position == "" {
		t.Fatalf("tail --format=json frame has no position: %s", line)
	}
	if frame.Position != events[0].Position().String() {
		t.Errorf("position = %q, want %q", frame.Position, events[0].Position().String())
	}
	if _, err := eventlog.ParsePosition(frame.Position); err != nil {
		t.Errorf("the frame's position does not parse: %v", err)
	}
}
