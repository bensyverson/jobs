package job

import "database/sql"

// buildOrientHeader synthesizes the orient header for the target: the target
// in full, its open dependencies in both directions, and its sibling leaves.
// Notes come from substantiveNotes so a completion note folds in when the
// target is itself done (RunInfo.Notes carries only `noted` events).
func buildOrientHeader(db *sql.DB, target, root *Task) (*OrientHeader, error) {
	info, err := RunInfo(db, target.ShortID)
	if err != nil {
		return nil, err
	}
	status, _, err := orientStatus(db, target)
	if err != nil {
		return nil, err
	}
	notes, err := substantiveNotes(db, target)
	if err != nil {
		return nil, err
	}
	blockedBy, err := orientDeps(db, info.Blockers)
	if err != nil {
		return nil, err
	}
	blocks, err := orientDeps(db, info.Blocked)
	if err != nil {
		return nil, err
	}
	siblings, err := orientSiblings(db, target)
	if err != nil {
		return nil, err
	}

	return &OrientHeader{
		Target:    target.ShortID,
		Title:     target.Title,
		Root:      root.ShortID,
		Status:    status,
		Desc:      target.Description,
		Criteria:  info.Criteria,
		Notes:     notes,
		BlockedBy: blockedBy,
		Blocks:    blocks,
		Siblings:  siblings,
	}, nil
}

// orientDeps projects linked tasks with their rendered status and newest
// substantive note.
func orientDeps(db *sql.DB, tasks []*Task) ([]OrientDep, error) {
	var out []OrientDep
	for _, t := range tasks {
		status, _, err := orientStatus(db, t)
		if err != nil {
			return nil, err
		}
		notes, err := substantiveNotes(db, t)
		if err != nil {
			return nil, err
		}
		dep := OrientDep{ID: t.ShortID, Title: t.Title, Status: status}
		if len(notes) > 0 {
			dep.LatestNote = notes[len(notes)-1].Text
		}
		out = append(out, dep)
	}
	return out, nil
}

// orientSiblings returns the target's same-parent sibling leaves in plan
// order. Branch siblings (those with children) are excluded: they are whole
// slices of the plan, already in the tree, not neighbors to weigh.
func orientSiblings(db *sql.DB, target *Task) ([]OrientSibling, error) {
	if target.ParentID == nil {
		return nil, nil
	}
	siblings, err := getChildren(db, *target.ParentID)
	if err != nil {
		return nil, err
	}
	var out []OrientSibling
	for _, s := range siblings {
		if s.ID == target.ID {
			continue
		}
		children, err := getChildren(db, s.ID)
		if err != nil {
			return nil, err
		}
		if len(children) > 0 {
			continue
		}
		status, _, err := orientStatus(db, s)
		if err != nil {
			return nil, err
		}
		notes, err := substantiveNotes(db, s)
		if err != nil {
			return nil, err
		}
		out = append(out, OrientSibling{ID: s.ShortID, Title: s.Title, Status: status, Notes: len(notes)})
	}
	return out, nil
}
