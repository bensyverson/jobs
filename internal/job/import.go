package job

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type ImportedTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Parent    string   `json:"parent"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	// Kind is set only when the row asked for a non-default tree kind, so a
	// plain plan echoes exactly as it always has.
	Kind string `json:"kind,omitempty"`
	// FoundIn is the short ID (or dry-run placeholder) of the task that
	// surfaced this one.
	FoundIn string `json:"found_in,omitempty"`
}

type ImportResult struct {
	DryRun   bool           `json:"dry_run"`
	Tasks    []ImportedTask `json:"tasks"`
	Warnings []string       `json:"warnings,omitempty"`
}

// parsedTask is the intermediate tree after YAML decode + validation.
type parsedTask struct {
	Title     string
	Desc      string
	Labels    []string
	Ref       string
	BlockedBy []string
	FoundIn   string
	Criteria  []Criterion
	Children  []*parsedTask

	// Kind is KindTask unless the row asked otherwise. kindPresent records
	// whether the key appeared at all, because the key is refused on a child
	// whatever its value.
	Kind        TreeKind
	kindPresent bool

	// pathLabel is the YAML path like "tasks[1].children[0]"; filled during validation.
	pathLabel string
	// index in the flat pre-order DFS walk; filled during validation.
	flatIndex int
}

// Raw YAML shape: we decode into a loose structure first so we can enforce
// additionalProperties=false and emit precise path-based errors.
type rawTask struct {
	Title     string     `yaml:"title"`
	Desc      string     `yaml:"desc"`
	Labels    []string   `yaml:"labels"`
	Ref       string     `yaml:"ref"`
	BlockedBy []string   `yaml:"blockedBy"`
	FoundIn   string     `yaml:"foundIn"`
	Kind      string     `yaml:"kind"`
	Criteria  []string   `yaml:"criteria"`
	Children  []*rawTask `yaml:"children"`

	// Set when a title key was present in the YAML at all (even empty).
	titlePresent bool
	// Set when a kind key was present, whatever its value: kind is refused on
	// a child even when it names the default.
	kindPresent bool
}

// Custom unmarshal to track whether title was explicitly provided.
func (r *rawTask) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping, got %v", n.Kind)
	}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		switch k.Value {
		case "title":
			r.titlePresent = true
			if err := v.Decode(&r.Title); err != nil {
				return err
			}
		case "desc":
			if err := v.Decode(&r.Desc); err != nil {
				return err
			}
		case "labels":
			if err := v.Decode(&r.Labels); err != nil {
				return err
			}
		case "ref":
			if err := v.Decode(&r.Ref); err != nil {
				return err
			}
		case "blockedBy":
			if err := v.Decode(&r.BlockedBy); err != nil {
				return err
			}
		case "foundIn":
			if err := v.Decode(&r.FoundIn); err != nil {
				return err
			}
		case "kind":
			r.kindPresent = true
			if err := v.Decode(&r.Kind); err != nil {
				return err
			}
		case "criteria":
			if err := decodeCriteria(v, &r.Criteria); err != nil {
				return err
			}
		case "children":
			if err := v.Decode(&r.Children); err != nil {
				return err
			}
		default:
			// Unknown key: ignore silently. JSON Schema declares additionalProperties=false,
			// but Phase 2 is lenient here — makes forward-compat (labels, future keys) painless.
		}
	}
	return nil
}

type rawRoot struct {
	Tasks []*rawTask `yaml:"tasks"`
}

var fenceOpenRe = regexp.MustCompile(`^(` + "```" + `|~~~)([a-zA-Z0-9_+-]*)\s*$`)

// tasksBlock is a fenced code block that decodes to a top-level map with a
// `tasks` key — a candidate for import. lang is the fence's info string
// ("yaml", "yml", or "" for unlabeled); startLine is the 1-based line of the
// opening fence, used to name the block in selection warnings.
type tasksBlock struct {
	body      string
	lang      string
	startLine int
}

// extractTasksBlocks scans raw Markdown text for fenced code blocks and returns
// every yaml/yml/unlabeled block whose YAML decode yields a top-level map with
// a `tasks` key, in document order. Callers import the first and may warn when
// there is more than one. If no block matches and at least one candidate fence
// produced a YAML parse error, that error is returned so callers can surface it
// instead of a generic message.
func extractTasksBlocks(content string) ([]tasksBlock, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	var (
		blocks   []tasksBlock
		inFence  bool
		fence    string
		curBody  strings.Builder
		curLang  string
		curStart int
		lastErr  error
		lineNo   int
	)

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !inFence {
			if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
				inFence = true
				fence = m[1]
				curLang = m[2]
				curStart = lineNo
				curBody.Reset()
			}
			continue
		}
		// Closing fence must match opener (``` or ~~~).
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == fence {
			body := curBody.String()
			// Only yaml/yml/unlabeled fences are candidates.
			if curLang == "" || curLang == "yaml" || curLang == "yml" {
				var probe map[string]any
				if err := yaml.Unmarshal([]byte(body), &probe); err != nil {
					lastErr = err
				} else if _, ok := probe["tasks"]; ok {
					blocks = append(blocks, tasksBlock{body: body, lang: curLang, startLine: curStart})
				}
			}
			inFence = false
			fence = ""
			curLang = ""
			curBody.Reset()
			continue
		}
		curBody.WriteString(line)
		curBody.WriteByte('\n')
	}

	// Content fallback: a raw, unfenced document whose top level is `tasks:` is
	// valid YAML and should import directly — no Markdown fence required. Only
	// fires when no fenced candidate was found, so fenced plans are unaffected.
	// The synthetic block spans the whole document, so the warning and
	// unknown-key paths operate over it exactly as for a fenced block.
	if len(blocks) == 0 {
		var probe map[string]any
		if err := yaml.Unmarshal([]byte(content), &probe); err == nil {
			if _, ok := probe["tasks"]; ok {
				blocks = append(blocks, tasksBlock{body: content, lang: "", startLine: 1})
			}
		}
	}
	return blocks, lastErr
}

