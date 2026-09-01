package eventlog

import (
	"strings"
	"testing"
)

func TestNewReplicaIDIsSixBase62Chars(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		id, err := NewReplicaID()
		if err != nil {
			t.Fatalf("NewReplicaID: %v", err)
		}
		if len(id) != ReplicaIDLength {
			t.Fatalf("id %q has length %d, want %d", id, len(id), ReplicaIDLength)
		}
		for _, r := range id {
			if !strings.ContainsRune(base62Chars, r) {
				t.Fatalf("id %q contains %q, which is not base62", id, r)
			}
		}
		if seen[id] {
			t.Fatalf("NewReplicaID repeated %q within 500 draws", id)
		}
		seen[id] = true
	}
}

func TestNewReplicaIDUsesTheWholeAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for range 2000 {
		id, err := NewReplicaID()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range id {
			seen[r] = true
		}
	}
	if len(seen) != len(base62Chars) {
		t.Errorf("saw %d of %d alphabet characters over 12000 draws", len(seen), len(base62Chars))
	}
}

func TestValidReplicaID(t *testing.T) {
	valid := []string{"k7Qx2m", "aaaaaa", "000000", "ZZZZZZ"}
	for _, s := range valid {
		if !ValidReplicaID(s) {
			t.Errorf("ValidReplicaID(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "abcde", "abcdefg", "abcde-", "abc/de", "abcd e", "abcdé"}
	for _, s := range invalid {
		if ValidReplicaID(s) {
			t.Errorf("ValidReplicaID(%q) = true, want false", s)
		}
	}
}

func TestBase62AlphabetMatchesTheShortIDAlphabet(t *testing.T) {
	// Must stay identical to base62Chars in internal/job/database.go, which this
	// package cannot import.
	const want = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if base62Chars != want {
		t.Fatalf("base62Chars drifted:\n got %q\nwant %q", base62Chars, want)
	}
}
