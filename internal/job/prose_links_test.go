package job

import (
	"testing"
)

// ResolveProseLinks is the only thing that decides whether an id-shaped
// token in a description or note becomes a link: it scans candidates out of
// the text and asks the store which of them exist.

func TestResolveProseLinks_TaskIDResolves(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "a real task")

	links, err := ResolveProseLinks(db, []string{"blocked on " + id + " until Friday"})
	if err != nil {
		t.Fatalf("ResolveProseLinks: %v", err)
	}
	if got, want := links[id], "/tasks/"+id; got != want {
		t.Errorf("links[%q] = %q, want %q", id, got, want)
	}
}

func TestResolveProseLinks_UnknownTokenIsAbsent(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "a real task")

	links, err := ResolveProseLinks(db, []string{"the word banana is not a task"})
	if err != nil {
		t.Fatalf("ResolveProseLinks: %v", err)
	}
	if url, ok := links["banana"]; ok {
		t.Errorf("banana resolved to %q; an id-shaped word must not link", url)
	}
}

func TestResolveProseLinks_CriterionResolvesToItsTaskAnchor(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "gated task")
	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "tests pass"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	task := MustGet(t, db, id)
	crits, err := GetCriteria(db, task.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	crit := crits[0].ShortID

	links, err := ResolveProseLinks(db, []string{"criterion `" + crit + "` is green"})
	if err != nil {
		t.Fatalf("ResolveProseLinks: %v", err)
	}
	if got, want := links[crit], "/tasks/"+id+"#crit-"+crit; got != want {
		t.Errorf("links[%q] = %q, want %q", crit, got, want)
	}
}

// Criterion short ids are unique per task, not per store (migration 0008),
// so the same three characters can name a criterion on two different tasks.
// There is no single URL for such a token, so it must not link at all.
func TestResolveProseLinks_AmbiguousCriterionDoesNotResolve(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "first task")
	second := MustAdd(t, db, "", "second task")
	if _, err := RunAddCriteria(db, first, []Criterion{{Label: "one"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	ft := MustGet(t, db, first)
	crits, err := GetCriteria(db, ft.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	shared := crits[0].ShortID

	// Mint the same short id on the second task, which the per-task unique
	// index allows.
	st := MustGet(t, db, second)
	if _, err := db.Exec(
		`INSERT INTO task_criteria (task_id, label, state, sort_key, short_id) VALUES (?, 'two', 'pending', 'm', ?)`,
		st.ID, shared,
	); err != nil {
		t.Fatalf("insert colliding criterion: %v", err)
	}

	links, err := ResolveProseLinks(db, []string{"criterion `" + shared + "` is green"})
	if err != nil {
		t.Fatalf("ResolveProseLinks: %v", err)
	}
	if url, ok := links[shared]; ok {
		t.Errorf("ambiguous criterion %q resolved to %q; want no link", shared, url)
	}
}

func TestResolveProseLinks_EmptyTextsQueriesNothing(t *testing.T) {
	db := SetupTestDB(t)
	links, err := ResolveProseLinks(db, nil)
	if err != nil {
		t.Fatalf("ResolveProseLinks: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("got %d links, want none", len(links))
	}
}
