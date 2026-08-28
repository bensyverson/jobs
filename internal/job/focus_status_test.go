package job

import (
	"bytes"
	"strings"
	"testing"
)

// HXNXb — `job status` (forest scope) is focus-aware: BuildRollup carries
// the actor's focused root, RenderSummary prints a `Focus:` line when set,
// and the Next: hint resolves within the focused root — falling back to the
// loud escape hint when the root is exhausted. Subtree scope (explicit
// target) stays focus-blind, matching "explicit arguments win".

// f3Z — Status output includes the Focus line when set and omits it when
// not.
func TestBuildRollup_Forest_CarriesAndRendersFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	MustAdd(t, db, rootB, "B leaf 2")

	// No focus yet: no Focus in the rollup or the rendering.
	rollup, err := BuildRollup(db, nil, TestActor)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	if rollup.Focus != nil {
		t.Errorf("Focus before any claim: got %s, want nil", rollup.Focus.ShortID)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, rollup)
	if strings.Contains(buf.String(), "Focus:") {
		t.Errorf("rendering must omit Focus line when unset:\n%s", buf.String())
	}

	MustClaim(t, db, leafB1, "1h") // sets focus to rootB

	rollup, err = BuildRollup(db, nil, TestActor)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	if rollup.Focus == nil || rollup.Focus.ShortID != rootB {
		t.Fatalf("Focus after claim: got %v, want %s", rollup.Focus, rootB)
	}
	buf.Reset()
	RenderSummary(&buf, rollup)
	if !strings.Contains(buf.String(), "Focus: "+rootB+" \"Root B\"") {
		t.Errorf("rendering must include the Focus line:\n%s", buf.String())
	}
}

// IvS — The status Next hint stays inside the focused root.
func TestBuildRollup_Forest_NextScopedToFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	leafB2 := MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // focus rootB

	rollup, err := BuildRollup(db, nil, TestActor)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	if rollup.Next == nil || rollup.Next.ShortID != leafB2 {
		t.Errorf("Next with focus on B: got %v, want %s", rollup.Next, leafB2)
	}
}

// With the focused root exhausted, Next is empty and the rendering carries
// the escape hint instead of silently pointing at another tree.
func TestRenderSummary_FocusedRootExhausted_ShowsEscapeHint(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB := MustAdd(t, db, rootB, "Only B leaf")

	MustClaim(t, db, leafB, "1h") // focus rootB; nothing else available in B

	rollup, err := BuildRollup(db, nil, TestActor)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	if rollup.Next != nil {
		t.Errorf("Next with exhausted focused root: got %s, want nil", rollup.Next.ShortID)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, rollup)
	out := buf.String()
	if !strings.Contains(out, "claim --next") || !strings.Contains(out, "focus --release") {
		t.Errorf("exhausted-focus rendering must carry both escapes:\n%s", out)
	}
}

// Subtree scope (explicit target) ignores focus entirely.
func TestBuildRollup_SubtreeScope_IgnoresFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // focus rootB

	targetA, err := GetTaskByShortID(db, rootA)
	if err != nil {
		t.Fatalf("GetTaskByShortID: %v", err)
	}
	rollup, err := BuildRollup(db, targetA, TestActor)
	if err != nil {
		t.Fatalf("BuildRollup(rootA): %v", err)
	}
	if rollup.Focus != nil {
		t.Errorf("subtree rollup Focus: got %s, want nil (explicit target wins)", rollup.Focus.ShortID)
	}
	if rollup.Next == nil || rollup.Next.ShortID != leafA {
		t.Errorf("subtree Next: got %v, want %s", rollup.Next, leafA)
	}
}
