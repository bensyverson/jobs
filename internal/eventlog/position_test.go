package eventlog

import (
	"math/rand"
	"slices"
	"testing"
)

func TestPositionComparesTsThenRepThenSeq(t *testing.T) {
	cases := []struct {
		name string
		a, b Position
		want int
	}{
		{"ts wins", Position{TS: 1, Rep: "zzzzzz", Seq: 99}, Position{TS: 2, Rep: "aaaaaa", Seq: 1}, -1},
		{"rep breaks a ts tie", Position{TS: 1, Rep: "aaaaaa", Seq: 99}, Position{TS: 1, Rep: "bbbbbb", Seq: 1}, -1},
		{"seq breaks a rep tie", Position{TS: 1, Rep: "aaaaaa", Seq: 1}, Position{TS: 1, Rep: "aaaaaa", Seq: 2}, -1},
		{"equal", Position{TS: 1, Rep: "aaaaaa", Seq: 1}, Position{TS: 1, Rep: "aaaaaa", Seq: 1}, 0},
		{"greater", Position{TS: 3, Rep: "aaaaaa", Seq: 1}, Position{TS: 2, Rep: "aaaaaa", Seq: 1}, 1},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("%s: Compare = %d, want %d", c.name, got, c.want)
		}
		if got := c.a.Less(c.b); got != (c.want < 0) {
			t.Errorf("%s: Less = %v, want %v", c.name, got, c.want < 0)
		}
	}
}

func TestPositionStringRoundTrips(t *testing.T) {
	for _, p := range []Position{
		{TS: 1756742400123, Rep: "k7Qx2m", Seq: 412},
		{TS: 1, Rep: "aaaaaa", Seq: 1},
		{TS: 1756742400000, Rep: "", Seq: 991},
	} {
		s := p.String()
		got, err := ParsePosition(s)
		if err != nil {
			t.Fatalf("ParsePosition(%q): %v", s, err)
		}
		if got != p {
			t.Errorf("round trip of %q: got %+v want %+v", s, got, p)
		}
	}
}

func TestParsePositionRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "1-k7Qx2m", "1-k7Qx2m-2-3", "x-k7Qx2m-2", "1-k7Qx2m-x", "-1-k7Qx2m-2", "1-k7Qx2m-0", "0-k7Qx2m-1"} {
		if _, err := ParsePosition(s); err == nil {
			t.Errorf("ParsePosition(%q): want error, got none", s)
		}
	}
}

func TestLegacyPositionEncodesWithAnEmptyRep(t *testing.T) {
	p := Position{TS: 1756742400000, Seq: 991}
	if got, want := p.String(), "1756742400000--991"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if !p.Legacy() {
		t.Error("Legacy() = false, want true")
	}
	if (Position{TS: 1, Rep: "aaaaaa", Seq: 1}).Legacy() {
		t.Error("Legacy() = true for a positioned cursor")
	}
	got, err := ParsePosition("1756742400000--991")
	if err != nil {
		t.Fatalf("ParsePosition: %v", err)
	}
	if got != p {
		t.Errorf("ParsePosition = %+v, want %+v", got, p)
	}
}

func TestLegacyPositionSortsBeforeEveryPositionedOneAtTheSameTS(t *testing.T) {
	legacy := Position{TS: 10, Seq: 4}
	positioned := Position{TS: 10, Rep: "aaaaaa", Seq: 1}
	if !legacy.Less(positioned) {
		t.Error("a legacy position must sort before a positioned one at the same ts")
	}
}

func TestParsedPositionsSortLikeTheStruct(t *testing.T) {
	ps := samplePositions()
	byStruct := slices.Clone(ps)
	slices.SortFunc(byStruct, Position.Compare)

	encoded := make([]Position, 0, len(ps))
	for _, p := range ps {
		q, err := ParsePosition(p.String())
		if err != nil {
			t.Fatalf("ParsePosition(%q): %v", p.String(), err)
		}
		encoded = append(encoded, q)
	}
	slices.SortFunc(encoded, Position.Compare)

	if !slices.Equal(byStruct, encoded) {
		t.Errorf("sorting parsed positions differs:\n got %+v\nwant %+v", encoded, byStruct)
	}
}

func samplePositions() []Position {
	return []Position{
		{TS: 5, Rep: "bbbbbb", Seq: 2},
		{TS: 5, Rep: "aaaaaa", Seq: 9},
		{TS: 1, Rep: "zzzzzz", Seq: 1},
		{TS: 5, Rep: "bbbbbb", Seq: 1},
		{TS: 1756742400123, Rep: "k7Qx2m", Seq: 412},
		{TS: 2, Rep: "aaaaaa", Seq: 3},
	}
}

func TestSortIsTotalAndStableUnderShuffle(t *testing.T) {
	base := envelopesForSorting()

	want := slices.Clone(base)
	Sort(want)

	rng := rand.New(rand.NewSource(7))
	for range 25 {
		shuffled := slices.Clone(base)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		Sort(shuffled)
		for i := range want {
			if shuffled[i].Position() != want[i].Position() {
				t.Fatalf("shuffled sort differs at %d: got %+v want %+v", i, shuffled[i].Position(), want[i].Position())
			}
		}
	}
}

func envelopesForSorting() []Envelope {
	var out []Envelope
	for _, p := range samplePositions() {
		out = append(out, Envelope{V: 1, Rep: p.Rep, Seq: p.Seq, TS: p.TS, Type: "noted"})
	}
	return out
}