// importGrammarKeys is the set of per-task keys the importer understands. Kept
// in sync with rawTask.UnmarshalYAML's switch; used to detect (and warn about)
// keys the import silently drops.
var importGrammarKeys = map[string]bool{
	"title":     true,
	"desc":      true,
	"labels":    true,
	"ref":       true,
	"blockedBy": true,
	"foundIn":   true,
	"kind":      true,
	"criteria":  true,
	"children":  true,
}

// collectUnknownKeys walks the chosen tasks block and returns the sorted, unique
// set of keys outside the import grammar: top-level keys other than `tasks`, and
// per-task keys not in importGrammarKeys (recursing through `children`). These
// are silently dropped on import, so callers surface them as a lossy-import
// warning. Parse problems are ignored here — they are handled on the typed decode.
func collectUnknownKeys(body string) []string {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	unknown := make(map[string]bool)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if key == "tasks" {
			collectUnknownTaskKeys(root.Content[i+1], unknown)
			continue
		}
		unknown[key] = true
	}
	if len(unknown) == 0 {
		return nil
	}
	out := make([]string, 0, len(unknown))
	for k := range unknown {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// collectUnknownTaskKeys walks a `tasks` (or `children`) sequence node and adds
// any per-task key outside the grammar to unknown, recursing through children.
func collectUnknownTaskKeys(seq *yaml.Node, unknown map[string]bool) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			key := item.Content[i].Value
			if key == "children" {
				collectUnknownTaskKeys(item.Content[i+1], unknown)
				continue
			}
			if !importGrammarKeys[key] {
				unknown[key] = true
			}
		}
	}
}

