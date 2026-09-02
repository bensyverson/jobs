// replay-snapshot reproduces the server-side state of the dashboard
// graph at any historical log position.
//
// The dashboard's scrubber moves the cursor through past events; the
// JS replay buffer reduces SQLite events into a Frame and POSTs a
// SubwayInput to /home/graph. To diagnose "why does ?at=N look
// wrong" without a browser, this tool walks the events table the
// same way and prints the resulting SubwayInput plus the topological
// Subway model and rendered SubwayView.
//
// Usage:
//
//	go run ./scripts/replay-snapshot --db=.jobs.db --at=1756742400123-k7Qx2m-412
//
// --at is the same cursor the dashboard's ?at= carries: a log position
// (<ts>-<replica>-<seq>), not a row id, because a rebuild renumbers row
// ids. An empty --at (the default) replays every event — equivalent to
// the current world the live server would render.
//
// The sort key is taken from the current tasks table rather than from
// "created" event payloads. This mirrors the dashboard's JS replay,
// which rewinds from the current head rather than forward-replaying
// from event 1. Events recorded before sort keys existed carry an
// integer sort_order that no longer means anything, early `job import`
// runs wrote sort_order=0 in every "created" event regardless of the
// intended order, and the 2026-04-23 backfill SQL script repaired the
// tasks table directly without recording move events. A pure
// forward-from-1 replay would therefore walk those tasks in arbitrary
// order.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"

	_ "modernc.org/sqlite"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/signals"
)

func main() {
	var dbPath string
	var atRaw string
	var inputOnly bool
	flag.StringVar(&dbPath, "db", ".jobs.db", "path to the Jobs SQLite DB")
	flag.StringVar(&atRaw, "at", "", "replay through this log position, as ?at= carries it (empty = all events)")
	flag.BoolVar(&inputOnly, "input-only", false, "print only the SubwayInput JSON (useful for piping into /home/graph)")
	flag.Parse()

	var atEvent eventlog.Position
	if atRaw != "" {
		p, perr := eventlog.ParsePosition(atRaw)
		if perr != nil {
			log.Fatalf("--at: %v", perr)
		}
		atEvent = p
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	currentSortKeys, err := loadCurrentSortKeys(ctx, db)
	if err != nil {
		log.Fatalf("load current tasks: %v", err)
	}
	f, err := buildFrameAt(ctx, db, atEvent, currentSortKeys)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}
	in := frameToInput(f)

	if inputOnly {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(in); err != nil {
			log.Fatalf("encode input: %v", err)
		}
		return
	}

	at := "all"
	if atEvent != (eventlog.Position{}) {
		at = atEvent.String()
	}
	fmt.Printf("# replay-snapshot · db=%s · at=%s · last replayed event=%s\n",
		dbPath, at, f.position)
	fmt.Printf("# %d tasks · %d blocks\n\n", len(in.Tasks), len(in.Blocks))

	sub := signals.BuildSubwayFromInput(in)

	fmt.Println("## Subway.Lines")
	if len(sub.Lines) == 0 {
		fmt.Println("(no lines — empty subway, no focals)")
	}
	for i, line := range sub.Lines {
		fmt.Printf("Line %d  anchor=%s\n", i, summarizeNodeRef(in, line.AnchorShortID))
		for _, item := range line.Items {
			switch item.Kind {
			case signals.LineItemStop:
				fmt.Printf("    stop      %s\n", summarizeNodeRef(in, item.ShortID))
			case signals.LineItemElision:
				fmt.Printf("    elision   …\n")
			case signals.LineItemElisionBroken:
				fmt.Printf("    elision   ⋯ (broken-line)\n")
			case signals.LineItemElisionTerminating:
				fmt.Printf("    elision   ⋯ (terminating)\n")
			}
		}
	}

	if len(sub.Forks) > 0 {
		fmt.Println("\n## Subway.Forks")
		for i, fk := range sub.Forks {
			fmt.Printf("Fork %d  lines=%v\n", i, fk.LineIndices)
		}
	}

	fmt.Println("\n## Subway.Edges")
	for _, e := range sub.Edges {
		fmt.Printf("  %-9s %s → %s\n", edgeKind(e.Kind), e.FromShortID, e.ToShortID)
	}

	view := render.LayoutSubway(sub)
	fmt.Printf("\n## SubwayView · canvas=%d×%d\n", view.CanvasW, view.CanvasH)
	for _, n := range view.Nodes {
		var marks string
		if n.IsForkAncestor {
			marks += "F"
		}
		if n.IsLineAnchor {
			marks += "A"
		}
		if n.IsActive {
			marks += "*"
		}
		if n.IsDone {
			marks += "✓"
		}
		fmt.Printf("  Node %-6s %-3s @ (%d, %d)\n", n.ShortID, marks, n.Left, n.Top)
	}
	if focal := computeGlobalNext(in); focal != "" {
		fmt.Printf("\n## globalNext (focal when nothing is claimed)\n  %s\n", summarizeNodeRef(in, focal))
	}
	if claimed := claimedShortIDs(in); len(claimed) > 0 {
		fmt.Printf("\n## Claimed tasks (override globalNext as focals)\n")
		for _, s := range claimed {
			fmt.Printf("  %s\n", summarizeNodeRef(in, s))
		}
	}
}

