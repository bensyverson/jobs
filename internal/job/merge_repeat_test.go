package job

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// Re-running a merge.
//
// `merge` writes the other side's tail into the cache as unpositioned rows, so
// the next open adopts the cache into the store and the rebuild reorders the
// events table by log position rather than by row id. The positional prefix the
// two files once shared is gone, and the question "are these two copies of one
// database?" has to be answered by set containment instead.

// mergedThenAdopted performs the situation the regression is about: two copies
// of one database, both written to, merged once, and then reopened so the real
// adoption and position-ordered rebuild run over the result. It returns the
// reopened local handle and the untouched other side.
func mergedThenAdopted(t *testing.T) (local, other *sql.DB, localPath, otherPath string) {
	t.Helper()
	clock := newMergeClock(t)
	var seedID string
	local, other, localPath, otherPath = divergedPair(t, func(db *sql.DB) {
		seedID = MustAdd(t, db, "", "Shared root")
	})

	clock.advance(time.Minute)
	MustAdd(t, local, seedID, "Only over here")
	clock.advance(time.Minute)
	MustAdd(t, other, seedID, "Only over there")

	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if err := local.Close(); err != nil {
		t.Fatalf("close local: %v", err)
	}

	// The real path: OpenDB adopts the cache the merge left unpositioned and
	// rebuilds it from the log, in (ts, rep, seq) order.
	reopened, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("reopen local: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	return reopened, other, localPath, otherPath
}

// Case 1: everything the other side holds is already here. Merging the same
// pair twice changes nothing, which is what the docs promise.
func TestMerge_SecondMergeOfTheSamePairIsAlreadyMerged(t *testing.T) {
	local, _, _, otherPath := mergedThenAdopted(t)

	before := logicalDump(t, local)

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("second merge of the same pair should succeed, got: %v", err)
	}
	if !report.AlreadyMerged {
		t.Errorf("report should say the other database is already merged: %+v", report)
	}
	if report.Changed {
		t.Error("an already-merged pair changes nothing")
	}
	md := report.Markdown()
	if !strings.Contains(md, "Already merged") || !strings.Contains(md, "nothing to do") {
		t.Errorf("report should say so in prose:\n%s", md)
	}

	if after := logicalDump(t, local); after != before {
		t.Errorf("second merge wrote to the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Case 2: the other copy was written to after the merge. Its history overlaps
// this one's but no longer lines up, and merge cannot fold that tail.
func TestMerge_RefusesATailWrittenAfterTheMerge(t *testing.T) {
	local, other, _, otherPath := mergedThenAdopted(t)

	MustAdd(t, other, "", "Written after the merge")

	before := logicalDump(t, local)
	_, err := RunMerge(local, otherPath, false)
	if err == nil {
		t.Fatal("expected a refusal: merge cannot fold a tail written after the merge")
	}
	if strings.Contains(err.Error(), "unrelated") {
		t.Errorf("the two databases are related; the message should not say otherwise: %v", err)
	}
	if !strings.Contains(err.Error(), "across-machines") {
		t.Errorf("the refusal should point at the across-machines guide: %v", err)
	}
	if after := logicalDump(t, local); after != before {
		t.Error("a refused merge wrote to the database")
	}
}