func RunImport(db *sql.DB, filePath, parentShortID string, dryRun bool, actor string) (*ImportResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	blocks, parseErr := extractTasksBlocks(string(data))
	if len(blocks) == 0 {
		if parseErr != nil {
			return nil, fmt.Errorf("YAML parse error in %s: %w", filePath, parseErr)
		}
		return nil, fmt.Errorf("no importable tasks found in %s: provide a fenced ```yaml code block, or a bare YAML document whose top level is `tasks:`", filePath)
	}

	// Selection is unchanged: the first candidate block wins. But make that
	// choice observable — warn when it was ambiguous (more than one candidate)
	// or lossy (the chosen block carries keys outside the import grammar).
	chosen := blocks[0]
	yamlBody := chosen.body
	warnings := importSelectionWarnings(filePath, blocks)

	var raw rawRoot
	if err := yaml.Unmarshal([]byte(yamlBody), &raw); err != nil {
		return nil, fmt.Errorf("YAML parse error: %s", err.Error())
	}

	// Phase A: convert raw → parsed tree with YAML-path labels, and validate.
	tree, flat, err := buildParsedTree(raw.Tasks)
	if err != nil {
		return nil, err
	}
	if err := validateRefs(flat); err != nil {
		return nil, err
	}
	if err := validateKinds(tree, flat, parentShortID != "", parentShortID); err != nil {
		return nil, err
	}

	// Validate --parent target (before any writes).
	var parentTask *Task
	if parentShortID != "" {
		parentTask, err = GetTaskByShortID(db, parentShortID)
		if err != nil {
			return nil, err
		}
		if parentTask == nil {
			return nil, fmt.Errorf("parent task %q not found", parentShortID)
		}
	}

	// Build title index for blockedBy resolution (local to the import).
	titleCounts := make(map[string]int)
	for _, p := range flat {
		titleCounts[p.Title]++
	}
	refIndex := make(map[string]*parsedTask)
	for _, p := range flat {
		if p.Ref != "" {
			refIndex[p.Ref] = p
		}
	}
	titleIndex := make(map[string]*parsedTask)
	for _, p := range flat {
		if titleCounts[p.Title] == 1 {
			titleIndex[p.Title] = p
		}
	}

	index := importIndex{
		refs:        refIndex,
		titles:      titleIndex,
		titleCounts: titleCounts,
	}

	// Pre-resolve blockedBy — some resolutions hit existing DB rows.
	blockedByResolved := make(map[*parsedTask][]resolvedRef)
	for _, p := range flat {
		if len(p.BlockedBy) == 0 {
			continue
		}
		list := make([]resolvedRef, 0, len(p.BlockedBy))
		for i, entry := range p.BlockedBy {
			r, err := index.resolve(db, p.pathLabel, fmt.Sprintf("blockedBy[%d]", i), entry)
			if err != nil {
				return nil, err
			}
			list = append(list, r)
		}
		blockedByResolved[p] = list
	}

	// foundIn resolves the same three ways, one value per task. Recorded in
	// the same second pass as blockedBy so a plan can name a task the
	// document has not reached yet.
	foundInResolved := make(map[*parsedTask]resolvedRef)
	for _, p := range flat {
		if p.FoundIn == "" {
			continue
		}
		r, err := index.resolve(db, p.pathLabel, "foundIn", p.FoundIn)
		if err != nil {
			return nil, err
		}
		if r.local == p {
			return nil, fmt.Errorf(
				"%s: foundIn %q refers to the task itself; a task cannot be found in itself",
				p.pathLabel, p.FoundIn,
			)
		}
		foundInResolved[p] = r
	}

	// Build echo order. If dry-run, emit placeholders. Warnings ride along on
	// both paths (computed before the dry-run early return below).
	result := &ImportResult{DryRun: dryRun, Warnings: warnings}
	if dryRun {
		for i, p := range flat {
			parent := ""
			if p != nil && isRoot(flat, tree, p) && parentTask != nil {
				parent = parentTask.ShortID
			} else if pp := findParsedParent(tree, p); pp != nil {
				parent = fmt.Sprintf("<new-%d>", pp.flatIndex+1)
			}
			var blockedBy []string
			for _, r := range blockedByResolved[p] {
				blockedBy = append(blockedBy, dryRunRefID(r))
			}
			foundIn := ""
			if r, ok := foundInResolved[p]; ok {
				foundIn = dryRunRefID(r)
			}
			result.Tasks = append(result.Tasks, ImportedTask{
				ID:        fmt.Sprintf("<new-%d>", i+1),
				Title:     p.Title,
				Parent:    parent,
				BlockedBy: blockedBy,
				Kind:      echoKind(p.Kind),
				FoundIn:   foundIn,
			})
		}
		return result, nil
	}

	// Phase B: single transaction.
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	shortIDByParsed := make(map[*parsedTask]string)
	dbIDByParsed := make(map[*parsedTask]int64)
	// Found-in edges resolve after every row exists, so the echo entry has to
	// be reachable again once the source's short ID is known.
	resultIndexByParsed := make(map[*parsedTask]int)

	// Insert in pre-order DFS so parents exist before children. Each node
	// receives an explicit sort key so findNextSibling's strict-greater
	// comparison can distinguish imported siblings.
	var insert func(node *parsedTask, parentDBID *int64, parentShort string, sortKey string) error
	insert = func(node *parsedTask, parentDBID *int64, parentShort string, sortKey string) error {
		sid, err := generateShortID(tx)
		if err != nil {
			return err
		}
		now := CurrentNowFunc().Unix()
		var id int64
		err = tx.QueryRow(`
			INSERT INTO tasks (short_id, parent_id, title, description, status, sort_key, created_at, updated_at, kind)
			VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?)
			RETURNING id
		`, sid, parentDBID, node.Title, node.Desc, sortKey, now, now, string(node.Kind)).Scan(&id)
		if err != nil {
			return err
		}
		shortIDByParsed[node] = sid
		dbIDByParsed[node] = id

		// Import still writes the tables itself — it moves onto apply with the
		// relations family — but its created events carry the new id anyway,
		// so nothing in the log is a payload apply would reject.
		createdPayload := CreatedPayload{
			ShortID:     sid,
			ParentID:    parentShort,
			Title:       node.Title,
			Description: node.Desc,
			SortKey:     sortKey,
		}
		// Mirrors `add --kind issue`: the default is silent, so a plain plan's
		// event stream is byte-for-byte what it was.
		if node.Kind.IsIssue() {
			createdPayload.Kind = string(node.Kind)
		}
		if err := recordEvent(tx, id, EventCreated, actor, createdPayload); err != nil {
			return err
		}

		if len(node.Labels) > 0 {
			added, _, err := insertLabels(tx, id, node.Labels)
			if err != nil {
				return err
			}
			if len(added) > 0 {
				if err := recordEvent(tx, id, EventLabeled, actor, LabeledPayload{
					Names:    added,
					Existing: []string{},
				}); err != nil {
					return err
				}
			}
		}

		if len(node.Criteria) > 0 {
			inserted, err := insertCriteria(tx, id, node.Criteria)
			if err != nil {
				return err
			}
			if err := recordEvent(tx, id, EventCriteriaAdded, actor, CriteriaAddedPayload{
				Criteria: criteriaEventDetail(inserted),
			}); err != nil {
				return err
			}
		}

		resultIndexByParsed[node] = len(result.Tasks)
		result.Tasks = append(result.Tasks, ImportedTask{
			ID:     sid,
			Title:  node.Title,
			Parent: parentShort,
			Kind:   echoKind(node.Kind),
		})

		// Children of this just-inserted node have no pre-existing
		// siblings in the DB, so their keys start at the front of the space.
		childKeys, err := SortKeySequence("", len(node.Children))
		if err != nil {
			return err
		}
		for i, child := range node.Children {
			cid := id
			if err := insert(child, &cid, sid, childKeys[i]); err != nil {
				return err
			}
		}
		return nil
	}

	var rootParentDBID *int64
	rootParentShort := ""
	if parentTask != nil {
		pid := parentTask.ID
		rootParentDBID = &pid
		rootParentShort = parentTask.ShortID
	}

	// Offset the import's roots by any pre-existing siblings so we don't
	// collide with existing tasks under the target parent (or at DB root
	// when --parent is omitted).
	rootKeys, err := sortKeysForNewChildren(tx, rootParentDBID, len(tree))
	if err != nil {
		return nil, err
	}
	for i, root := range tree {
		if err := insert(root, rootParentDBID, rootParentShort, rootKeys[i]); err != nil {
			return nil, err
		}
	}

	// Resolve blockedBy after all inserts (forward references).
	for parsed, list := range blockedByResolved {
		blockedDBID := dbIDByParsed[parsed]
		for _, r := range list {
			var blockerDBID int64
			if r.local != nil {
				blockerDBID = dbIDByParsed[r.local]
			} else {
				blockerDBID = r.dbTask.ID
			}
			if _, err := tx.Exec(
				"INSERT OR IGNORE INTO blocks (blocker_id, blocked_id) VALUES (?, ?)",
				blockerDBID, blockedDBID,
			); err != nil {
				return nil, err
			}
			var blockerShort, blockedShort string
			blockedShort = shortIDByParsed[parsed]
			if r.local != nil {
				blockerShort = shortIDByParsed[r.local]
			} else {
				blockerShort = r.dbTask.ShortID
			}
			if err := recordEvent(tx, blockedDBID, EventBlocked, actor, BlockedPayload{
				BlockedID: blockedShort,
				BlockerID: blockerShort,
			}); err != nil {
				return nil, err
			}
		}
	}

	// Record found-in edges after all inserts, for the same forward-reference
	// reason blockedBy waits.
	for parsed, r := range foundInResolved {
		task, err := GetTaskByShortID(tx, shortIDByParsed[parsed])
		if err != nil {
			return nil, err
		}
		source := r.dbTask
		if r.local != nil {
			source, err = GetTaskByShortID(tx, shortIDByParsed[r.local])
			if err != nil {
				return nil, err
			}
		}
		if err := setFoundInTx(tx, task, source, task.ShortID, source.ShortID, actor); err != nil {
			return nil, err
		}
		if i, ok := resultIndexByParsed[parsed]; ok {
			result.Tasks[i].FoundIn = source.ShortID
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// importSelectionWarnings builds the non-blocking stderr warnings for an
// import: one when more than one candidate `tasks:` block exists (naming the
// chosen one), and one when the chosen block carries keys outside the grammar
// (which are silently dropped). Returns nil for the clean, unambiguous case.
func importSelectionWarnings(filePath string, blocks []tasksBlock) []string {
	var warnings []string
	chosen := blocks[0]
	if len(blocks) > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"%s: found %d code blocks with a top-level `tasks:` key; importing the first (%s fence at line %d) and ignoring the other %d",
			filePath, len(blocks), fenceLangName(chosen.lang), chosen.startLine, len(blocks)-1,
		))
	}
	if unknown := collectUnknownKeys(chosen.body); len(unknown) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%s: ignored %d key(s) outside the import grammar: %s",
			filePath, len(unknown), strings.Join(unknown, ", "),
		))
	}
	return warnings
}