// frame is the minimal state machine the JS replay buffer maintains.
// Only fields the Subway model reads are tracked; titles are kept
// for diagnostic output but aren't load-bearing.
//
// currentSortKeys is the live tasks-table sort_key map, threaded
// through so the "created" event handler can stamp the current key
// rather than whatever the historical event payload carried — see the
// package doc comment.
type frame struct {
	tasks           map[string]*ftask
	blocks          map[string]map[string]bool // blocked_short → set of blocker_short
	currentSortKeys map[string]string
	position        string
}

type ftask struct {
	shortID       string
	title         string
	parentShortID string
	sortKey       string
	status        string
	claimedBy     string
}

func loadCurrentSortKeys(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT short_id, sort_key FROM tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var short string
		var so string
		if err := rows.Scan(&short, &so); err != nil {
			return nil, err
		}
		out[short] = so
	}
	return out, rows.Err()
}

func buildFrameAt(ctx context.Context, db *sql.DB, atEvent eventlog.Position, currentSortKeys map[string]string) (*frame, error) {
	f := &frame{
		tasks:           map[string]*ftask{},
		blocks:          map[string]map[string]bool{},
		currentSortKeys: currentSortKeys,
	}

	// Order and bound by the log position, the same cursor the dashboard
	// uses: a rebuild renumbers e.id, so replaying "up to id N" walks a
	// different prefix after every pull.
	const cols = `SELECT e.id, e.ts, e.rep, e.seq, COALESCE(t.short_id, ''), e.event_type, e.actor, COALESCE(e.detail, '')
			FROM events e
			LEFT JOIN tasks t ON t.id = e.task_id`
	const order = ` ORDER BY e.ts, e.rep, CASE WHEN e.rep = '' THEN e.id ELSE e.seq END`

	var rows *sql.Rows
	var err error
	if atEvent == (eventlog.Position{}) {
		rows, err = db.QueryContext(ctx, cols+order)
	} else {
		rows, err = db.QueryContext(ctx,
			cols+" WHERE "+job.EventPositionExpr("e")+" <= (?, ?, ?)"+order,
			job.EventPositionArgs(atEvent)...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, ts int64
		var rep string
		var seq uint64
		var taskShort, eventType, actor, detailStr string
		if err := rows.Scan(&id, &ts, &rep, &seq, &taskShort, &eventType, &actor, &detailStr); err != nil {
			return nil, err
		}
		var detail map[string]any
		if detailStr != "" {
			_ = json.Unmarshal([]byte(detailStr), &detail)
		}
		applyEventReplay(f, taskShort, eventType, actor, detail, f.currentSortKeys)
		f.position = job.EventEntry{ID: id, TS: ts, Rep: rep, Seq: seq}.Position().String()
	}
	return f, rows.Err()
}

func applyEventReplay(f *frame, taskShort, eventType, actor string, detail map[string]any, currentSortKeys map[string]string) {
	getString := func(key string) string {
		v, _ := detail[key].(string)
		return v
	}
	switch eventType {
	case "created":
		// Prefer the live tasks-table sort key if known: historical
		// "created" payloads carry no key at all, so trusting the event
		// payload would scramble preorder traversal.
		so, ok := currentSortKeys[taskShort]
		if !ok {
			so = getString("sort_key")
		}
		f.tasks[taskShort] = &ftask{
			shortID:       taskShort,
			title:         getString("title"),
			parentShortID: getString("parent_id"),
			sortKey:       so,
			status:        "available",
		}
	case "claimed":
		if t, ok := f.tasks[taskShort]; ok {
			t.status = "claimed"
			t.claimedBy = actor
		}
	case "released", "claim_expired":
		if t, ok := f.tasks[taskShort]; ok {
			t.status = "available"
			t.claimedBy = ""
		}
	case "done":
		if t, ok := f.tasks[taskShort]; ok {
			t.status = "done"
			t.claimedBy = ""
		}
	case "canceled":
		if t, ok := f.tasks[taskShort]; ok {
			t.status = "canceled"
			t.claimedBy = ""
		}
	case "reopened":
		if t, ok := f.tasks[taskShort]; ok {
			t.status = "available"
		}
	case "blocked":
		blocked := getString("blocked_id")
		blocker := getString("blocker_id")
		if blocked == "" || blocker == "" {
			return
		}
		s := f.blocks[blocked]
		if s == nil {
			s = map[string]bool{}
			f.blocks[blocked] = s
		}
		s[blocker] = true
	case "unblocked":
		blocked := getString("blocked_id")
		blocker := getString("blocker_id")
		if blocked == "" || blocker == "" {
			return
		}
		if s, ok := f.blocks[blocked]; ok {
			delete(s, blocker)
			if len(s) == 0 {
				delete(f.blocks, blocked)
			}
		}
	case "moved":
		if t, ok := f.tasks[taskShort]; ok {
			if so, ok := detail["sort_key"].(string); ok {
				t.sortKey = so
			}
		}
	case "reparented":
		if t, ok := f.tasks[taskShort]; ok {
			t.parentShortID = getString("new_parent_id")
			if so, ok := detail["sort_key"].(string); ok {
				t.sortKey = so
			}
		}
	case "edited":
		if t, ok := f.tasks[taskShort]; ok {
			if title := getString("new_title"); title != "" {
				t.title = title
			}
		}
	}
}

func frameToInput(f *frame) signals.SubwayInput {
	var in signals.SubwayInput
	shorts := make([]string, 0, len(f.tasks))
	for s := range f.tasks {
		shorts = append(shorts, s)
	}
	sort.Strings(shorts)
	for _, s := range shorts {
		t := f.tasks[s]
		in.Tasks = append(in.Tasks, signals.SubwayInputTask{
			ShortID:       t.shortID,
			Title:         t.title,
			Status:        t.status,
			ParentShortID: t.parentShortID,
			SortKey:       t.sortKey,
			ClaimedBy:     t.claimedBy,
		})
	}
	blocked := make([]string, 0, len(f.blocks))
	for k := range f.blocks {
		blocked = append(blocked, k)
	}
	sort.Strings(blocked)
	for _, b := range blocked {
		blockers := make([]string, 0, len(f.blocks[b]))
		for blk := range f.blocks[b] {
			blockers = append(blockers, blk)
		}
		sort.Strings(blockers)
		for _, blk := range blockers {
			in.Blocks = append(in.Blocks, signals.SubwayInputBlock{
				BlockerShortID: blk,
				BlockedShortID: b,
			})
		}
	}
	return in
}

func summarizeNodeRef(in signals.SubwayInput, shortID string) string {
	for _, t := range in.Tasks {
		if t.ShortID == shortID {
			marks := ""
			switch t.Status {
			case "claimed":
				marks = " *claimed*"
			case "done":
				marks = " ✓"
			case "canceled":
				marks = " ⊘"
			}
			title := t.Title
			if len(title) > 50 {
				title = title[:47] + "…"
			}
			return fmt.Sprintf("%s %q%s", shortID, title, marks)
		}
	}
	return shortID
}

func edgeKind(k signals.SubwayEdgeKind) string {
	switch k {
	case signals.SubwayEdgeFlow:
		return "Flow"
	case signals.SubwayEdgeBranch:
		return "Branch"
	case signals.SubwayEdgeBranchClosed:
		return "Branch✗"
	case signals.SubwayEdgeBlocker:
		return "Blocker"
	}
	return fmt.Sprintf("Kind%d", k)
}

// computeGlobalNext mirrors signals.globalNext for diagnostic output:
// the first preorder available leaf with no open blockers. Reads the
// SubwayInput rather than reaching into unexported package internals.
func computeGlobalNext(in signals.SubwayInput) string {
	type tnode struct {
		short    string
		status   string
		children []*tnode
		open     bool
	}
	byShort := map[string]*tnode{}
	for _, t := range in.Tasks {
		byShort[t.ShortID] = &tnode{short: t.ShortID, status: t.Status}
	}
	openBlockers := map[string]int{}
	for _, b := range in.Blocks {
		blocker, okB := byShort[b.BlockerShortID]
		if !okB {
			continue
		}
		if blocker.status == "done" || blocker.status == "canceled" {
			continue
		}
		openBlockers[b.BlockedShortID]++
	}
	var roots []*tnode
	indexByShort := map[string]int{}
	for i, t := range in.Tasks {
		indexByShort[t.ShortID] = i
	}
	for _, t := range in.Tasks {
		n := byShort[t.ShortID]
		if t.ParentShortID == "" {
			roots = append(roots, n)
			continue
		}
		p, ok := byShort[t.ParentShortID]
		if !ok {
			roots = append(roots, n)
			continue
		}
		p.children = append(p.children, n)
	}
	sortChildren := func(nodes []*tnode) {
		sort.SliceStable(nodes, func(i, j int) bool {
			ti := in.Tasks[indexByShort[nodes[i].short]]
			tj := in.Tasks[indexByShort[nodes[j].short]]
			return ti.SortKey < tj.SortKey
		})
	}
	sortChildren(roots)
	for _, n := range byShort {
		sortChildren(n.children)
	}

	var found string
	var visit func(n *tnode) bool
	visit = func(n *tnode) bool {
		if n.status == "available" && len(n.children) == 0 && openBlockers[n.short] == 0 {
			found = n.short
			return true
		}
		return slices.ContainsFunc(n.children, visit)
	}
	if slices.ContainsFunc(roots, visit) {
		return found
	}
	return ""
}

func claimedShortIDs(in signals.SubwayInput) []string {
	var out []string
	for _, t := range in.Tasks {
		if t.Status == "claimed" {
			out = append(out, t.ShortID)
		}
	}
	sort.Strings(out)
	return out
}
