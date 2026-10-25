package handlers_test

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// A reworded criterion reads as prose on both surfaces that list events: the
// Log row folds old → new into its metadata cell under a REWORDED verb, and
// the task's History says what the wording was and became.

func rewordedFixture(t *testing.T) (deps handlers.Deps, id string) {
	t.Helper()
	db := setupLogTestDB(t)
	id = mustAdd(t, db, "alice", "subject", nil, nil)
	crits, err := job.RunAddCriteria(db, id, []job.Criterion{{Label: "tests pass"}}, "alice")
	if err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunRewordCriterion(db, id, crits[0].ShortID, "all tests pass", "alice"); err != nil {
		t.Fatalf("RunRewordCriterion: %v", err)
	}
	return newLogDeps(t, db), id
}

func TestLog_CriterionRewordedRendersVerbAndOldToNew(t *testing.T) {
	deps, _ := rewordedFixture(t)
	body := fetchLog(t, deps, "")

	if strings.Contains(body, `criterion_reworded</span>`) {
		t.Errorf("log verb span should not render the raw 'criterion_reworded' enum")
	}
	mustContain(t, body, `c-log-row__verb--criterion_reworded">reworded<`)
	mustContain(t, body, `data-event-meta="criterion_reworded">tests pass → all tests pass</span>`)
}

func TestTask_HistoryRendersCriterionRewordedAsHumanVerb(t *testing.T) {
	deps, id := rewordedFixture(t)
	body := stripInitialFrame(fetchTask(t, deps, id).Body.String())

	if strings.Contains(body, "criterion_reworded by") {
		t.Errorf("History should not surface raw 'criterion_reworded by'\n%s", body)
	}
	mustContain(t, body, `reworded &#34;tests pass&#34; to &#34;all tests pass&#34; by`)
}