// fenceLangName renders a fence info string for human messages, naming the
// unlabeled case explicitly.
func fenceLangName(lang string) string {
	if lang == "" {
		return "unlabeled"
	}
	return lang
}

// sortKeysForNewChildren returns n consecutive sort keys that follow every
// existing child of parentDBID (or every root-level task when nil). Used by
// RunImport so an imported tree lands after the siblings already there.
func sortKeysForNewChildren(tx dbtx, parentDBID *int64, n int) ([]string, error) {
	var last string
	q := "SELECT COALESCE(MAX(sort_key), '') FROM tasks WHERE " + parentFilterSQL(parentDBID)
	if err := tx.QueryRow(q, parentFilterArgs(parentDBID)...).Scan(&last); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return SortKeySequence(last, n)
}

// buildParsedTree converts the raw YAML tree into parsed tasks, assigning YAML-path
// labels (e.g. `tasks[0].children[1]`) and a flat pre-order index. Rejects rows
// missing the title key.
func buildParsedTree(rawList []*rawTask) ([]*parsedTask, []*parsedTask, error) {
	var flat []*parsedTask
	var walk func(list []*rawTask, parentPath string) ([]*parsedTask, error)
	walk = func(list []*rawTask, parentPath string) ([]*parsedTask, error) {
		var out []*parsedTask
		for i, r := range list {
			var path string
			if parentPath == "" {
				path = fmt.Sprintf("tasks[%d]", i)
			} else {
				path = fmt.Sprintf("%s.children[%d]", parentPath, i)
			}
			if !r.titlePresent || strings.TrimSpace(r.Title) == "" {
				return nil, fmt.Errorf("%s: title is required", path)
			}
			labels, lerr := validateImportLabels(path, r.Labels)
			if lerr != nil {
				return nil, lerr
			}
			criteria, cerr := validateImportCriteria(path, r.Criteria)
			if cerr != nil {
				return nil, cerr
			}
			kind := KindTask
			if r.kindPresent {
				k, kerr := ParseTreeKind(r.Kind)
				if kerr != nil {
					return nil, fmt.Errorf("%s: %s", path, kerr.Error())
				}
				kind = k
			}
			p := &parsedTask{
				Title:       r.Title,
				Desc:        r.Desc,
				Labels:      labels,
				Ref:         r.Ref,
				BlockedBy:   r.BlockedBy,
				FoundIn:     strings.TrimSpace(r.FoundIn),
				Criteria:    criteria,
				Kind:        kind,
				kindPresent: r.kindPresent,
				pathLabel:   path,
				flatIndex:   len(flat),
			}
			flat = append(flat, p)
			children, err := walk(r.Children, path)
			if err != nil {
				return nil, err
			}
			p.Children = children
			out = append(out, p)
		}
		return out, nil
	}
	tree, err := walk(rawList, "")
	if err != nil {
		return nil, nil, err
	}
	return tree, flat, nil
}

