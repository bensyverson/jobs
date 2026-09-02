package job

import (
	"sort"
	"strings"
	"testing"
)

func mustKey(t *testing.T, a, b string, rep ...string) string {
	t.Helper()
	k, err := KeyBetween(a, b, rep...)
	if err != nil {
		t.Fatalf("KeyBetween(%q, %q, %v): %v", a, b, rep, err)
	}
	if a != "" && !(k > a) {
		t.Fatalf("KeyBetween(%q, %q) = %q: not greater than left neighbour", a, b, k)
	}
	if b != "" && !(k < b) {
		t.Fatalf("KeyBetween(%q, %q) = %q: not less than right neighbour", a, b, k)
	}
	return k
}

func TestKeyBetween_EmptyGapIsAMiddleKey(t *testing.T) {
	k := mustKey(t, "", "")
	if len(k) != sortKeyIntWidth {
		t.Fatalf("first key %q: want width %d, got %d", k, sortKeyIntWidth, len(k))
	}
	// Room on both sides: the first key is not at either extreme.
	before := mustKey(t, "", k)
	after := mustKey(t, k, "")
	if !(before < k && k < after) {
		t.Fatalf("first key %q has no room: before=%q after=%q", k, before, after)
	}
}

func TestKeyBetween_AppendStaysBounded(t *testing.T) {
	prev := ""
	for i := range 5000 {
		k := mustKey(t, prev, "")
		if len(k) > sortKeyIntWidth {
			t.Fatalf("append %d produced %q (len %d); keys must not grow on append", i, k, len(k))
		}
		prev = k
	}
}

func TestKeyBetween_PrependStaysBounded(t *testing.T) {
	next := ""
	for i := range 5000 {
		k := mustKey(t, "", next)
		if len(k) > sortKeyIntWidth {
			t.Fatalf("prepend %d produced %q (len %d); keys must not grow on prepend", i, k, len(k))
		}
		next = k
	}
}

func TestKeyBetween_RepeatedInsertBetweenStaysOrdered(t *testing.T) {
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")
	prevHi := hi
	for range 200 {
		k := mustKey(t, lo, prevHi)
		prevHi = k
	}
	if !(lo < prevHi && prevHi < hi) {
		t.Fatalf("after 200 inserts: %q < %q < %q violated", lo, prevHi, hi)
	}
}

func TestKeyBetween_MidpointOfEveryAdjacentPairSorts(t *testing.T) {
	// Build a sequence, then insert between every adjacent pair and check
	// the whole list is still in order.
	var keys []string
	prev := ""
	for range 20 {
		prev = mustKey(t, prev, "")
		keys = append(keys, prev)
	}
	var expanded []string
	for i := 0; i < len(keys)-1; i++ {
		expanded = append(expanded, keys[i], mustKey(t, keys[i], keys[i+1]))
	}
	expanded = append(expanded, keys[len(keys)-1])
	if !sort.StringsAreSorted(expanded) {
		t.Fatalf("expanded sequence not sorted: %v", expanded)
	}
}

func TestKeyBetween_ReplicaTieBreak(t *testing.T) {
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")

	const repA = "Zp09aQ"
	const repB = "k7Qx2m"
	ka := mustKey(t, lo, hi, repA)
	kb := mustKey(t, lo, hi, repB)

	if ka == kb {
		t.Fatalf("two replicas produced the same key %q", ka)
	}
	if !(repA < repB) {
		t.Fatalf("test premise: %q should sort before %q", repA, repB)
	}
	if !(ka < kb) {
		t.Fatalf("keys not ordered by replica id: %q (%s) should sort before %q (%s)", ka, repA, kb, repB)
	}
	// Both still land inside the gap (mustKey already checked, but be explicit
	// about the pair as a whole).
	if !(lo < ka && kb < hi) {
		t.Fatalf("replica keys escaped the gap: %q < %q, %q < %q", lo, ka, kb, hi)
	}
}

