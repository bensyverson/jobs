package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// notePreviewMax is the maximum rune count of the body preview shown on
// `note`, `done -m`, and `cancel -m` acks. Long enough to confirm the
// right body landed; short enough to keep the ack on one line.
const notePreviewMax = 60

// childInlineCap caps the inline child list rendered by `show <id>`.
// At or below the cap, every child renders as one line. Above it,
// `show` emits a count line and points at `job ls <id>` to keep the
// briefing scannable on long-running parents.
const childInlineCap = 10

// NotePreview builds the body preview shown on note/done/cancel acks.
// Returns the rune count of the stored body and a single-line preview
// clamped to notePreviewMax runes. Newlines and tabs collapse to spaces;
// on overflow the cut snaps to the last space in the back third of the
// window so words don't get chopped mid-token.
func NotePreview(body string) (count int, preview string) {
	count = utf8.RuneCountInString(body)

	collapsed := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			return r
		}
	}, body)

	runes := []rune(collapsed)
	if len(runes) <= notePreviewMax {
		return count, strings.TrimRight(string(runes), " ")
	}

	cut := notePreviewMax
	for i := cut - 1; i >= notePreviewMax*2/3; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	return count, strings.TrimRight(string(runes[:cut]), " ") + "…"
}

// CollectBlockers walks a forest of TaskNodes and returns a map of
// short-ID → blocker short-IDs. Used by list/tree renderers to annotate
// blocked tasks.
func CollectBlockers(db *sql.DB, nodes []*TaskNode) (map[string][]string, error) {
	var ids []int64
	shortByID := make(map[int64]string)
	var walk func([]*TaskNode)
	walk = func(nodes []*TaskNode) {
		for _, node := range nodes {
			ids = append(ids, node.Task.ID)
			shortByID[node.Task.ID] = node.Task.ShortID
			walk(node.Children)
		}
	}
	walk(nodes)

	byID, err := GetBlockersForTaskIDs(db, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(byID))
	for id, blockers := range byID {
		if short, ok := shortByID[id]; ok && len(blockers) > 0 {
			result[short] = blockers
		}
	}
	return result, nil
}

type taskNodeJSON struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Description    string         `json:"description"`
	ClaimedBy      *string        `json:"claimed_by,omitempty"`
	ClaimExpiresAt *int64         `json:"claim_expires_at,omitempty"`
	Children       []taskNodeJSON `json:"children"`
}

func FormatTaskNodesJSON(nodes []*TaskNode) ([]byte, error) {
	var result []taskNodeJSON
	for _, node := range nodes {
		result = append(result, taskNodeToJSON(node))
	}
	return json.MarshalIndent(result, "", "  ")
}

func taskNodeToJSON(node *TaskNode) taskNodeJSON {
	var children []taskNodeJSON
	for _, child := range node.Children {
		children = append(children, taskNodeToJSON(child))
	}
	return taskNodeJSON{
		ID:             node.Task.ShortID,
		Title:          node.Task.Title,
		Status:         node.Task.Status,
		Description:    node.Task.Description,
		ClaimedBy:      node.Task.ClaimedBy,
		ClaimExpiresAt: node.Task.ClaimExpiresAt,
		Children:       children,
	}
}

func RenderMarkdownList(w io.Writer, nodes []*TaskNode, blockers map[string][]string, labels map[int64][]string, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, node := range nodes {
		checkbox := "[ ]"
		if node.Task.Status == "done" {
			checkbox = "[x]"
		}
		fmt.Fprintf(w, "%s- %s `%s` %s", indent, checkbox, node.Task.ShortID, node.Task.Title)
		parens := listStateParens(node, blockers, labels)
		if parens != "" {
			fmt.Fprintf(w, " %s", parens)
		}
		fmt.Fprintln(w)
		RenderMarkdownList(w, node.Children, blockers, labels, depth+1)
	}
}

