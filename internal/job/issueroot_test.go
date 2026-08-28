package job

import (
	"strings"
	"testing"
)

// 88QEE — `job issue` resolves its parent through the issue-root resolver:
// the caller's focused issue root, else the sole issue root, else an error
// that names the way out. --found-in defaults from the caller's live claims,
// which LiveClaims answers.

func TestResolveIssueRoot_SoleIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "A plan")
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)

	root, err := ResolveIssueRoot(db, TestActor)
	if err != nil {
		t.Fatalf("ResolveIssueRoot: %v", err)
	}
	if root.ShortID != bugs {
		t.Fatalf("resolved %s, want the sole issue root %s", root.ShortID, bugs)
	}
}

func TestResolveIssueRoot_PrefersTheFocusedIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "Bugs")
	second := MustAdd(t, db, "", "Inbox")
	mustSetKind(t, db, first, KindIssue)
	mustSetKind(t, db, second, KindIssue)
	mustSetFocus(t, db, second, TestActor)

	root, err := ResolveIssueRoot(db, TestActor)
	if err != nil {
		t.Fatalf("ResolveIssueRoot: %v", err)
	}
	if root.ShortID != second {
		t.Fatalf("resolved %s, want the focused issue root %s", root.ShortID, second)
	}
}

// A task focus never decides the issue parent: with one issue root and a
// task focus elsewhere, the sole issue root still wins.
func TestResolveIssueRoot_IgnoresTheTaskFocus(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "A plan")
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	mustSetFocus(t, db, plan, TestActor)

	root, err := ResolveIssueRoot(db, TestActor)
	if err != nil {
		t.Fatalf("ResolveIssueRoot: %v", err)
	}
	if root.ShortID != bugs {
		t.Fatalf("resolved %s, want %s", root.ShortID, bugs)
	}
}

// Another actor's issue focus is not this actor's.
func TestResolveIssueRoot_FocusIsPerActor(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "Bugs")
	second := MustAdd(t, db, "", "Inbox")
	mustSetKind(t, db, first, KindIssue)
	mustSetKind(t, db, second, KindIssue)
	mustSetFocus(t, db, second, "someone-else")

	if _, err := ResolveIssueRoot(db, TestActor); err == nil {
		t.Fatal("ResolveIssueRoot: want an ambiguity error, got none")
	}
}

func TestResolveIssueRoot_NoIssueRootNamesTheCreateCommand(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "A plan")

	_, err := ResolveIssueRoot(db, TestActor)
	if err == nil {
		t.Fatal("ResolveIssueRoot: want an error with no issue root, got none")
	}
	if !strings.Contains(err.Error(), "job add <title> --kind issue") {
		t.Fatalf("error = %q, want it to name `job add <title> --kind issue`", err)
	}
}

func TestResolveIssueRoot_AmbiguousNamesEveryRoot(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "Bugs")
	second := MustAdd(t, db, "", "Inbox")
	mustSetKind(t, db, first, KindIssue)
	mustSetKind(t, db, second, KindIssue)

	_, err := ResolveIssueRoot(db, TestActor)
	if err == nil {
		t.Fatal("ResolveIssueRoot: want an ambiguity error, got none")
	}
	msg := err.Error()
	for _, want := range []string{first, "Bugs", second, "Inbox", "job focus <id>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}
}

// A closed issue root is not a candidate: it cannot take new work, and
// GetFocusKind already reads a closed root as no focus.
func TestResolveIssueRoot_SkipsClosedIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	closed := MustAdd(t, db, "", "Old bugs")
	open := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, closed, KindIssue)
	mustSetKind(t, db, open, KindIssue)
	if _, _, err := RunDone(db, []string{closed}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	root, err := ResolveIssueRoot(db, TestActor)
	if err != nil {
		t.Fatalf("ResolveIssueRoot: %v", err)
	}
	if root.ShortID != open {
		t.Fatalf("resolved %s, want the open issue root %s", root.ShortID, open)
	}
}

func TestLiveClaims_NoneWhenTheActorHoldsNothing(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "A plan")
	leaf := MustAdd(t, db, plan, "A leaf")
	if err := RunClaim(db, leaf, "", "", "someone-else", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	claims, err := LiveClaims(db, TestActor)
	if err != nil {
		t.Fatalf("LiveClaims: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("LiveClaims = %d, want 0 for an actor holding nothing", len(claims))
	}
}

func TestLiveClaims_ReturnsTheActorsOwnClaims(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "A plan")
	mine := MustAdd(t, db, plan, "Mine")
	theirs := MustAdd(t, db, plan, "Theirs")
	if err := RunClaim(db, mine, "", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim(mine): %v", err)
	}
	if err := RunClaim(db, theirs, "", "", "someone-else", false); err != nil {
		t.Fatalf("RunClaim(theirs): %v", err)
	}

	claims, err := LiveClaims(db, TestActor)
	if err != nil {
		t.Fatalf("LiveClaims: %v", err)
	}
	if len(claims) != 1 || claims[0].ShortID != mine {
		t.Fatalf("LiveClaims = %v, want exactly %s", claims, mine)
	}
}

func TestLiveClaims_ReturnsEveryClaimWhenSeveralAreHeld(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "First")
	second := MustAdd(t, db, "", "Second")
	if err := RunClaim(db, first, "", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim(first): %v", err)
	}
	if err := RunClaim(db, second, "", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim(second): %v", err)
	}

	claims, err := LiveClaims(db, TestActor)
	if err != nil {
		t.Fatalf("LiveClaims: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("LiveClaims = %d, want 2", len(claims))
	}
}

// An expired claim is not live: the sweep runs first, so a stale lock never
// becomes the found-in default.
func TestLiveClaims_ExcludesExpiredClaims(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	if err := RunClaim(db, leaf, "1s", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}
	if _, err := db.Exec("UPDATE tasks SET claim_expires_at = ? WHERE short_id = ?",
		CurrentNowFunc().Unix()-60, leaf); err != nil {
		t.Fatalf("expire: %v", err)
	}

	claims, err := LiveClaims(db, TestActor)
	if err != nil {
		t.Fatalf("LiveClaims: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("LiveClaims = %d, want 0 once the claim has expired", len(claims))
	}
}
