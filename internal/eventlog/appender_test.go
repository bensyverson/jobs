package eventlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newStore returns a cache path and its store directory in a fresh temp dir.
func newStore(t *testing.T) (cache, store string) {
	t.Helper()
	cache = filepath.Join(t.TempDir(), ".jobs.db")
	return cache, StoreDir(cache)
}

func event(actor string, n int) *Envelope {
	return &Envelope{
		TS:    int64(1000 + n),
		Actor: actor,
		Type:  "noted",
		Task:  "VBF5u",
		Data:  json.RawMessage(fmt.Sprintf(`{"note":"%s-%d"}`, actor, n)),
	}
}

func TestAppendAssignsGaplessSeqFromOne(t *testing.T) {
	cache, store := newStore(t)
	a, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatalf("OpenAppender: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	batch := []*Envelope{event("ben", 1), event("ben", 2)}
	if err := a.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}
	for i, e := range batch {
		if e.Seq != uint64(i+1) {
			t.Errorf("event %d got seq %d, want %d", i, e.Seq, i+1)
		}
		if e.Rep != "k7Qx2m" {
			t.Errorf("event %d got rep %q", i, e.Rep)
		}
		if e.V != Version {
			t.Errorf("event %d got v %d, want %d", i, e.V, Version)
		}
	}

	more := []*Envelope{event("ben", 3)}
	if err := a.Append(more); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if more[0].Seq != 3 {
		t.Errorf("continuation got seq %d, want 3", more[0].Seq)
	}
}

func TestAppendIsANoOpForAnEmptyBatch(t *testing.T) {
	cache, store := newStore(t)
	a, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Append(nil); err != nil {
		t.Fatalf("Append(nil): %v", err)
	}
	if _, err := os.Stat(LogPath(store, "k7Qx2m")); err == nil {
		if got, _ := a.LastSeq(); got != 0 {
			t.Errorf("LastSeq = %d after an empty batch, want 0", got)
		}
	}
}

func TestASecondAppenderContinuesTheSeq(t *testing.T) {
	cache, store := newStore(t)
	first, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// Both were opened against an empty file; the second must still see the
	// first's writes rather than a seq cached at open.
	e1 := event("ben", 1)
	if err := first.Append([]*Envelope{e1}); err != nil {
		t.Fatal(err)
	}
	e2 := event("agent", 2)
	if err := second.Append([]*Envelope{e2}); err != nil {
		t.Fatal(err)
	}
	if e2.Seq != 2 {
		t.Fatalf("second appender assigned seq %d, want 2", e2.Seq)
	}
}

func TestAppenderReopensOntoAnExistingFile(t *testing.T) {
	cache, store := newStore(t)
	a, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Append([]*Envelope{event("ben", 1), event("ben", 2)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, err := b.LastSeq(); err != nil || got != 2 {
		t.Fatalf("LastSeq = %d, %v; want 2, nil", got, err)
	}
	e := event("ben", 3)
	if err := b.Append([]*Envelope{e}); err != nil {
		t.Fatal(err)
	}
	if e.Seq != 3 {
		t.Errorf("seq after reopen = %d, want 3", e.Seq)
	}
}

func TestConcurrentAppendsAreGaplessAndNeverInterleave(t *testing.T) {
	cache, store := newStore(t)

	const appenders = 4
	const batches = 15
	const perBatch = 3

	appendersList := make([]*Appender, appenders)
	for i := range appendersList {
		a, err := OpenAppender(store, cache, "k7Qx2m")
		if err != nil {
			t.Fatalf("OpenAppender: %v", err)
		}
		t.Cleanup(func() { _ = a.Close() })
		appendersList[i] = a
	}

	var wg sync.WaitGroup
	for i, a := range appendersList {
		for b := range batches {
			wg.Go(func() {
				batch := make([]*Envelope, perBatch)
				for k := range batch {
					batch[k] = event(fmt.Sprintf("a%d", i), b*perBatch+k)
				}
				if err := a.Append(batch); err != nil {
					t.Errorf("Append: %v", err)
				}
			})
		}
	}
	wg.Wait()

	raw, err := os.ReadFile(LogPath(store, "k7Qx2m"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("log does not end in a newline")
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := appenders * batches * perBatch
	if len(lines) != want {
		t.Fatalf("got %d lines, want %d", len(lines), want)
	}
	for i, line := range lines {
		e, err := Unmarshal([]byte(line))
		if err != nil {
			t.Fatalf("line %d does not parse (lines interleaved?): %v\n%q", i+1, err, line)
		}
		if e.Seq != uint64(i+1) {
			t.Fatalf("line %d has seq %d, want %d", i+1, e.Seq, i+1)
		}
	}
}

func TestAppendLockedWorksUnderACallerHeldLock(t *testing.T) {
	cache, store := newStore(t)
	a, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	l, err := AcquireLock(cache)
	if err != nil {
		t.Fatal(err)
	}
	e := event("ben", 1)
	if err := a.AppendLocked([]*Envelope{e}); err != nil {
		t.Fatalf("AppendLocked: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if e.Seq != 1 {
		t.Errorf("seq = %d, want 1", e.Seq)
	}

	evs, err := ReadAll(store)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
}

func TestAppendRefusesATruncatedTail(t *testing.T) {
	cache, store := newStore(t)
	a, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Append([]*Envelope{event("ben", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	path := LogPath(store, "k7Qx2m")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte(`{"v":1,"rep":"k7Qx`)...), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := OpenAppender(store, cache, "k7Qx2m")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Append([]*Envelope{event("ben", 2)}); err == nil {
		t.Fatal("Append onto a truncated tail: want an error, got none")
	}
}

func TestOpenAppenderRejectsABadReplicaID(t *testing.T) {
	cache, store := newStore(t)
	for _, rep := range []string{"", "../escape", "toolongid"} {
		if _, err := OpenAppender(store, cache, rep); err == nil {
			t.Errorf("OpenAppender(%q): want error, got none", rep)
		}
	}
}
