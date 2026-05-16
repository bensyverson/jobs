package job

import (
	"bytes"
	"strings"
	"testing"
)

// R7 — Drop `note:` body from `list` parentheticals. Done tasks should
// scan as "(labels: ...)" not "(note: <wall of text>, labels: ...)".

func TestRenderMarkdownList_DoneTask_NoNoteBody(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Done task")
	if _, _, err := RunDone(db, []string{id}, false, "this is a long completion note that should not appear in list parentheticals", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, nil, nil, 0)
	got := buf.String()
	if strings.Contains(got, "note:") {
		t.Errorf("note: should not appear in list parens:\n%s", got)
	}
	if strings.Contains(got, "long completion note") {
		t.Errorf("note body should not appear in list parens:\n%s", got)
	}
}

func TestRenderMarkdownList_DoneTask_PreservesLabels(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Done task")
	if _, _, err := RunDone(db, []string{id}, false, "long body here", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	taskRow := MustGet(t, db, id)
	if _, err := RunLabelAdd(db, id, []string{"prototype", "web"}, TestActor); err != nil {
		t.Fatalf("label add: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	labels, err := GetLabelsForTaskIDs(db, []int64{taskRow.ID})
	if err != nil {
		t.Fatalf("GetLabelsForTaskIDs: %v", err)
	}
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, nil, labels, 0)
	got := buf.String()
	if !strings.Contains(got, "labels: prototype, web") {
		t.Errorf("labels missing from done task line:\n%s", got)
	}
}

func TestRenderMarkdownList_DoneTask_NoLabelsNoLabels_EmptyParens(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Done task")
	if _, _, err := RunDone(db, []string{id}, false, "completion text", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, nil, nil, 0)
	got := buf.String()
	// With no labels/blockers and no surface for the note, the line
	// should have NO trailing parentheses at all.
	if strings.Contains(got, "()") {
		t.Errorf("empty parens left on done task line:\n%s", got)
	}
	for line := range strings.SplitSeq(strings.TrimRight(got, "\n"), "\n") {
		if strings.Contains(line, id) {
			if strings.Contains(line, "(") {
				t.Errorf("done task with no decorations should have no parens: %q", line)
			}
		}
	}
}

func TestRenderMarkdownList_CanceledTask_PreservesMarker(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Canceled task")
	if _, _, _, err := RunCancel(db, []string{id}, "x", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, nil, nil, 0)
	got := buf.String()
	if !strings.Contains(got, "(canceled)") {
		t.Errorf("canceled marker missing:\n%s", got)
	}
}

func TestRenderMarkdownList_ClaimedTask_PreservesClaimedBy(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Claimed task")
	if err := RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, nil, nil, 0)
	got := buf.String()
	if !strings.Contains(got, "claimed by alice") {
		t.Errorf("claimed-by marker missing:\n%s", got)
	}
}
