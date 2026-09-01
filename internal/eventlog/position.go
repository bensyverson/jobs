package eventlog

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Position is the global ordering key. Every replica sorts the union of every
// log file by (ts, rep, seq) and gets the same sequence; that is the whole
// merge algorithm.
type Position struct {
	TS  int64
	Rep string
	Seq uint64
}

// Position returns e's ordering key.
func (e Envelope) Position() Position {
	return Position{TS: e.TS, Rep: e.Rep, Seq: e.Seq}
}

// Compare orders p against q, returning -1, 0 or 1.
func (p Position) Compare(q Position) int {
	if c := cmp.Compare(p.TS, q.TS); c != 0 {
		return c
	}
	if c := strings.Compare(p.Rep, q.Rep); c != 0 {
		return c
	}
	return cmp.Compare(p.Seq, q.Seq)
}

// Less reports whether p sorts before q.
func (p Position) Less(q Position) bool { return p.Compare(q) < 0 }

// String encodes p as "<ts>-<rep>-<seq>", URL-safe because rep is base62 and
// the other two are decimal.
//
// The encoding is a cursor, not a sort key: compare positions by parsing them,
// not by comparing the strings.
func (p Position) String() string {
	return fmt.Sprintf("%d-%s-%d", p.TS, p.Rep, p.Seq)
}

// ParsePosition decodes the encoding produced by Position.String.
func ParsePosition(s string) (Position, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return Position{}, fmt.Errorf("eventlog: %q is not a position (want <ts>-<rep>-<seq>)", s)
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || ts <= 0 {
		return Position{}, fmt.Errorf("eventlog: %q has no valid ts", s)
	}
	if !ValidReplicaID(parts[1]) {
		return Position{}, fmt.Errorf("eventlog: %q has no valid rep", s)
	}
	seq, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || seq == 0 {
		return Position{}, fmt.Errorf("eventlog: %q has no valid seq", s)
	}
	return Position{TS: ts, Rep: parts[1], Seq: seq}, nil
}

// Sort orders evs by position. The order is total — no two events share a
// position unless a replica has repeated a seq — so it is stable under shuffle.
func Sort(evs []Envelope) {
	slices.SortStableFunc(evs, func(a, b Envelope) int {
		return a.Position().Compare(b.Position())
	})
}
