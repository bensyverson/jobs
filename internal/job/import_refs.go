package job

import (
	"database/sql"
	"fmt"
)

// resolvedRef is one end of a cross-task reference: either a task this import
// is about to create (local) or a row already in the database (dbTask).
type resolvedRef struct {
	local  *parsedTask
	dbTask *Task
}

// importIndex holds the lookups a cross-task reference resolves against.
type importIndex struct {
	refs        map[string]*parsedTask
	titles      map[string]*parsedTask
	titleCounts map[string]int
}

// resolve turns one reference entry into the task it names, trying in order:
// a ref defined in this import, an unambiguous verbatim title in this import,
// then a short ID already in the database. field names the key for the error
// message ("blockedBy[2]", "foundIn") so both grammar keys report failures the
// same way, against the same row.
func (ix importIndex) resolve(db *sql.DB, pathLabel, field, entry string) (resolvedRef, error) {
	if t, ok := ix.refs[entry]; ok {
		return resolvedRef{local: t}, nil
	}
	if ix.titleCounts[entry] >= 2 {
		return resolvedRef{}, fmt.Errorf(
			"%s: %s %q matches multiple tasks; use a ref or a short ID to disambiguate",
			pathLabel, field, entry,
		)
	}
	if t, ok := ix.titles[entry]; ok {
		return resolvedRef{local: t}, nil
	}
	existing, err := GetTaskByShortID(db, entry)
	if err != nil {
		return resolvedRef{}, err
	}
	if existing != nil {
		return resolvedRef{dbTask: existing}, nil
	}
	return resolvedRef{}, fmt.Errorf(
		"%s: %s %q does not match any ref, imported task title, or existing task ID",
		pathLabel, field, entry,
	)
}

// validateKinds enforces that `kind` appears only where it means something:
// on a task that becomes a root. tree holds the import's top-level entries, so
// anything outside it is a child of another imported task; underParent reports
// whether --parent was given, in which case even those top-level entries
// become children and no row may carry a kind.
func validateKinds(tree []*parsedTask, flat []*parsedTask, underParent bool, parentShortID string) error {
	roots := make(map[*parsedTask]bool, len(tree))
	for _, r := range tree {
		roots[r] = true
	}
	for _, p := range flat {
		if !p.kindPresent {
			continue
		}
		if underParent {
			return fmt.Errorf(
				"%s: kind is a property of a root only, and --parent %s makes every imported task a child; drop the key or import without --parent",
				p.pathLabel, parentShortID,
			)
		}
		if !roots[p] {
			return fmt.Errorf(
				"%s: kind is a property of the root only; set it on the import's root task instead",
				p.pathLabel,
			)
		}
	}
	return nil
}

// dryRunRefID renders a resolved reference for the dry-run echo: a
// `<new-N>` placeholder for a task this import would create, or the real
// short ID of a row that already exists.
func dryRunRefID(r resolvedRef) string {
	if r.local != nil {
		return fmt.Sprintf("<new-%d>", r.local.flatIndex+1)
	}
	if r.dbTask != nil {
		return r.dbTask.ShortID
	}
	return ""
}

// echoKind renders a tree kind for the import echo. The default is empty:
// `task` is what every root is unless it says otherwise, and naming it on
// every row would be noise — the same reason `show` prints no Kind line for a
// task-tree.
func echoKind(k TreeKind) string {
	if k.IsIssue() {
		return string(k)
	}
	return ""
}
