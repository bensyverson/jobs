package job

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// reportFixture drives the real handlers against a store whose clock the
// test moves, so every event lands at a moment the test chose.
type reportFixture struct {
	t   *testing.T
	db  *sql.DB
	now time.Time
}

// reportDay0 is a Monday, so day buckets and week buckets start together.
var reportDay0 = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func newReportFixture(t *testing.T) *reportFixture {
	t.Helper()
	f := &reportFixture{t: t, db: SetupTestDB(t), now: reportDay0}
	restore := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return f.now }
	t.Cleanup(func() { CurrentNowFunc = restore })
	return f
}

// day is midnight UTC n days after reportDay0, plus an optional offset.
func day(n int, plus ...time.Duration) time.Time {
	t := reportDay0.AddDate(0, 0, n)
	for _, p := range plus {
		t = t.Add(p)
	}
	return t
}

func (f *reportFixture) at(t time.Time) *reportFixture { f.now = t; return f }

func (f *reportFixture) add(parent, title string) string {
	f.t.Helper()
	return MustAdd(f.t, f.db, parent, title)
}

func (f *reportFixture) issueRoot(title string) string {
	f.t.Helper()
	res, err := RunAddKind(f.db, "", title, "", "", nil, TestActor, KindIssue)
	if err != nil {
		f.t.Fatalf("add issue root: %v", err)
	}
	return res.ShortID
}

func (f *reportFixture) done(id string) { f.doneAs(id, TestActor) }

func (f *reportFixture) doneAs(id, actor string) {
	f.t.Helper()
	if _, _, err := RunDone(f.db, []string{id}, false, "", nil, actor, false, ""); err != nil {
		f.t.Fatalf("done %s: %v", id, err)
	}
}

func (f *reportFixture) reopen(id string) {
	f.t.Helper()
	if _, err := RunReopen(f.db, id, false, TestActor); err != nil {
		f.t.Fatalf("reopen %s: %v", id, err)
	}
}

func (f *reportFixture) cancel(id string) {
	f.t.Helper()
	if _, _, _, err := RunCancel(f.db, []string{id}, "not needed", false, false, true, TestActor); err != nil {
		f.t.Fatalf("cancel %s: %v", id, err)
	}
}

func (f *reportFixture) purge(id string) {
	f.t.Helper()
	if _, _, _, err := RunCancel(f.db, []string{id}, "mistake", true, true, true, TestActor); err != nil {
		f.t.Fatalf("purge %s: %v", id, err)
	}
}

func (f *reportFixture) claim(id string) { f.claimAs(id, TestActor) }

func (f *reportFixture) claimAs(id, actor string) {
	f.t.Helper()
	if err := RunClaim(f.db, id, "72h", "", actor, false); err != nil {
		f.t.Fatalf("claim %s: %v", id, err)
	}
}

func (f *reportFixture) releaseAs(id, actor string) {
	f.t.Helper()
	if err := RunRelease(f.db, id, "", actor); err != nil {
		f.t.Fatalf("release %s: %v", id, err)
	}
}

func (f *reportFixture) block(blocked, blocker string) {
	f.t.Helper()
	if err := RunBlockMany(f.db, blocked, []string{blocker}, TestActor); err != nil {
		f.t.Fatalf("block %s by %s: %v", blocked, blocker, err)
	}
}

func (f *reportFixture) split(id string, titles ...string) []string {
	f.t.Helper()
	res, err := RunSplit(f.db, id, titles, TestActor)
	if err != nil {
		f.t.Fatalf("split %s: %v", id, err)
	}
	return res.ChildShortIDs
}

func (f *reportFixture) reparent(id, newParent string) {
	f.t.Helper()
	if err := RunReparent(f.db, id, newParent, "", "", TestActor); err != nil {
		f.t.Fatalf("reparent %s under %s: %v", id, newParent, err)
	}
}

// importPlan writes plan to a file named source and imports it, nested under
// parent when parent is non-empty. It returns the created ids in plan order.
func (f *reportFixture) importPlan(source, parent, plan string) []string {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), source)
	if err := os.WriteFile(path, []byte(plan), 0o644); err != nil {
		f.t.Fatal(err)
	}
	res, err := RunImport(f.db, path, parent, false, TestActor)
	if err != nil {
		f.t.Fatalf("import %s: %v", source, err)
	}
	ids := make([]string, len(res.Tasks))
	for i, task := range res.Tasks {
		ids[i] = task.ID
	}
	return ids
}

func (f *reportFixture) report(q ReportQuery) Report {
	f.t.Helper()
	if q.Location == nil {
		q.Location = time.UTC
	}
	r, err := BuildReport(f.db, q)
	if err != nil {
		f.t.Fatalf("BuildReport: %v", err)
	}
	return r
}

// daily is the common query: day buckets over [day(0), day(n)] in UTC.
func daily(n int) ReportQuery {
	return ReportQuery{Since: day(0), Until: day(n), Bucket: BucketDay, Location: time.UTC}
}

// sampleEnding returns the sample whose End is end.
func sampleEnding(t *testing.T, r Report, end time.Time) Sample {
	t.Helper()
	for _, s := range r.Series {
		if s.End.Equal(end) {
			return s
		}
	}
	t.Fatalf("no sample ends at %s; series ends %v", end, seriesEnds(r))
	return Sample{}
}

func seriesEnds(r Report) []time.Time {
	out := make([]time.Time, len(r.Series))
	for i, s := range r.Series {
		out[i] = s.End
	}
	return out
}

//go:fix inline
func secondsPtr(v int64) *int64 { return new(v) }

func int64PtrString(p *int64) string {
	if p == nil {
		return "nil"
	}
	return time.Duration(*p * int64(time.Second)).String()
}
