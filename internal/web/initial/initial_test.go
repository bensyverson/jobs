package initial_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/initial"
)

func TestLoad_EmptyDatabase(t *testing.T) {
	db := job.SetupTestDB(t)

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.HeadPosition != "" {
		t.Errorf("HeadPosition = %q, want empty", f.HeadPosition)
	}
	if f.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", f.EventCount)
	}
	if len(f.Tasks) != 0 {
		t.Errorf("Tasks len = %d, want 0", len(f.Tasks))
	}
	if len(f.Blocks) != 0 {
		t.Errorf("Blocks len = %d, want 0", len(f.Blocks))
	}
	if len(f.Claims) != 0 {
		t.Errorf("Claims len = %d, want 0", len(f.Claims))
	}
}

func TestLoad_TasksWithLabelsAndParents(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "parent task")
	child := job.MustAdd(t, db, parent, "child task")
	if _, err := job.RunLabelAdd(db, parent, []string{"web", "alpha"}, job.TestActor); err != nil {
		t.Fatalf("RunLabelAdd: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var got map[string]initial.TaskState
	got = make(map[string]initial.TaskState, len(f.Tasks))
	for _, ts := range f.Tasks {
		got[ts.ShortID] = ts
	}

	p, ok := got[parent]
	if !ok {
		t.Fatalf("parent task %s missing from frame", parent)
	}
	if p.Title != "parent task" {
		t.Errorf("parent title = %q, want %q", p.Title, "parent task")
	}
	if p.ParentShortID != nil {
		t.Errorf("parent.ParentShortID = %v, want nil", p.ParentShortID)
	}
	if len(p.Labels) != 2 || p.Labels[0] != "alpha" || p.Labels[1] != "web" {
		t.Errorf("parent.Labels = %v, want [alpha web] (sorted)", p.Labels)
	}

	c, ok := got[child]
	if !ok {
		t.Fatalf("child task %s missing from frame", child)
	}
	if c.ParentShortID == nil || *c.ParentShortID != parent {
		t.Errorf("child.ParentShortID = %v, want %q", c.ParentShortID, parent)
	}
}

func TestLoad_IncludesDoneAndCanceledTasks(t *testing.T) {
	db := job.SetupTestDB(t)
	idDone := job.MustAdd(t, db, "", "will be done")
	idCancel := job.MustAdd(t, db, "", "will be canceled")
	idLive := job.MustAdd(t, db, "", "still live")

	job.MustDone(t, db, idDone)
	if _, _, _, err := job.RunCancel(db, []string{idCancel}, "no reason", false, false, false, job.TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	statuses := make(map[string]string)
	for _, ts := range f.Tasks {
		statuses[ts.ShortID] = ts.Status
	}

	// Done and canceled tasks must be present in the head frame so
	// reverseEvent can flip their status when the scrubber walks back.
	if statuses[idDone] != "done" {
		t.Errorf("done task status = %q, want %q", statuses[idDone], "done")
	}
	if statuses[idCancel] != "canceled" {
		t.Errorf("canceled task status = %q, want %q", statuses[idCancel], "canceled")
	}
	if statuses[idLive] != "available" {
		t.Errorf("live task status = %q, want %q", statuses[idLive], "available")
	}
}

func TestLoad_ExcludesPurgedTasks(t *testing.T) {
	db := job.SetupTestDB(t)
	idAlive := job.MustAdd(t, db, "", "alive")
	idPurged := job.MustAdd(t, db, "", "purged")
	if _, _, _, err := job.RunCancel(db, []string{idPurged}, "purging", false, true, true, job.TestActor); err != nil {
		t.Fatalf("RunCancel --purge: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ts := range f.Tasks {
		if ts.ShortID == idPurged {
			t.Errorf("purged task %s should not be in frame", idPurged)
		}
	}
	// Sanity: the alive task is still there.
	found := false
	for _, ts := range f.Tasks {
		if ts.ShortID == idAlive {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("alive task %s missing from frame", idAlive)
	}
}

func TestLoad_ActiveClaims(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "claimed task")
	job.MustClaim(t, db, id, "30m")

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Claims) != 1 {
		t.Fatalf("Claims len = %d, want 1", len(f.Claims))
	}
	c := f.Claims[0]
	if c.ShortID != id {
		t.Errorf("claim shortID = %q, want %q", c.ShortID, id)
	}
	if c.ClaimedBy != job.TestActor {
		t.Errorf("claim claimedBy = %q, want %q", c.ClaimedBy, job.TestActor)
	}
	if c.ExpiresAt <= 0 {
		t.Errorf("claim expiresAt = %d, want > 0", c.ExpiresAt)
	}
}

func TestLoad_ActiveBlocks(t *testing.T) {
	db := job.SetupTestDB(t)
	idA := job.MustAdd(t, db, "", "blocked task")
	idB := job.MustAdd(t, db, "", "blocker task")
	if err := job.RunBlockMany(db, idA, []string{idB}, job.TestActor); err != nil {
		t.Fatalf("RunBlockMany: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Blocks) != 1 {
		t.Fatalf("Blocks len = %d, want 1", len(f.Blocks))
	}
	e := f.Blocks[0]
	if e.BlockedShortID != idA || e.BlockerShortID != idB {
		t.Errorf("edge = (%s, %s), want (%s, %s)", e.BlockedShortID, e.BlockerShortID, idA, idB)
	}
}

func TestLoad_TasksWithCriteria(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "task with criteria")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "alpha"},
		{Label: "beta"},
		{Label: "gamma"},
	}, job.TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "beta", job.CriterionPassed, job.TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var got *initial.TaskState
	for i := range f.Tasks {
		if f.Tasks[i].ShortID == id {
			got = &f.Tasks[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("task %s missing from frame", id)
	}
	if len(got.Criteria) != 3 {
		t.Fatalf("Criteria len = %d, want 3", len(got.Criteria))
	}
	want := []initial.CriterionState{
		{Label: "alpha", State: "pending"},
		{Label: "beta", State: "passed"},
		{Label: "gamma", State: "pending"},
	}
	for i, w := range want {
		if got.Criteria[i] != w {
			t.Errorf("Criteria[%d] = %+v, want %+v", i, got.Criteria[i], w)
		}
	}
}

func TestLoad_TasksWithoutCriteriaSerializeAsEmptyArray(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "criterion-less task")

	raw, err := initial.LoadJSON(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	// We want criteria: [] not criteria: null so the JS consumer can
	// always treat it as iterable. Spot-check via JSON parse.
	var parsed struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, ts := range parsed.Tasks {
		if ts["shortId"] == id {
			c, ok := ts["criteria"]
			if !ok {
				t.Fatalf("criteria field missing on task %s", id)
			}
			arr, ok := c.([]any)
			if !ok {
				t.Fatalf("criteria field on task %s is %T, want []any", id, c)
			}
			if len(arr) != 0 {
				t.Errorf("criteria len = %d, want 0", len(arr))
			}
			return
		}
	}
	t.Fatalf("task %s missing from JSON island", id)
}

func TestLoadJSON_HTMLSafeAgainstScriptInjection(t *testing.T) {
	db := job.SetupTestDB(t)
	// A task whose title contains a literal </script> sequence — the
	// classic XSS smuggling payload for JSON islands. The encoder
	// should escape the < to < so the browser's HTML parser
	// can't terminate the surrounding <script> early.
	job.MustAdd(t, db, "", `nope</script><script>alert(1)`)

	raw, err := initial.LoadJSON(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if strings.Contains(string(raw), "</script>") {
		t.Errorf("LoadJSON output contains a literal </script> — XSS risk:\n%s", raw)
	}
	// Sanity: the encoded form is still valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Errorf("LoadJSON output not valid JSON: %v\n%s", err, raw)
	}
}

func TestLoadJSON_TrailingNewlineTrimmed(t *testing.T) {
	db := job.SetupTestDB(t)
	raw, err := initial.LoadJSON(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if strings.HasSuffix(string(raw), "\n") {
		t.Errorf("LoadJSON output should not end with newline:\n%q", raw)
	}
}

func TestLoadJSON_HeadPositionAdvancesWithEvents(t *testing.T) {
	db := job.SetupTestDB(t)
	first, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	job.MustAdd(t, db, "", "creates an event")
	second, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if second.HeadPosition == "" || second.HeadPosition == first.HeadPosition {
		t.Errorf("HeadPosition should advance: %q -> %q", first.HeadPosition, second.HeadPosition)
	}
	if _, err := eventlog.ParsePosition(second.HeadPosition); err != nil {
		t.Errorf("HeadPosition %q does not parse: %v", second.HeadPosition, err)
	}
	if second.EventCount != first.EventCount+1 {
		t.Errorf("EventCount should advance by one: %d -> %d", first.EventCount, second.EventCount)
	}
}

func TestLoad_KindOnRootsOnly(t *testing.T) {
	db := job.SetupTestDB(t)
	taskRoot := job.MustAdd(t, db, "", "task root")
	issueRoot := job.MustAdd(t, db, "", "issue root")
	child := job.MustAdd(t, db, issueRoot, "child of an issue root")
	if _, err := job.RunSetKind(db, issueRoot, job.KindIssue, job.TestActor); err != nil {
		t.Fatalf("RunSetKind: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := make(map[string]initial.TaskState, len(f.Tasks))
	for _, ts := range f.Tasks {
		got[ts.ShortID] = ts
	}

	if got[taskRoot].Kind != "task" {
		t.Errorf("task root Kind = %q, want %q", got[taskRoot].Kind, "task")
	}
	if got[issueRoot].Kind != "issue" {
		t.Errorf("issue root Kind = %q, want %q", got[issueRoot].Kind, "issue")
	}
	// Kind is a property of the root only; children carry none so the
	// client can tell "child" from "task-tree root" without a lookup.
	if got[child].Kind != "" {
		t.Errorf("child Kind = %q, want empty", got[child].Kind)
	}
}

func TestLoad_FoundInEdgeOnSurfacedTask(t *testing.T) {
	db := job.SetupTestDB(t)
	source := job.MustAdd(t, db, "", "the leaf that surfaced it")
	surfaced := job.MustAdd(t, db, "", "the defect")
	unrelated := job.MustAdd(t, db, "", "no provenance")
	if err := job.RunSetFoundIn(db, surfaced, source, job.TestActor); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}

	f, err := initial.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := make(map[string]initial.TaskState, len(f.Tasks))
	for _, ts := range f.Tasks {
		got[ts.ShortID] = ts
	}

	if got[surfaced].FoundIn != source {
		t.Errorf("surfaced.FoundIn = %q, want %q", got[surfaced].FoundIn, source)
	}
	if got[unrelated].FoundIn != "" {
		t.Errorf("unrelated.FoundIn = %q, want empty", got[unrelated].FoundIn)
	}
	if got[source].FoundIn != "" {
		t.Errorf("source.FoundIn = %q, want empty", got[source].FoundIn)
	}
}

func TestLoadJSON_CarriesKindAndFoundIn(t *testing.T) {
	db := job.SetupTestDB(t)
	issueRoot := job.MustAdd(t, db, "", "issue root")
	if _, err := job.RunSetKind(db, issueRoot, job.KindIssue, job.TestActor); err != nil {
		t.Fatalf("RunSetKind: %v", err)
	}
	source := job.MustAdd(t, db, "", "source leaf")
	if err := job.RunSetFoundIn(db, issueRoot, source, job.TestActor); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}

	raw, err := initial.LoadJSON(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	var parsed struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, ts := range parsed.Tasks {
		if ts["shortId"] != issueRoot {
			continue
		}
		if ts["kind"] != "issue" {
			t.Errorf("island kind = %v, want %q", ts["kind"], "issue")
		}
		if ts["foundIn"] != source {
			t.Errorf("island foundIn = %v, want %q", ts["foundIn"], source)
		}
		return
	}
	t.Fatalf("task %s missing from JSON island", issueRoot)
}

func TestLoadJSON_OmitsFoundInWhenAbsent(t *testing.T) {
	db := job.SetupTestDB(t)
	// A plain child: no kind (roots only) and no provenance edge.
	// Both fields stay off the wire rather than encoding as "".
	root := job.MustAdd(t, db, "", "root")
	child := job.MustAdd(t, db, root, "child")

	raw, err := initial.LoadJSON(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	var parsed struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, ts := range parsed.Tasks {
		if ts["shortId"] != child {
			continue
		}
		if _, ok := ts["kind"]; ok {
			t.Errorf("child carries kind = %v, want the key absent", ts["kind"])
		}
		if _, ok := ts["foundIn"]; ok {
			t.Errorf("child carries foundIn = %v, want the key absent", ts["foundIn"])
		}
		return
	}
	t.Fatalf("task %s missing from JSON island", child)
}