func TestKeyBetween_ReplicaTieBreakAtEveryGapShape(t *testing.T) {
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")
	tight := mustKey(t, lo, hi) // adjacent to both neighbours
	gaps := [][2]string{
		{"", ""},
		{"", lo},
		{lo, ""},
		{lo, hi},
		{lo, tight},
		{tight, hi},
	}
	for _, g := range gaps {
		ka := mustKey(t, g[0], g[1], "Zp09aQ")
		kb := mustKey(t, g[0], g[1], "k7Qx2m")
		if !(ka < kb) {
			t.Fatalf("gap (%q,%q): %q should sort before %q", g[0], g[1], ka, kb)
		}
	}
}

func TestKeyBetween_ReplicaKeysRemainUsableNeighbours(t *testing.T) {
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")
	k := mustKey(t, lo, hi, "k7Qx2m")
	// A key carrying a replica suffix must still be a legal neighbour.
	mustKey(t, lo, k)
	mustKey(t, k, hi)
	mustKey(t, k, "")
	mustKey(t, "", k)
}

func TestKeyBetween_RejectsBadInput(t *testing.T) {
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")
	cases := []struct {
		name string
		a, b string
		rep  []string
	}{
		{"inverted", hi, lo, nil},
		{"equal", lo, lo, nil},
		{"left too short", "abc", "", nil},
		{"right bad char", "", "V000/0", nil},
		{"rep bad char", lo, hi, []string{"a/b"}},
		{"two reps", lo, hi, []string{"aa", "bb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if k, err := KeyBetween(c.a, c.b, c.rep...); err == nil {
				t.Fatalf("KeyBetween(%q, %q, %v) = %q, want error", c.a, c.b, c.rep, k)
			}
		})
	}
}

func TestKeyBetween_UsesByteOrderedAlphabetOnly(t *testing.T) {
	if !sort.StringsAreSorted(strings.Split(sortKeyDigits, "")) {
		t.Fatalf("sort key alphabet %q is not in byte order", sortKeyDigits)
	}
	if len(sortKeyDigits) != 62 {
		t.Fatalf("sort key alphabet has %d characters, want 62", len(sortKeyDigits))
	}
	prev := ""
	for range 50 {
		prev = mustKey(t, prev, "")
		for j := 0; j < len(prev); j++ {
			if !strings.ContainsRune(sortKeyDigits, rune(prev[j])) {
				t.Fatalf("key %q contains %q, outside the alphabet", prev, prev[j])
			}
		}
	}
}

func TestKeyBetween_NeverAPrefixOfTheRightNeighbour(t *testing.T) {
	// The replica suffix is only safe if the generated key is never a prefix
	// of the right-hand neighbour; assert the property directly.
	lo := mustKey(t, "", "")
	hi := mustKey(t, lo, "")
	right := hi
	for range 100 {
		k := mustKey(t, lo, right)
		if strings.HasPrefix(right, k) {
			t.Fatalf("key %q is a prefix of its right neighbour %q", k, right)
		}
		right = k
	}
}

func TestSortKeySequence(t *testing.T) {
	keys, err := SortKeySequence("", 5)
	if err != nil {
		t.Fatalf("SortKeySequence: %v", err)
	}
	if len(keys) != 5 {
		t.Fatalf("want 5 keys, got %d", len(keys))
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("sequence not sorted: %v", keys)
	}
	after, err := SortKeySequence(keys[len(keys)-1], 3)
	if err != nil {
		t.Fatalf("SortKeySequence (continued): %v", err)
	}
	if !(after[0] > keys[len(keys)-1]) {
		t.Fatalf("continued sequence %v does not follow %v", after, keys)
	}
	if n, err := SortKeySequence("", 0); err != nil || len(n) != 0 {
		t.Fatalf("SortKeySequence(_, 0) = %v, %v", n, err)
	}
}
