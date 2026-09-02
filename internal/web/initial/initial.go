// Package initial loads the head-frame snapshot the dashboard's
// time-travel scrubber hydrates from. Output is a JSON byte slice
// HTML-safe-escaped for embedding inside a <script type="application/json">
// island in the page layout. The shape mirrors the input contract of
// initialFrame() in internal/web/assets/js/replay.mjs.
//
// Why a snapshot of all non-deleted tasks (not just open ones): the
// JS reverse-fold needs every task that ever existed to be present
// in the head frame so that walking back through a `done` or
// `canceled` event can flip the task's status without it vanishing.
// Purged tasks are gone from history by design and stay excluded.
package initial

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

// Frame is the JSON shape the JS reducer's initialFrame() consumes.
// Field tags match the JS field names exactly — the server is
// authoritative on the wire format.
type Frame struct {
	HeadEventID int64        `json:"headEventId"`
	Tasks       []TaskState  `json:"tasks"`
	Blocks      []BlockEdge  `json:"blocks"`
	Claims      []ClaimState `json:"claims"`
}

// TaskState describes one task at the head moment. parentShortId is
// nullable; when the task has no parent, the field encodes as null
// (Go nil string pointer). labels is a flat list — order matches the
// task_labels insertion order to keep the wire output deterministic.
// criteria mirrors the task_criteria rows in sort order so the JS
// reducer can render the checklist and apply criterion_state events
// without re-querying.
//
// kind is carried on roots only ("task" or "issue") and omitted on
// children, so a client can tell a task-tree root from a child of one
// without walking to the parent. foundIn is the short id of the task
// that surfaced this one, omitted when there is no edge. Both field
// names match `job show --format=json` so one parser reads either.
type TaskState struct {
	ShortID       string           `json:"shortId"`
	Title         string           `json:"title"`
	Description   string           `json:"description"`
	Status        string           `json:"status"`
	ParentShortID *string          `json:"parentShortId"`
	SortKey       string           `json:"sortKey"`
	Labels        []string         `json:"labels"`
	Criteria      []CriterionState `json:"criteria"`
	Kind          string           `json:"kind,omitempty"`
	FoundIn       string           `json:"foundIn,omitempty"`
}

// CriterionState is one acceptance-criterion row in the JSON island,
// in the wire shape the JS reducer consumes.
type CriterionState struct {
	Label string `json:"label"`
	State string `json:"state"`
}

// BlockEdge is one (blocked, blocker) edge active at the head moment.
// Edges where the blocker is done or deleted are excluded by the
// loader — the JS reducer treats blocks as a set of *active* edges,
// matching the live web views.
type BlockEdge struct {
	BlockedShortID string `json:"blockedShortId"`
	BlockerShortID string `json:"blockerShortId"`
}

// ClaimState is one currently held claim. Only tasks with status
// "claimed" and a non-null claimed_by are included; expired claims
// that haven't been swept yet land here too because the dashboard
// renders them as live until the sweep records the claim_expired
// event.
type ClaimState struct {
	ShortID   string `json:"shortId"`
	ClaimedBy string `json:"claimedBy"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Load builds a Frame from the current database state. Three small
// queries — one per relation — composed into the wire shape. Cheap
// enough to run on every page render at the dashboard's local-first
// scale; a future optimization could cache it between events arriving
// on the broadcaster.
func Load(ctx context.Context, db *sql.DB) (Frame, error) {
	var f Frame

	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM events`,
	).Scan(&f.HeadEventID); err != nil {
		return f, fmt.Errorf("head event id: %w", err)
	}

	tasks, err := loadTasks(ctx, db)
	if err != nil {
		return f, err
	}
	f.Tasks = tasks

	blocks, err := loadBlocks(ctx, db)
	if err != nil {
		return f, err
	}
	f.Blocks = blocks

	claims, err := loadClaims(ctx, db)
	if err != nil {
		return f, err
	}
	f.Claims = claims

	return f, nil
}

