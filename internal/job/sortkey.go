package job

import (
	"fmt"
	"strings"
)

// Fractional sort keys.
//
// Integer sort orders cannot merge: two machines inserting under one parent
// both shift the same rows, and the shifts collide
// (project/2026-09-01-git-native-event-log.md, "Ordering within a parent").
// A sort key is a string chosen so that a new key can always be generated
// strictly between any two neighbours, which makes placing a task a plain
// column write — no sibling's key is ever touched.
//
// Alphabet. The keys are compared by SQLite's default BINARY collation and
// by Go's string comparison, so the alphabet must be in *byte* order.
// base62Chars (used for short ids) is not: it runs a-z, A-Z, 0-9 while
// ASCII runs 0-9, A-Z, a-z. Sort keys therefore use the same 62 characters
// in ASCII order, as sortKeyDigits below. A replica id minted from
// base62Chars is drawn from the same character set, so it is always a valid
// key suffix.
//
// Scheme. A key is a fixed-width base-62 integer part followed by an
// optional fraction:
//
//	V00000      the first key under a parent (the middle of the integer space)
//	V00001      appended after it
//	V000000V    inserted between V00000 and V00001
//
// Appending takes the next integer, so repeated append-at-end never grows a
// key past sortKeyIntWidth characters; the integer space holds 62^6 ≈ 5.7e10
// positions and starts in the middle, so prepending is equally bounded in
// practice. Repeated insertion into one gap grows the fraction, which is the
// accepted cost of the scheme.
//
// Invariant: a key's fraction never ends in the lowest digit ('0'). Byte
// comparison then agrees with fraction comparison ("1" and "10" would
// otherwise be equal as fractions but ordered as strings).
const (
	sortKeyDigits    = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	sortKeyBase      = int64(len(sortKeyDigits))
	sortKeyIntWidth  = 6
	sortKeyIntMax    = int64(56800235583) // 62^6 - 1
	sortKeyIntStart  = sortKeyIntMax / 2
	sortKeyRepEndsIn = "1" // a non-'0' terminator, so a replica suffix keeps the invariant
)

// KeyBetween returns a sort key that sorts strictly after a and strictly
// before b in byte order. An empty a means "before everything", an empty b
// means "after everything"; both empty asks for the first key under a
// parent.
//
// An optional replica id may be passed as a tie-break suffix. Two replicas
// generating a key for the same gap then get distinct keys that order by
// replica id, which is what makes concurrent inserts under one parent
// converge to the same order on every machine.
func KeyBetween(a, b string, rep ...string) (string, error) {
	var repID string
	switch len(rep) {
	case 0:
	case 1:
		repID = rep[0]
	default:
		return "", fmt.Errorf("sort key: at most one replica id, got %d", len(rep))
	}
	if repID != "" {
		if err := validateSortKeyDigits(repID); err != nil {
			return "", fmt.Errorf("sort key: replica id %q: %w", repID, err)
		}
	}

	key, err := keyBetween(a, b)
	if err != nil {
		return "", err
	}
	if repID == "" {
		return key, nil
	}
	// keyBetween never returns a prefix of b, so any suffix stays inside the
	// gap. The terminator keeps the no-trailing-'0' invariant for replica
	// ids that happen to end in '0'.
	return key + repID + sortKeyRepEndsIn, nil
}

// SortKeySequence returns n consecutive keys that all sort after `after`
// (or from the start of the space when it is empty). Used to lay out a list
// of new siblings in one pass — a plan import, a split.
func SortKeySequence(after string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	keys := make([]string, 0, n)
	prev := after
	for range n {
		k, err := KeyBetween(prev, "")
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
		prev = k
	}
	return keys, nil
}

func keyBetween(a, b string) (string, error) {
	if a == "" && b == "" {
		return sortKeyIntString(sortKeyIntStart), nil
	}
	if b == "" {
		ia, fa, err := parseSortKey(a)
		if err != nil {
			return "", err
		}
		if ia < sortKeyIntMax {
			return sortKeyIntString(ia + 1), nil
		}
		return sortKeyIntString(ia) + midpointFrac(fa, ""), nil
	}
	if a == "" {
		ib, fb, err := parseSortKey(b)
		if err != nil {
			return "", err
		}
		if ib > 0 {
			return sortKeyIntString(ib - 1), nil
		}
		return sortKeyIntString(0) + midpointFrac("", fb), nil
	}

	ia, fa, err := parseSortKey(a)
	if err != nil {
		return "", err
	}
	ib, fb, err := parseSortKey(b)
	if err != nil {
		return "", err
	}
	if a >= b {
		return "", fmt.Errorf("sort key: %q does not sort before %q", a, b)
	}
	switch {
	case ib-ia >= 2:
		return sortKeyIntString((ia + ib) / 2), nil
	case ib-ia == 1:
		// Anything after a but still inside a's integer slot is below b.
		return sortKeyIntString(ia) + midpointFrac(fa, ""), nil
	default:
		return sortKeyIntString(ia) + midpointFrac(fa, fb), nil
	}
}

// midpointFrac returns a fraction string strictly between a and b, where an
// empty a is 0 and an empty b is 1. It is the classic fractional-indexing
// midpoint with one change: where the canonical version may return a prefix
// of b, this one recurses one digit further. A key that is a prefix of its
// right neighbour cannot carry a replica suffix and stay inside the gap, and
// that suffix is how two replicas break a tie.
//
// Callers guarantee a < b and that neither fraction ends in '0'.
func midpointFrac(a, b string) string {
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			var aTail string
			if n < len(a) {
				aTail = a[n:]
			}
			return b[:n] + midpointFrac(aTail, b[n:])
		}
	}

	da := int64(0)
	if a != "" {
		da = int64(strings.IndexByte(sortKeyDigits, a[0]))
	}
	db := sortKeyBase
	if b != "" {
		db = int64(strings.IndexByte(sortKeyDigits, b[0]))
	}
	if db-da > 1 {
		return string(sortKeyDigits[(da+db+1)/2])
	}
	if b != "" && len(b) > 1 {
		return b[:1] + midpointFrac("", b[1:])
	}
	var aTail string
	if len(a) > 1 {
		aTail = a[1:]
	}
	return string(sortKeyDigits[da]) + midpointFrac(aTail, "")
}

// parseSortKey splits a key into its integer part and its fraction.
func parseSortKey(k string) (int64, string, error) {
	if len(k) < sortKeyIntWidth {
		return 0, "", fmt.Errorf("sort key: %q is shorter than %d characters", k, sortKeyIntWidth)
	}
	if err := validateSortKeyDigits(k); err != nil {
		return 0, "", fmt.Errorf("sort key: %q: %w", k, err)
	}
	frac := k[sortKeyIntWidth:]
	if frac != "" && frac[len(frac)-1] == sortKeyDigits[0] {
		return 0, "", fmt.Errorf("sort key: %q ends in %q, which has no ordering", k, sortKeyDigits[0])
	}
	var n int64
	for i := range sortKeyIntWidth {
		n = n*sortKeyBase + int64(strings.IndexByte(sortKeyDigits, k[i]))
	}
	return n, frac, nil
}

func sortKeyIntString(n int64) string {
	out := make([]byte, sortKeyIntWidth)
	for i := sortKeyIntWidth - 1; i >= 0; i-- {
		out[i] = sortKeyDigits[n%sortKeyBase]
		n /= sortKeyBase
	}
	return string(out)
}

func validateSortKeyDigits(s string) error {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(sortKeyDigits, s[i]) < 0 {
			return fmt.Errorf("character %q is not a sort key digit", s[i])
		}
	}
	return nil
}
