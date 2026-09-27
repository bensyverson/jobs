package job

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func renderListString(db any, nodes []*TaskNode, blockers map[string][]string) string {
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, blockers, nil, 0)
	return buf.String()
}

func TestList_CheckboxOpen(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Open task")
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [ ] `" + id + "` Open task\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_CheckboxDone(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Closed task")
	MustDone(t, db, id)
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [x] `" + id + "` Closed task\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_ClaimedParens(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	baseTime := time.Unix(1_700_000_000, 0)
	CurrentNowFunc = func() time.Time { return baseTime }

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Claim me")
	if err := RunClaim(db, id, "45m", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [ ] `" + id + "` Claim me (claimed by alice, 45m left)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_BlockedParens(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	if err := RunBlock(db, b, a, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	if !strings.Contains(got, "`"+b+"` B (blocked on "+a+")") {
		t.Errorf("expected blocked parens:\n%s", got)
	}
}

func TestList_NestedIndentation(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	child := MustAdd(t, db, parent, "Child")
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	wantParent := "- [ ] `" + parent + "` Parent\n"
	wantChild := "  - [ ] `" + child + "` Child\n"
	if !strings.Contains(got, wantParent) {
		t.Errorf("missing parent line:\n%s", got)
	}
	if !strings.Contains(got, wantChild) {
		t.Errorf("missing indented child line:\n%s", got)
	}
}

func TestRenderListEmpty_Fresh(t *testing.T) {
	var buf bytes.Buffer
	RenderListEmpty(&buf, 0, 0)
	want := "No tasks. Run 'job import plan.md' or 'job --as <name> add \"<title>\"' to get started.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRenderListEmpty_AllDone(t *testing.T) {
	var buf bytes.Buffer
	RenderListEmpty(&buf, 3, 3)
	want := "Nothing actionable. 3 tasks done. Run 'list all' to see the full tree.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// A `--force-close-with-pending` close records the deferred labels under
// "criteria_waived" (event_payloads.go, DonePayload.CriteriaWaived) — they
// show in `job log --format=json` today, but the text renderer silently
// dropped them, so a reviewer reading the text log has no way to tell a
// close waived anything.
func TestFormatEvent_Done_CriteriaWaived_Renders(t *testing.T) {
	detailJSON := `{"note":"shipping anyway","criteria_waived":["tests pass","docs updated"],"was_status":"available"}`
	out := FormatEventDescription("done", detailJSON)
	if !strings.Contains(out, "waived: tests pass, docs updated") {
		t.Errorf("done should render waived criteria, got %q", out)
	}
}

// No criteria_waived key: the ordinary close renders exactly as before, with
// no "waived" clause at all.
func TestFormatEvent_Done_NoWaivedCriteria_OmitsClause(t *testing.T) {
	detailJSON := `{"note":"all green","was_status":"available"}`
	out := FormatEventDescription("done", detailJSON)
	if strings.Contains(out, "waived") {
		t.Errorf("done with no waived criteria should not mention waiving, got %q", out)
	}
}

// TestFormatDuration pins leaf TUVIbz(2): FormatDuration truncated to whole
// hours, so a 2h claim read "expires in 1h" a minute after it was taken —
// the 59 remaining minutes were silently dropped. Below an hour and at/above
// a day the existing granularity (whole minutes, whole days) is unchanged;
// only the hours bucket gains a minutes remainder.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		seconds int64
		want    string
	}{
		{0, "0s"},
		{45, "45s"},
		{59, "59s"},
		{60, "1m"},
		{90, "1m"},
		{1799, "29m"},
		{1800, "30m"},
		{3599, "59m"},
		{3600, "1h"},
		{3660, "1h 1m"},
		{7140, "1h 59m"},
		{7200, "2h"},
		{86399, "23h 59m"},
		{86400, "1d"},
		{90000, "1d"},
		{172800, "2d"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.seconds); got != c.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", c.seconds, got, c.want)
		}
	}
}

// TestList_ClaimedParens_ShowsMinutesRemainder is the reported symptom end to
// end: a 2h claim, a minute later, reads "1h 59m left" in `job ls`, not the
// truncated "1h left".
func TestList_ClaimedParens_ShowsMinutesRemainder(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	baseTime := time.Unix(1_700_000_000, 0)
	CurrentNowFunc = func() time.Time { return baseTime }

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Claim me")
	if err := RunClaim(db, id, "2h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	CurrentNowFunc = func() time.Time { return baseTime.Add(time.Minute) }

	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [ ] `" + id + "` Claim me (claimed by alice, 1h 59m left)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