// LoadJSON wraps Load with HTML-safe JSON encoding suitable for
// embedding inside a <script type="application/json"> island. The
// json.Encoder escapes <, >, and & by default (SetEscapeHTML(true)),
// which is exactly what we need to defeat </script> injection inside
// task titles or descriptions. The trailing newline that
// Encoder.Encode emits is trimmed so the output is a single line.
func LoadJSON(ctx context.Context, db *sql.DB) ([]byte, error) {
	f, err := Load(ctx, db)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encode initial frame: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func loadTasks(ctx context.Context, db *sql.DB) ([]TaskState, error) {
	// Tasks: every non-deleted task. The JS reducer needs done/
	// canceled tasks present so reverseEvent on a done event can flip
	// the status without dropping the task from the frame. Parent
	// short id resolved in the same query via self-join.
	rows, err := db.QueryContext(ctx, `
		SELECT t.short_id, t.title, t.description, t.status,
		       p.short_id, t.sort_key, t.kind
		FROM tasks t
		LEFT JOIN tasks p ON p.id = t.parent_id
		WHERE t.deleted_at IS NULL
		ORDER BY t.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("tasks query: %w", err)
	}
	defer rows.Close()

	var tasks []TaskState
	idsByShort := make(map[string]int)
	for rows.Next() {
		var ts TaskState
		var parent sql.NullString
		var kind sql.NullString
		if err := rows.Scan(&ts.ShortID, &ts.Title, &ts.Description, &ts.Status, &parent, &ts.SortKey, &kind); err != nil {
			return nil, err
		}
		if parent.Valid {
			s := parent.String
			ts.ParentShortID = &s
		} else if kind.Valid {
			// Roots only. A child's kind column is meaningless — the
			// tree's kind lives on its root — so it stays off the wire.
			ts.Kind = kind.String
		}
		ts.Labels = []string{}
		ts.Criteria = []CriterionState{}
		idsByShort[ts.ShortID] = len(tasks)
		tasks = append(tasks, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Labels: one row per (task, label). Group into the matching
	// TaskState by short id. Sorted lexically so the wire output is
	// deterministic regardless of insertion order — important for
	// snapshot-style tests.
	lrows, err := db.QueryContext(ctx, `
		SELECT t.short_id, l.name
		FROM task_labels l
		JOIN tasks t ON t.id = l.task_id
		WHERE t.deleted_at IS NULL
		ORDER BY t.short_id, l.name
	`)
	if err != nil {
		return nil, fmt.Errorf("labels query: %w", err)
	}
	defer lrows.Close()

	for lrows.Next() {
		var shortID, name string
		if err := lrows.Scan(&shortID, &name); err != nil {
			return nil, err
		}
		idx, ok := idsByShort[shortID]
		if !ok {
			continue
		}
		tasks[idx].Labels = append(tasks[idx].Labels, name)
	}
	if err := lrows.Err(); err != nil {
		return nil, err
	}
	for i := range tasks {
		sort.Strings(tasks[i].Labels)
	}

	// Criteria: one row per (task, criterion) in sort order so the JS
	// reducer can render the checklist as the CLI does. Sort by
	// (task_id, sort_key, id) for deterministic output.
	crows, err := db.QueryContext(ctx, `
		SELECT t.short_id, c.label, c.state
		FROM task_criteria c
		JOIN tasks t ON t.id = c.task_id
		WHERE t.deleted_at IS NULL
		ORDER BY t.short_id, c.sort_key, c.id
	`)
	if err != nil {
		return nil, fmt.Errorf("criteria query: %w", err)
	}
	defer crows.Close()

	for crows.Next() {
		var shortID, label, state string
		if err := crows.Scan(&shortID, &label, &state); err != nil {
			return nil, err
		}
		idx, ok := idsByShort[shortID]
		if !ok {
			continue
		}
		tasks[idx].Criteria = append(tasks[idx].Criteria, CriterionState{
			Label: label,
			State: state,
		})
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Found-in: the provenance edge lives in its own table, one source
	// per task. Both ends are resolved to short ids here so the client
	// never needs an internal id. The source is joined unconditionally
	// — the edge outlives the source's close and soft delete by design
	// — but a task the frame excludes cannot carry one, so the
	// surfaced end is filtered to match the task query above.
	frows, err := db.QueryContext(ctx, `
		SELECT t.short_id, s.short_id
		FROM found_in f
		JOIN tasks t ON t.id = f.task_id
		JOIN tasks s ON s.id = f.source_id
		WHERE t.deleted_at IS NULL
		ORDER BY t.short_id
	`)
	if err != nil {
		return nil, fmt.Errorf("found_in query: %w", err)
	}
	defer frows.Close()

	for frows.Next() {
		var shortID, sourceID string
		if err := frows.Scan(&shortID, &sourceID); err != nil {
			return nil, err
		}
		idx, ok := idsByShort[shortID]
		if !ok {
			continue
		}
		tasks[idx].FoundIn = sourceID
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func loadBlocks(ctx context.Context, db *sql.DB) ([]BlockEdge, error) {
	// Active block edges: blocked task open + not deleted, blocker
	// task open + not deleted. Done blockers are auto-removed by the
	// done path (and edges are dropped on cancel too), so this filter
	// matches the live block strip on Home and the Plan view's
	// "blocked by" rendering.
	rows, err := db.QueryContext(ctx, `
		SELECT bt.short_id, kt.short_id
		FROM blocks b
		JOIN tasks bt ON bt.id = b.blocked_id
		JOIN tasks kt ON kt.id = b.blocker_id
		WHERE bt.deleted_at IS NULL
		  AND bt.status NOT IN ('done', 'canceled')
		  AND kt.deleted_at IS NULL
		  AND kt.status != 'done'
		ORDER BY bt.short_id, kt.short_id
	`)
	if err != nil {
		return nil, fmt.Errorf("blocks query: %w", err)
	}
	defer rows.Close()

	var edges []BlockEdge
	for rows.Next() {
		var e BlockEdge
		if err := rows.Scan(&e.BlockedShortID, &e.BlockerShortID); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}

func loadClaims(ctx context.Context, db *sql.DB) ([]ClaimState, error) {
	// Claims: every task currently in 'claimed' status with a non-
	// null claimer. claim_expires_at can theoretically be NULL on a
	// claimed row (legacy data); coalesce to 0 in that case so the
	// JSON encodes a number consistently.
	rows, err := db.QueryContext(ctx, `
		SELECT short_id, claimed_by, COALESCE(claim_expires_at, 0)
		FROM tasks
		WHERE status = 'claimed'
		  AND claimed_by IS NOT NULL
		  AND deleted_at IS NULL
		ORDER BY short_id
	`)
	if err != nil {
		return nil, fmt.Errorf("claims query: %w", err)
	}
	defer rows.Close()

	var claims []ClaimState
	for rows.Next() {
		var c ClaimState
		if err := rows.Scan(&c.ShortID, &c.ClaimedBy, &c.ExpiresAt); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return claims, nil
}