func listStateParens(node *TaskNode, blockers map[string][]string, labels map[int64][]string) string {
	var parts []string
	switch node.Task.Status {
	case "done":
		// Completion note bodies used to be inlined here; they now live
		// in `job info` (R5). Keeping the list view structural makes
		// labels — the most common scan target — visible without a wall
		// of note text in front of them.
	case "canceled":
		parts = append(parts, "canceled")
	case "claimed":
		s := "claimed"
		if node.Task.ClaimedBy != nil {
			s = "claimed by " + *node.Task.ClaimedBy
		}
		if node.Task.ClaimExpiresAt != nil {
			remaining := *node.Task.ClaimExpiresAt - nowUnix()
			if remaining > 0 {
				s += ", " + FormatDuration(remaining) + " left"
			}
		}
		parts = append(parts, s)
	}
	if node.Task.Status != "done" {
		if blks, ok := blockers[node.Task.ShortID]; ok && len(blks) > 0 {
			parts = append(parts, "blocked on "+strings.Join(blks, ", "))
		}
	}
	if labels != nil {
		if lbls, ok := labels[node.Task.ID]; ok && len(lbls) > 0 {
			parts = append(parts, "labels: "+strings.Join(lbls, ", "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func RenderListEmpty(w io.Writer, totalTasks, doneTasks int) {
	if totalTasks == 0 {
		fmt.Fprintln(w, `No tasks. Run 'job import plan.md' or 'job --as <name> add "<title>"' to get started.`)
		return
	}
	fmt.Fprintf(w, "Nothing actionable. %d tasks done. Run 'list all' to see the full tree.\n", doneTasks)
}

func FormatDuration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}

func nowUnix() int64 {
	return CurrentNowFunc().Unix()
}

// RenderAncestorBrief writes a compact ancestor preamble block: ID, Title,
// and (if present) Description. Used by `show --ancestors` to give the
// reader the parent chain's context without the full per-task briefing.
func RenderAncestorBrief(w io.Writer, t *Task) {
	fmt.Fprintf(w, "ID:           %s\n", t.ShortID)
	fmt.Fprintf(w, "Title:        %s\n", t.Title)
	if t.Description != "" {
		fmt.Fprintf(w, "Description:  %s\n", unwrapProse(t.Description))
	}
}

func RenderInfoMarkdown(w io.Writer, info *TaskInfo) {
	fmt.Fprintf(w, "ID:           %s\n", info.Task.ShortID)
	fmt.Fprintf(w, "Title:        %s\n", info.Task.Title)
	fmt.Fprintf(w, "Status:       %s\n", info.Task.Status)
	if info.Task.Status == "claimed" {
		fmt.Fprintf(w, "Claim:        %s\n", formatClaimExpires(info.Task.ClaimedBy, info.Task.ClaimExpiresAt))
	}
	if info.Parent != nil {
		fmt.Fprintf(w, "Parent:       %s (%s)\n", info.Parent.ShortID, info.Parent.Title)
	} else {
		fmt.Fprintf(w, "Parent:       (root)\n")
	}
	if len(info.Labels) > 0 {
		fmt.Fprintf(w, "Labels:       %s\n", strings.Join(info.Labels, ", "))
	}
	if len(info.Children) > 0 {
		// Above the cap: the inline list would crowd the briefing, so
		// collapse to a single count line that names the canonical drill-
		// down command. Mirrors the cap rule documented in `show --help`.
		if len(info.Children) > childInlineCap {
			fmt.Fprintf(w, "Children:     %d total — list truncated. Run `job ls %s` to see all.\n", len(info.Children), info.Task.ShortID)
		} else {
			fmt.Fprintln(w, "Children:")
			for _, c := range info.Children {
				node := &TaskNode{Task: c}
				checkbox := "[ ]"
				if c.Status == "done" {
					checkbox = "[x]"
				}
				fmt.Fprintf(w, "  - %s `%s` %s", checkbox, c.ShortID, c.Title)
				if parens := listStateParens(node, info.ChildBlockers, info.ChildLabels); parens != "" {
					fmt.Fprintf(w, " %s", parens)
				}
				fmt.Fprintln(w)
			}
		}
	}
	if len(info.Blockers) > 0 {
		var ids []string
		for _, b := range info.Blockers {
			ids = append(ids, b.ShortID)
		}
		fmt.Fprintf(w, "Blocked by:   %s\n", strings.Join(ids, ", "))
	}
	if len(info.Blocked) > 0 {
		var ids []string
		for _, b := range info.Blocked {
			ids = append(ids, b.ShortID)
		}
		fmt.Fprintf(w, "Blocks:       %s\n", strings.Join(ids, ", "))
	}
	// found-in is provenance, printed apart from the two blocking lines so
	// the reader is never invited to read it as sequence. The source's
	// status is inline because the useful case is a closed source.
	if info.FoundIn != nil {
		fmt.Fprintf(w, "Found in:     %s %s (%s)\n", info.FoundIn.ShortID, info.FoundIn.Title, info.FoundIn.Status)
	}
	if len(info.Surfaced) > 0 {
		fmt.Fprintln(w, "Surfaced:")
		for _, sfc := range info.Surfaced {
			fmt.Fprintf(w, "  - `%s` %s\n", sfc.ShortID, sfc.Title)
		}
	}
	fmt.Fprintf(w, "Created:      %s\n", formatTimestamp(info.Task.CreatedAt))

	if info.Task.Description != "" {
		fmt.Fprintf(w, "\nDescription:\n  %s\n", unwrapProse(info.Task.Description))
	}

	if len(info.Criteria) > 0 {
		pending := 0
		for _, c := range info.Criteria {
			if c.State == CriterionPending {
				pending++
			}
		}
		// When pending > 0 the header line names the same constraint the
		// operator will hit at close time (the strict-close gate), so the
		// claim briefing primes them for the close shape rather than letting
		// the count surprise them on `job done`.
		if pending > 0 {
			fmt.Fprintf(w, "Criteria: %d pending — mark each before close, or use --force-close-with-pending\n", pending)
		} else {
			fmt.Fprintln(w, "Criteria:")
		}
		for _, c := range info.Criteria {
			if c.ShortID != "" {
				fmt.Fprintf(w, "  %s %s %s\n", c.ShortID, criterionGlyph(c.State), c.Label)
			} else {
				fmt.Fprintf(w, "  %s %s\n", criterionGlyph(c.State), c.Label)
			}
		}
	}

	if len(info.Notes) > 0 {
		fmt.Fprintln(w, "Notes:")
		now := nowUnix()
		for _, n := range info.Notes {
			ago := now - n.CreatedAt
			ts := formatTimestamp(n.CreatedAt)
			if ago > 0 {
				fmt.Fprintf(w, "  [%s] @%s (%s ago)\n", ts, n.Actor, FormatDuration(ago))
			} else {
				fmt.Fprintf(w, "  [%s] @%s\n", ts, n.Actor)
			}
			fmt.Fprintf(w, "    %s\n", unwrapProse(n.Text))
		}
	}
}

// criterionGlyph returns the checkbox-style glyph for a criterion state.
func criterionGlyph(state CriterionState) string {
	switch state {
	case CriterionPassed:
		return "[x]"
	case CriterionFailed:
		return "[!]"
	case CriterionSkipped:
		return "[-]"
	default:
		return "[ ]"
	}
}

// unwrapProse turns author-supplied hard-wrapped prose into terminal-
// friendly output:
//   - Single \n inside a paragraph collapses to a space.
//   - Blank lines (\n\n+) are preserved as paragraph breaks.
//   - Bullet lines (- or *) and numbered list lines (1.) keep their own
//     line so list structure survives.
//
// Trailing whitespace and trailing blank lines are trimmed.
func unwrapProse(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	var out []string
	var para []string

	flush := func() {
		if len(para) > 0 {
			out = append(out, strings.Join(para, " "))
			para = para[:0]
		}
	}

	isBullet := func(line string) bool {
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || t == "-" || t == "*" {
			return true
		}
		// Loose numbered-list detection: "1. " through "999. ".
		for i := 0; i < len(t) && i < 4; i++ {
			c := t[i]
			if c >= '0' && c <= '9' {
				continue
			}
			if c == '.' && i > 0 && i+1 < len(t) && t[i+1] == ' ' {
				return true
			}
			break
		}
		return false
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			flush()
			out = append(out, "")
			continue
		}
		if isBullet(trimmed) {
			flush()
			out = append(out, trimmed)
			continue
		}
		para = append(para, trimmed)
	}
	flush()

	// Trim trailing blank lines so descriptions that ended in newlines
	// don't blow out the bottom of the section.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func RenderInfoJSON(w io.Writer, info *TaskInfo) {
	type noteJSON struct {
		Text      string `json:"text"`
		Actor     string `json:"actor"`
		CreatedAt int64  `json:"created_at"`
	}
	type childJSON struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Status   string   `json:"status"`
		Blockers []string `json:"blockers,omitempty"`
		Labels   []string `json:"labels,omitempty"`
	}
	type infoJSON struct {
		ID          string      `json:"id"`
		Title       string      `json:"title"`
		Description string      `json:"description"`
		Status      string      `json:"status"`
		Parent      *string     `json:"parent,omitempty"`
		Children    []childJSON `json:"children"`
		Blockers    []string    `json:"blockers,omitempty"`
		FoundIn     string      `json:"found_in,omitempty"`
		Surfaced    []string    `json:"surfaced,omitempty"`
		Labels      []string    `json:"labels"`
		Notes       []noteJSON  `json:"notes"`
		CreatedAt   int64       `json:"created_at"`
	}

	var parentID *string
	if info.Parent != nil {
		parentID = &info.Parent.ShortID
	}
	var blockers []string
	for _, b := range info.Blockers {
		blockers = append(blockers, b.ShortID)
	}

	var foundIn string
	if info.FoundIn != nil {
		foundIn = info.FoundIn.ShortID
	}
	var surfaced []string
	for _, s := range info.Surfaced {
		surfaced = append(surfaced, s.ShortID)
	}

	labels := info.Labels
	if labels == nil {
		labels = []string{}
	}

	notes := make([]noteJSON, 0, len(info.Notes))
	for _, n := range info.Notes {
		notes = append(notes, noteJSON{Text: n.Text, Actor: n.Actor, CreatedAt: n.CreatedAt})
	}

	children := make([]childJSON, 0, len(info.Children))
	for _, c := range info.Children {
		entry := childJSON{ID: c.ShortID, Title: c.Title, Status: c.Status}
		if blks := info.ChildBlockers[c.ShortID]; len(blks) > 0 {
			entry.Blockers = blks
		}
		if lbls := info.ChildLabels[c.ID]; len(lbls) > 0 {
			entry.Labels = lbls
		}
		children = append(children, entry)
	}

	obj := infoJSON{
		ID:          info.Task.ShortID,
		Title:       info.Task.Title,
		Description: info.Task.Description,
		Status:      info.Task.Status,
		Parent:      parentID,
		Children:    children,
		Blockers:    blockers,
		FoundIn:     foundIn,
		Surfaced:    surfaced,
		Labels:      labels,
		Notes:       notes,
		CreatedAt:   info.Task.CreatedAt,
	}
	b, _ := json.MarshalIndent(obj, "", "  ")
	w.Write(b)
}

func formatTimestamp(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}

func RenderTaskJSON(w io.Writer, task *Task) {
	obj := taskNodeJSON{
		ID:             task.ShortID,
		Title:          task.Title,
		Status:         task.Status,
		Description:    task.Description,
		ClaimedBy:      task.ClaimedBy,
		ClaimExpiresAt: task.ClaimExpiresAt,
	}
	b, _ := json.MarshalIndent(obj, "", "  ")
	w.Write(b)
}

func RenderNextAllText(w io.Writer, tasks []*Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No available tasks.")
		return
	}
	for _, t := range tasks {
		fmt.Fprintf(w, "- %s %q\n", t.ShortID, t.Title)
	}
}

func RenderNextAllJSON(w io.Writer, tasks []*Task) error {
	out := make([]taskNodeJSON, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskNodeJSON{
			ID:             t.ShortID,
			Title:          t.Title,
			Status:         t.Status,
			Description:    t.Description,
			ClaimedBy:      t.ClaimedBy,
			ClaimExpiresAt: t.ClaimExpiresAt,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	w.Write(b)
	fmt.Fprintln(w)
	return nil
}

func RenderNextText(w io.Writer, task *Task) {
	fmt.Fprintf(w, "%s  %s\n", task.ShortID, task.Title)
	if task.Description != "" {
		fmt.Fprintf(w, "\n  %s\n", task.Description)
	}
}

func RenderEventLogMarkdown(w io.Writer, events []EventEntry) {
	for _, e := range events {
		ts := formatTimestamp(e.CreatedAt)
		desc := FormatEventDescription(e.EventType, e.Detail)
		fmt.Fprintf(w, "[%s] %s %s  @%s\n", ts, e.ShortID, desc, e.Actor)
	}
}

func FormatEventDescription(eventType, detailJSON string) string {
	var detail map[string]any
	if detailJSON != "" {
		json.Unmarshal([]byte(detailJSON), &detail)
	}

	switch eventType {
	case "created":
		title := ""
		if detail != nil {
			if t, ok := detail["title"].(string); ok {
				title = t
			}
		}
		return fmt.Sprintf("created: %q", title)
	case "claimed":
		dur := FormatDuration(DefaultClaimTTLSeconds)
		if detail != nil {
			if d, ok := detail["duration"].(string); ok && d != "" {
				dur = d
			}
		}
		return fmt.Sprintf("claimed (%s)", dur)
	case "heartbeat":
		return "heartbeat"
	case "released":
		return "released"
	case "claim_expired":
		return "claim expired"
	case "done":
		parts := []string{"done"}
		cascaded := false
		if detail != nil {
			if c, ok := detail["cascade"].(bool); ok && c {
				cascaded = true
			}
			if f, ok := detail["force"].(bool); ok && f {
				cascaded = true
			}
			if note, ok := detail["note"].(string); ok && note != "" {
				parts = append(parts, "note: "+note)
			}
			if children, ok := detail["cascade_closed"].([]any); ok && len(children) > 0 {
				parts = append(parts, fmt.Sprintf("and %d subtasks", len(children)))
			} else if children, ok := detail["force_closed_children"].([]any); ok && len(children) > 0 {
				parts = append(parts, fmt.Sprintf("and %d subtasks", len(children)))
			}
		}
		if cascaded {
			parts[0] = "done --cascade"
		}
		return strings.Join(parts, " (") + strings.Repeat(")", len(parts)-1)
	case "reopened":
		if detail != nil {
			if children, ok := detail["reopened_children"].([]any); ok && len(children) > 0 {
				return fmt.Sprintf("reopened (and %d subtasks)", len(children))
			}
		}
		return "reopened"
	case "noted":
		text := ""
		if detail != nil {
			if t, ok := detail["text"].(string); ok {
				text = t
			}
		}
		return fmt.Sprintf("noted: %q", text)
	case "edited":
		if detail != nil {
			_, hasOldTitle := detail["old_title"]
			_, hasOldDesc := detail["old_desc"]
			if hasOldTitle {
				oldTitle, _ := detail["old_title"].(string)
				newTitle, _ := detail["new_title"].(string)
				if hasOldDesc {
					return fmt.Sprintf("edited: title %q -> %q, description updated", oldTitle, newTitle)
				}
				return fmt.Sprintf("edited: %q -> %q", oldTitle, newTitle)
			}
			if hasOldDesc {
				return "edited: description updated"
			}
		}
		return "edited"
	case "blocked":
		blockerID := ""
		if detail != nil {
			if b, ok := detail["blocker_id"].(string); ok {
				blockerID = b
			}
		}
		return fmt.Sprintf("blocked by %s", blockerID)
	case "unblocked":
		blockerID := ""
		reason := ""
		if detail != nil {
			if b, ok := detail["blocker_id"].(string); ok {
				blockerID = b
			}
			if r, ok := detail["reason"].(string); ok {
				reason = " (" + r + ")"
			}
		}
		return fmt.Sprintf("unblocked from %s%s", blockerID, reason)
	case "found_in_set":
		source := ""
		previous := ""
		if detail != nil {
			if v, ok := detail["source_id"].(string); ok {
				source = v
			}
			if v, ok := detail["previous_source_id"].(string); ok {
				previous = v
			}
		}
		if previous != "" {
			return fmt.Sprintf("found in %s (was %s)", source, previous)
		}
		return fmt.Sprintf("found in %s", source)
	case "found_in_cleared":
		source := ""
		if detail != nil {
			if v, ok := detail["source_id"].(string); ok {
				source = v
			}
		}
		return fmt.Sprintf("found-in cleared (was %s)", source)
	case "labeled":
		names := stringListFromDetail(detail, "names")
		existing := stringListFromDetail(detail, "existing")
		added := subtractStringList(names, existing)
		if len(added) == 0 {
			return "labeled: " + strings.Join(names, ", ")
		}
		if len(existing) == 0 {
			return "labeled: " + strings.Join(added, ", ")
		}
		return fmt.Sprintf("labeled: %s (already had: %s)", strings.Join(added, ", "), strings.Join(existing, ", "))
	case "unlabeled":
		names := stringListFromDetail(detail, "names")
		absent := stringListFromDetail(detail, "absent")
		removed := subtractStringList(names, absent)
		if len(removed) == 0 {
			return "unlabeled: " + strings.Join(names, ", ")
		}
		if len(absent) == 0 {
			return "unlabeled: " + strings.Join(removed, ", ")
		}
		return fmt.Sprintf("unlabeled: %s (was absent: %s)", strings.Join(removed, ", "), strings.Join(absent, ", "))
	case "moved":
		direction := ""
		relativeTo := ""
		if detail != nil {
			if d, ok := detail["direction"].(string); ok {
				direction = d
			}
			if r, ok := detail["relative_to"].(string); ok {
				relativeTo = r
			}
		}
		return fmt.Sprintf("moved %s %s", direction, relativeTo)
	case "criteria_added":
		count := 0
		if detail != nil {
			if list, ok := detail["criteria"].([]any); ok {
				count = len(list)
			}
		}
		return fmt.Sprintf("criteria added (%d)", count)
	case "criterion_state":
		label := ""
		state := ""
		if detail != nil {
			if v, ok := detail["label"].(string); ok {
				label = v
			}
			if v, ok := detail["state"].(string); ok {
				state = v
			}
		}
		return fmt.Sprintf("criterion %q → %s", label, state)
	case "reparented":
		newParent := ""
		priorParent := ""
		direction := ""
		relativeTo := ""
		if detail != nil {
			if v, ok := detail["new_parent_id"].(string); ok {
				newParent = v
			}
			if v, ok := detail["prior_parent_id"].(string); ok {
				priorParent = v
			}
			if v, ok := detail["direction"].(string); ok {
				direction = v
			}
			if v, ok := detail["relative_to"].(string); ok {
				relativeTo = v
			}
		}
		dest := newParent
		if dest == "" {
			dest = "(root)"
		}
		from := priorParent
		if from == "" {
			from = "(root)"
		}
		base := fmt.Sprintf("reparented from %s to %s", from, dest)
		if direction != "" {
			base = fmt.Sprintf("%s (%s %s)", base, direction, relativeTo)
		}
		return base
	case "removed":
		parts := []string{"removed"}
		if detail != nil {
			if children, ok := detail["children_removed"].([]any); ok && len(children) > 0 {
				parts = append(parts, fmt.Sprintf("and %d subtasks", len(children)))
			}
		}
		return strings.Join(parts, " (") + strings.Repeat(")", len(parts)-1)
	case "canceled":
		parts := []string{"canceled"}
		cascaded := false
		if detail != nil {
			if c, ok := detail["cascade"].(bool); ok && c {
				cascaded = true
			}
			if reason, ok := detail["reason"].(string); ok && reason != "" {
				parts = append(parts, "reason: "+reason)
			}
			if children, ok := detail["cascade_closed"].([]any); ok && len(children) > 0 {
				parts = append(parts, fmt.Sprintf("and %d subtasks", len(children)))
			}
		}
		if cascaded && len(parts) > 0 {
			parts[0] = "canceled --cascade"
		}
		return strings.Join(parts, " (") + strings.Repeat(")", len(parts)-1)
	case "purged":
		parts := []string{"purged"}
		if detail != nil {
			if id, ok := detail["purged_id"].(string); ok && id != "" {
				parts = append(parts, "id: "+id)
			}
			if reason, ok := detail["reason"].(string); ok && reason != "" {
				parts = append(parts, "reason: "+reason)
			}
			if children, ok := detail["cascade_purged"].([]any); ok && len(children) > 0 {
				parts = append(parts, fmt.Sprintf("and %d subtasks", len(children)))
			}
		}
		return strings.Join(parts, " (") + strings.Repeat(")", len(parts)-1)
	default:
		return eventType
	}
}

func stringListFromDetail(detail map[string]any, key string) []string {
	if detail == nil {
		return nil
	}
	raw, ok := detail[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// subtractStringList returns a - b preserving a's order.
func subtractStringList(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	skip := make(map[string]bool, len(b))
	for _, s := range b {
		skip[s] = true
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if !skip[s] {
			out = append(out, s)
		}
	}
	return out
}

func RenderLabelAck(w io.Writer, res *LabelResult) {
	if len(res.Added) > 0 && len(res.Existing) == 0 {
		fmt.Fprintf(w, "Labeled: %s (%s)\n", res.ShortID, plusJoined(res.Added))
		return
	}
	if len(res.Added) > 0 && len(res.Existing) > 0 {
		fmt.Fprintf(w, "Labeled: %s (%s; already had: %s)\n",
			res.ShortID, plusJoined(res.Added), strings.Join(res.Existing, ", "))
		return
	}
	fmt.Fprintf(w, "Already labeled: %s (%s)\n", res.ShortID, strings.Join(res.Existing, ", "))
}

func RenderUnlabelAck(w io.Writer, res *UnlabelResult) {
	if len(res.Removed) > 0 && len(res.Absent) == 0 {
		fmt.Fprintf(w, "Unlabeled: %s (%s)\n", res.ShortID, minusJoined(res.Removed))
		return
	}
	if len(res.Removed) > 0 && len(res.Absent) > 0 {
		fmt.Fprintf(w, "Unlabeled: %s (%s; was absent: %s)\n",
			res.ShortID, minusJoined(res.Removed), strings.Join(res.Absent, ", "))
		return
	}
	fmt.Fprintf(w, "Already unlabeled: %s (%s)\n", res.ShortID, strings.Join(res.Absent, ", "))
}

func plusJoined(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "+" + n
	}
	return strings.Join(parts, ", ")
}

func minusJoined(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "-" + n
	}
	return strings.Join(parts, ", ")
}

func RenderLabelJSON(w io.Writer, res *LabelResult) error {
	obj := map[string]any{
		"id":       res.ShortID,
		"added":    ensureStringSlice(res.Added),
		"existing": ensureStringSlice(res.Existing),
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	w.Write(b)
	fmt.Fprintln(w)
	return nil
}

func RenderUnlabelJSON(w io.Writer, res *UnlabelResult) error {
	obj := map[string]any{
		"id":      res.ShortID,
		"removed": ensureStringSlice(res.Removed),
		"absent":  ensureStringSlice(res.Absent),
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	w.Write(b)
	fmt.Fprintln(w)
	return nil
}

type eventJSON struct {
	ID        int64  `json:"id"`
	TaskID    int64  `json:"task_id"`
	ShortID   string `json:"short_id"`
	EventType string `json:"event_type"`
	Actor     string `json:"actor"`
	Detail    any    `json:"detail"`
	CreatedAt int64  `json:"created_at"`
}

func RenderCancelAck(w io.Writer, canceled []*CanceledResult, alreadyCanceled []string, reason string) {
	RenderCancelAckAs(w, canceled, alreadyCanceled, reason, "")
}

// RenderCancelAckAs renders the cancel ack and, when actor is non-empty,
// appends "as=<actor>" to the leading single-result line so misattributed
// writes surface at the moment of action.
func RenderCancelAckAs(w io.Writer, canceled []*CanceledResult, alreadyCanceled []string, reason, actor string) {
	asTag := ""
	if actor != "" {
		asTag = " as=" + actor
	}
	single := len(canceled) == 1 && len(alreadyCanceled) == 0 && len(canceled[0].CascadeCanceled) == 0

	if single {
		c := canceled[0]
		fmt.Fprintf(w, "Canceled: %s %q%s\n", c.ShortID, c.Title, asTag)
	} else if len(canceled) == 1 && len(canceled[0].CascadeCanceled) > 0 && len(alreadyCanceled) == 0 {
		c := canceled[0]
		fmt.Fprintf(w, "Canceled: %s %q (and %d subtasks)%s\n", c.ShortID, c.Title, len(c.CascadeCanceled), asTag)
	} else if len(canceled) > 0 {
		fmt.Fprintf(w, "Canceled %d tasks:\n", len(canceled))
		for _, c := range canceled {
			if len(c.CascadeCanceled) > 0 {
				fmt.Fprintf(w, "- Canceled: %s %q (and %d subtasks)\n", c.ShortID, c.Title, len(c.CascadeCanceled))
			} else {
				fmt.Fprintf(w, "- Canceled: %s %q\n", c.ShortID, c.Title)
			}
		}
	}
	if len(canceled) > 0 && reason != "" {
		count, preview := NotePreview(reason)
		fmt.Fprintf(w, "  reason: %d chars · %q\n", count, preview)
	}
	// Surface auto-closed ancestors from the cancel cascade, one line each.
	// Status is stamped because cancel-triggered cascades can land on either
	// "done" (any sibling was done) or "canceled" (all siblings canceled).
	for _, c := range canceled {
		for _, a := range c.AutoClosedAncestors {
			fmt.Fprintf(w, "  Auto-closed (%s): %s %q\n", a.Status, a.ShortID, a.Title)
		}
	}
	if len(alreadyCanceled) > 0 {
		fmt.Fprintf(w, "  already canceled: %s\n", strings.Join(alreadyCanceled, ", "))
	}
}

func RenderPurgeAck(w io.Writer, purged []*PurgedResult, reason string) {
	for _, p := range purged {
		fmt.Fprintf(w, "Purged: %s %q\n", p.ShortID, p.Title)
		fmt.Fprintf(w, "  reason: %s\n", reason)
		if len(p.CascadePurged) > 0 {
			fmt.Fprintf(w, "  (and %d subtasks)\n", len(p.CascadePurged))
		}
		fmt.Fprintf(w, "  (%d events erased)\n", p.EventsErased)
	}
}

type cancelJSONAutoClosed struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type cancelJSONCanceled struct {
	ID                  string                 `json:"id"`
	Title               string                 `json:"title"`
	CascadeClosed       []string               `json:"cascade_closed"`
	WasStatus           string                 `json:"was_status"`
	AutoClosedAncestors []cancelJSONAutoClosed `json:"auto_closed_ancestors,omitempty"`
}

type cancelJSONPurged struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	CascadePurged []string `json:"cascade_purged"`
	ErasedEvents  int      `json:"erased_events"`
}

type cancelJSON struct {
	Canceled        []cancelJSONCanceled `json:"canceled,omitempty"`
	AlreadyCanceled []string             `json:"already_canceled"`
	Reason          string               `json:"reason"`
	Purged          bool                 `json:"purged"`
	PurgedItems     []cancelJSONPurged   `json:"purged_items,omitempty"`
	ErasedEvents    int                  `json:"erased_events,omitempty"`
}

func RenderCancelJSON(w io.Writer, canceled []*CanceledResult, alreadyCanceled []string, purged []*PurgedResult, reason string) error {
	out := cancelJSON{
		AlreadyCanceled: alreadyCanceled,
		Reason:          reason,
	}
	if out.AlreadyCanceled == nil {
		out.AlreadyCanceled = []string{}
	}
	if len(purged) > 0 {
		out.Purged = true
		for _, p := range purged {
			cp := p.CascadePurged
			if cp == nil {
				cp = []string{}
			}
			out.PurgedItems = append(out.PurgedItems, cancelJSONPurged{
				ID:            p.ShortID,
				Title:         p.Title,
				CascadePurged: cp,
				ErasedEvents:  p.EventsErased,
			})
			out.ErasedEvents += p.EventsErased
		}
	} else {
		for _, c := range canceled {
			cc := c.CascadeCanceled
			if cc == nil {
				cc = []string{}
			}
			var ancestors []cancelJSONAutoClosed
			for _, a := range c.AutoClosedAncestors {
				ancestors = append(ancestors, cancelJSONAutoClosed{
					ID:     a.ShortID,
					Title:  a.Title,
					Status: a.Status,
				})
			}
			out.Canceled = append(out.Canceled, cancelJSONCanceled{
				ID:                  c.ShortID,
				Title:               c.Title,
				CascadeClosed:       cc,
				WasStatus:           c.WasStatus,
				AutoClosedAncestors: ancestors,
			})
		}
		if out.Canceled == nil {
			out.Canceled = []cancelJSONCanceled{}
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	w.Write(b)
	fmt.Fprintln(w)
	return nil
}

func RenderHeartbeatAck(w io.Writer, results []*HeartbeatResult) {
	now := CurrentNowFunc().Unix()
	if len(results) == 1 {
		r := results[0]
		fmt.Fprintf(w, "Heartbeat: %s (expires in %s)\n", r.ShortID, FormatDuration(r.ExpiresAt-now))
		return
	}
	fmt.Fprintf(w, "Heartbeat %d tasks:\n", len(results))
	for _, r := range results {
		fmt.Fprintf(w, "- %s (expires in %s)\n", r.ShortID, FormatDuration(r.ExpiresAt-now))
	}
}

type heartbeatJSONEntry struct {
	ID               string `json:"id"`
	ExpiresAt        int64  `json:"expires_at"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type heartbeatJSON struct {
	Heartbeat []heartbeatJSONEntry `json:"heartbeat"`
}

func RenderHeartbeatJSON(w io.Writer, results []*HeartbeatResult) error {
	now := CurrentNowFunc().Unix()
	out := heartbeatJSON{Heartbeat: make([]heartbeatJSONEntry, 0, len(results))}
	for _, r := range results {
		out.Heartbeat = append(out.Heartbeat, heartbeatJSONEntry{
			ID:               r.ShortID,
			ExpiresAt:        r.ExpiresAt,
			ExpiresInSeconds: r.ExpiresAt - now,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	w.Write(b)
	fmt.Fprintln(w)
	return nil
}

func renderClosedLine(w io.Writer, shortID, eventType, format string, alreadyClosed bool) error {
	if format == "json" {
		obj := map[string]any{
			"closed": shortID,
			"event":  eventType,
		}
		if alreadyClosed {
			obj["already_closed"] = true
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		w.Write(b)
		fmt.Fprintln(w)
		return nil
	}
	if alreadyClosed {
		fmt.Fprintf(w, "Closed: %s (already %s)\n", shortID, eventType)
	} else {
		fmt.Fprintf(w, "Closed: %s (%s)\n", shortID, eventType)
	}
	return nil
}

func renderTimeoutSummary(w io.Writer, stillOpen []string, format string) error {
	if format == "json" {
		obj := map[string]any{
			"timeout":    true,
			"still_open": stillOpen,
		}
		if stillOpen == nil {
			obj["still_open"] = []string{}
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		w.Write(b)
		fmt.Fprintln(w)
		return nil
	}
	fmt.Fprintf(w, "Timeout: %d still open: %s\n", len(stillOpen), strings.Join(stillOpen, ", "))
	return nil
}

// FormatEventLogJSONLines emits one JSON object per line, with no wrapping
// array. Suitable for `tail --format=json` consumed by line-based subscribers
// such as `jq -c`.
func FormatEventLogJSONLines(w io.Writer, events []EventEntry) error {
	for _, e := range events {
		var detail any
		if e.Detail != "" {
			var parsed any
			if err := json.Unmarshal([]byte(e.Detail), &parsed); err == nil {
				detail = parsed
			}
		}
		obj := eventJSON{
			ID:        e.ID,
			TaskID:    e.TaskID,
			ShortID:   e.ShortID,
			EventType: e.EventType,
			Actor:     e.Actor,
			Detail:    detail,
			CreatedAt: e.CreatedAt,
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func FormatEventLogJSON(events []EventEntry) ([]byte, error) {
	var result []eventJSON
	for _, e := range events {
		var detail any
		if e.Detail != "" {
			var parsed any
			if err := json.Unmarshal([]byte(e.Detail), &parsed); err == nil {
				detail = parsed
			}
		}
		result = append(result, eventJSON{
			ID:        e.ID,
			TaskID:    e.TaskID,
			ShortID:   e.ShortID,
			EventType: e.EventType,
			Actor:     e.Actor,
			Detail:    detail,
			CreatedAt: e.CreatedAt,
		})
	}
	return json.MarshalIndent(result, "", "  ")
}
