package eventlog

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// ReplicaIDLength is the number of base62 characters in a replica id. A replica
// is one checkout on one machine; a worktree is its own replica.
const ReplicaIDLength = 6

// NewReplicaID mints a replica id from crypto/rand.
func NewReplicaID() (string, error) {
	id := make([]byte, ReplicaIDLength)
	limit := big.NewInt(int64(len(base62Chars)))
	for i := range id {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("eventlog: mint replica id: %w", err)
		}
		id[i] = base62Chars[n.Int64()]
	}
	return string(id), nil
}

// ValidReplicaID reports whether s is well formed: exactly ReplicaIDLength
// base62 characters. Log file names are replica ids, so this also guards the
// path built from one.
func ValidReplicaID(s string) bool {
	if len(s) != ReplicaIDLength {
		return false
	}
	for i := range len(s) {
		if !strings.ContainsRune(base62Chars, rune(s[i])) {
			return false
		}
	}
	return true
}
