package job

import (
	"bytes"
	"strings"
	"testing"
)

// Descriptions and notes are markdown prose (see prose.go): the console
// reflows hard-wrapped paragraphs at render time rather than at write time.

func TestRenderInfoMarkdown_UnwrapsDescription(t *testing.T) {
	db := SetupTestDB(t)
	id, err := RunAdd(db, "", "Task",
		"line one of description\nline two\nline three with words", "", nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}

	info, err := RunInfo(db, id.ShortID)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var buf bytes.Buffer
	RenderInfoMarkdown(&buf, info)
	got := buf.String()
	if !strings.Contains(got, "Description:\n  line one of description line two line three with words") {
		t.Errorf("description not unwrapped:\n%s", got)
	}
}

func TestRenderInfoMarkdown_IndentsEveryDescriptionLine(t *testing.T) {
	db := SetupTestDB(t)
	id, err := RunAdd(db, "", "Task", "Intro.\n\n- one\n- two", "", nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	info, err := RunInfo(db, id.ShortID)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var buf bytes.Buffer
	RenderInfoMarkdown(&buf, info)
	got := buf.String()
	if !strings.Contains(got, "Description:\n  Intro.\n\n  - one\n  - two\n") {
		t.Errorf("continuation lines not indented:\n%s", got)
	}
}

func TestRenderInfoMarkdown_UnwrapsNoteBody(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	body := "Implemented sticky chrome layout\nbody/page/main use 100vh\nmain has overflow-y: auto\n\n- bullet"
	if err := RunNote(db, id, body, nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}

	info, err := RunInfo(db, id)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var buf bytes.Buffer
	RenderInfoMarkdown(&buf, info)
	got := buf.String()
	if !strings.Contains(got, "    Implemented sticky chrome layout body/page/main use 100vh main has overflow-y: auto\n\n    - bullet\n") {
		t.Errorf("note body should be reflowed and indented on every line:\n%s", got)
	}
}

func TestRenderAncestorBrief_HangingIndent(t *testing.T) {
	var buf bytes.Buffer
	RenderAncestorBrief(&buf, &Task{ShortID: "abc12", Title: "T", Description: "one\ntwo\n\nthree"})
	want := "Description:  one two\n\n              three\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("got:\n%s\nwant to contain:\n%s", buf.String(), want)
	}
}
