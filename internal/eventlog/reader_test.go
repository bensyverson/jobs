package eventlog

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog writes raw lines verbatim as rep's log file.
func writeLog(t *testing.T, store, rep string, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(LogDir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	path := LogPath(store, rep)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(t *testing.T, rep string, seq uint64, ts int64) string {
	t.Helper()
	b, err := Marshal(Envelope{V: Version, Rep: rep, Seq: seq, TS: ts, Actor: "ben", Type: "noted", Task: "VBF5u"})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

func TestReadAllReturnsTheUnionSortedByPosition(t *testing.T) {
	_, store := newStore(t)
	writeLog(t, store, "aaaaaa", []string{
		line(t, "aaaaaa", 1, 10),
		line(t, "aaaaaa", 2, 30),
		line(t, "aaaaaa", 3, 50),
	})
	writeLog(t, store, "bbbbbb", []string{
		line(t, "bbbbbb", 1, 20),
		line(t, "bbbbbb", 2, 30),
		line(t, "bbbbbb", 3, 40),
	})

	evs, err := ReadAll(store)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	got := make([]Position, len(evs))
	for i, e := range evs {
		got[i] = e.Position()
	}
	want := []Position{
		{TS: 10, Rep: "aaaaaa", Seq: 1},
		{TS: 20, Rep: "bbbbbb", Seq: 1},
		{TS: 30, Rep: "aaaaaa", Seq: 2},
		{TS: 30, Rep: "bbbbbb", Seq: 2},
		{TS: 40, Rep: "bbbbbb", Seq: 3},
		{TS: 50, Rep: "aaaaaa", Seq: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestUnionOfShuffledFilesSortsIdentically(t *testing.T) {
	_, store := newStore(t)
	reps := []string{"aaaaaa", "bbbbbb", "k7Qx2m", "Zp09aQ"}
	rng := rand.New(rand.NewSource(11))
	for _, rep := range reps {
		var lines []string
		ts := int64(1)
		for seq := uint64(1); seq <= 20; seq++ {
			ts += int64(rng.Intn(5)) // deliberately collides across replicas
			lines = append(lines, line(t, rep, seq, ts))
		}
		writeLog(t, store, rep, lines)
	}

	first, err := ReadAll(store)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	// Reading again in a store whose directory entries are the same must give
	// the identical order; so must sorting a shuffled copy of the union.
	for range 20 {
		shuffled := make([]Envelope, len(first))
		copy(shuffled, first)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		Sort(shuffled)
		for i := range first {
			if shuffled[i].Position() != first[i].Position() {
				t.Fatalf("shuffled union differs at %d: %+v vs %+v", i, shuffled[i].Position(), first[i].Position())
			}
		}
	}
}

func TestReadAllOfAnEmptyStoreIsEmpty(t *testing.T) {
	_, store := newStore(t)
	evs, err := ReadAll(store)
	if err != nil {
		t.Fatalf("ReadAll of a store with no log dir: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("got %d events, want 0", len(evs))
	}
}

func TestMalformedLineIsReportedWithFileAndLineNumber(t *testing.T) {
	_, store := newStore(t)
	path := writeLog(t, store, "aaaaaa", []string{
		line(t, "aaaaaa", 1, 10),
		line(t, "aaaaaa", 2, 20),
		`{"v":1,"rep":"aaaaaa","seq":3,`,
	})

	_, err := ReadAll(store)
	if err == nil {
		t.Fatal("want a parse error, got none")
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want *ParseError, got %T: %v", err, err)
	}
	if pe.Path != path {
		t.Errorf("Path = %q, want %q", pe.Path, path)
	}
	if pe.Line != 3 {
		t.Errorf("Line = %d, want 3", pe.Line)
	}
	if !strings.Contains(pe.Error(), path) || !strings.Contains(pe.Error(), "3") {
		t.Errorf("message %q does not name the file and line", pe.Error())
	}
}

func TestTruncatedFinalLineIsReported(t *testing.T) {
	_, store := newStore(t)
	path := LogPath(store, "aaaaaa")
	if err := os.MkdirAll(LogDir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	body := line(t, "aaaaaa", 1, 10) + "\n" + line(t, "aaaaaa", 2, 20)[:20] // no newline, half a line
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadAll(store)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want *ParseError, got %T: %v", err, err)
	}
	if pe.Line != 2 || pe.Path != path {
		t.Errorf("got %s:%d, want %s:2", pe.Path, pe.Line, path)
	}
}

func TestACompleteFinalLineWithoutANewlineIsStillTruncated(t *testing.T) {
	_, store := newStore(t)
	path := LogPath(store, "aaaaaa")
	if err := os.MkdirAll(LogDir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	body := line(t, "aaaaaa", 1, 10) + "\n" + line(t, "aaaaaa", 2, 20)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var pe *ParseError
	if _, err := ReadAll(store); !errors.As(err, &pe) {
		t.Fatalf("a file not ending in a newline must be reported; got %v", err)
	} else if pe.Line != 2 {
		t.Errorf("Line = %d, want 2", pe.Line)
	}
}

func TestSeqGapIsReportedWithFileAndExpectedSeq(t *testing.T) {
	_, store := newStore(t)
	path := writeLog(t, store, "aaaaaa", []string{
		line(t, "aaaaaa", 1, 10),
		line(t, "aaaaaa", 2, 20),
		line(t, "aaaaaa", 4, 30),
	})

	_, err := ReadAll(store)
	var ge *SeqGapError
	if !errors.As(err, &ge) {
		t.Fatalf("want *SeqGapError, got %T: %v", err, err)
	}
	if ge.Path != path {
		t.Errorf("Path = %q, want %q", ge.Path, path)
	}
	if ge.Expected != 3 || ge.Got != 4 {
		t.Errorf("got expected=%d got=%d, want expected=3 got=4", ge.Expected, ge.Got)
	}
	if !strings.Contains(ge.Error(), path) || !strings.Contains(ge.Error(), "3") {
		t.Errorf("message %q does not name the file and the expected seq", ge.Error())
	}
}

func TestSeqMustStartAtOne(t *testing.T) {
	_, store := newStore(t)
	writeLog(t, store, "aaaaaa", []string{line(t, "aaaaaa", 2, 10)})
	var ge *SeqGapError
	if _, err := ReadAll(store); !errors.As(err, &ge) {
		t.Fatalf("want *SeqGapError for a file starting at seq 2, got %v", err)
	} else if ge.Expected != 1 {
		t.Errorf("Expected = %d, want 1", ge.Expected)
	}
}

func TestALineWhoseRepDisagreesWithItsFilenameIsAParseError(t *testing.T) {
	_, store := newStore(t)
	writeLog(t, store, "aaaaaa", []string{line(t, "bbbbbb", 1, 10)})
	var pe *ParseError
	if _, err := ReadAll(store); !errors.As(err, &pe) {
		t.Fatalf("want *ParseError, got %v", err)
	}
}

func TestFilesListsEveryReplicaWithItsSize(t *testing.T) {
	_, store := newStore(t)
	pa := writeLog(t, store, "bbbbbb", []string{line(t, "bbbbbb", 1, 10)})
	pb := writeLog(t, store, "aaaaaa", []string{line(t, "aaaaaa", 1, 10), line(t, "aaaaaa", 2, 20)})
	// Anything that is not a replica log is ignored.
	if err := os.WriteFile(filepath.Join(LogDir(store), "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := Files(store)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	if files[0].Rep != "aaaaaa" || files[1].Rep != "bbbbbb" {
		t.Errorf("files are not sorted by replica: %+v", files)
	}
	if files[0].Path != pb || files[1].Path != pa {
		t.Errorf("paths = %q, %q; want %q, %q", files[0].Path, files[1].Path, pb, pa)
	}
	for _, f := range files {
		st, err := os.Stat(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		if f.Size != st.Size() {
			t.Errorf("%s: Size = %d, want %d", f.Rep, f.Size, st.Size())
		}
	}
}

func TestFilesOfAnEmptyStoreIsEmpty(t *testing.T) {
	_, store := newStore(t)
	files, err := Files(store)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}

func TestReadAllRoundTripsWhatTheAppenderWrote(t *testing.T) {
	cache, store := newStore(t)
	for _, rep := range []string{"aaaaaa", "bbbbbb"} {
		a, err := OpenAppender(store, cache, rep)
		if err != nil {
			t.Fatal(err)
		}
		var batch []*Envelope
		for i := range 5 {
			e := event(rep, i)
			e.TS = int64(100 + i*10)
			batch = append(batch, e)
		}
		if err := a.Append(batch); err != nil {
			t.Fatal(err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	}

	evs, err := ReadAll(store)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(evs) != 10 {
		t.Fatalf("got %d events, want 10", len(evs))
	}
	for i := 1; i < len(evs); i++ {
		if !evs[i-1].Position().Less(evs[i].Position()) {
			t.Fatalf("events %d and %d are out of order: %+v, %+v", i-1, i, evs[i-1].Position(), evs[i].Position())
		}
	}
	for _, e := range evs {
		if e.Data == nil {
			t.Fatalf("event %v lost its payload", e.Position())
		}
		if want := fmt.Sprintf(`{"note":"%s-`, e.Actor); !strings.HasPrefix(string(e.Data), want) {
			t.Errorf("payload %s does not start with %s", e.Data, want)
		}
	}
}