// validateImportLabels normalizes a per-task labels list using the same rules
// the CLI applies (trim whitespace, reject empty, reject commas), and dedupes
// preserving first-seen order. Errors include the YAML path so users can
// locate the offending entry.
func validateImportLabels(path string, raw []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for i, r := range raw {
		name, err := validateLabelName(r)
		if err != nil {
			return nil, fmt.Errorf("%s: labels[%d]: %s", path, i, err.Error())
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// validateImportCriteria normalizes acceptance-criterion labels from YAML.
// All imported criteria start in the pending state; transitions land later via
// `job done --criterion` or `job edit --set-criterion`.
func validateImportCriteria(path string, raw []string) ([]Criterion, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Criterion, 0, len(raw))
	for i, r := range raw {
		label, err := validateCriterionLabel(r)
		if err != nil {
			return nil, fmt.Errorf("%s: criteria[%d]: %s", path, i, err.Error())
		}
		// Order comes from the slice: insertCriteria mints a key per row in
		// the order it is given.
		out = append(out, Criterion{Label: label, State: CriterionPending})
	}
	return out, nil
}

// decodeCriteria accepts a YAML list of bare-string labels:
//
//	criteria: [Tests pass, Docs updated]
func decodeCriteria(n *yaml.Node, dst *[]string) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("criteria must be a list")
	}
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("criteria entries must be strings; all imported criteria start as pending")
		}
		var s string
		if err := item.Decode(&s); err != nil {
			return err
		}
		*dst = append(*dst, s)
	}
	return nil
}

// validateRefs ensures refs are unique across the import.
func validateRefs(flat []*parsedTask) error {
	seen := make(map[string]*parsedTask)
	for _, p := range flat {
		if p.Ref == "" {
			continue
		}
		if prior, ok := seen[p.Ref]; ok {
			return fmt.Errorf("%s: ref %q is already used at %s", p.pathLabel, p.Ref, prior.pathLabel)
		}
		seen[p.Ref] = p
	}
	return nil
}

func isRoot(flat []*parsedTask, tree []*parsedTask, p *parsedTask) bool {
	return slices.Contains(tree, p)
}

func findParsedParent(tree []*parsedTask, target *parsedTask) *parsedTask {
	var walk func(node *parsedTask) *parsedTask
	walk = func(node *parsedTask) *parsedTask {
		for _, c := range node.Children {
			if c == target {
				return node
			}
			if found := walk(c); found != nil {
				return found
			}
		}
		return nil
	}
	for _, root := range tree {
		if root == target {
			return nil
		}
		if found := walk(root); found != nil {
			return found
		}
	}
	return nil
}
