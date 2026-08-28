package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	dbPath string
	asFlag string
)

// looksLikeShortID returns true if s has the shape of a job short-ID:
// exactly 5 characters, each in [a-zA-Z0-9]. Used to detect when a user
// passed prose where an ID was expected.
func looksLikeShortID(s string) bool {
	if len(s) != 5 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// resolveMessage expands a -m flag value. Three forms:
//   - "@path": read the file at path. Errors clearly if the file is missing.
//   - "-": read from stdin.
//   - anything else: returned as the literal string.
//
// Rationale: shell-quoting multi-line evidence payloads into -m "..." is
// painful (backticks, nested quotes); file and stdin forms sidestep it.
func resolveMessage(raw string, stdin io.Reader) (string, error) {
	return resolveMessageAs(raw, stdin, dashMSource)
}

// bodySource names how the operator spelled a body argument, so a read error
// quotes the flag they actually typed rather than the internal form.
type bodySource struct {
	// Flag is the flag name to blame: "-m" or "-F".
	Flag string
	// Sigil prefixes the path in error messages — "@" for -m, whose file
	// form is `-m @path`, and empty for -F, whose argument is a bare path.
	Sigil string
}

var (
	dashMSource = bodySource{Flag: "-m", Sigil: "@"}
	dashFSource = bodySource{Flag: "-F", Sigil: ""}
)

// resolveMessageAs is resolveMessage with the spelling that error messages
// should blame. The same body can arrive as `-m @path` or as `-F path`, and an
// error naming the flag the operator did not type sends them looking in the
// wrong place.
//
// Both readers trim trailing newlines, as git's commit-message cleanup does:
// a heredoc, a pipe or an editor-saved file almost always ends in a newline
// the author did not mean, and a stored body should not carry it.
func resolveMessageAs(raw string, stdin io.Reader, src bodySource) (string, error) {
	if raw == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("%s -: read stdin: %w", src.Flag, err)
		}
		return strings.TrimRight(string(b), "\n\r"), nil
	}
	if strings.HasPrefix(raw, "@") {
		path := raw[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s %s%s: read note file: %w", src.Flag, src.Sigil, path, err)
		}
		return strings.TrimRight(string(b), "\n\r"), nil
	}
	return raw, nil
}

// registerFileFlag registers the shared -F/--file flag, the file form of a
// verb's inline body flag. noun names what is being read ("note", "reason",
// "description") and inline is the long name of the flag it mirrors.
//
// One registration point keeps the spelling, the shorthand and the help text
// identical across all seven body-taking verbs.
func registerFileFlag(cmd *cobra.Command, target *string, noun, inline string) {
	cmd.Flags().StringVarP(target, "file", "F", "",
		fmt.Sprintf("read the %s from <path> (file form of --%s); `-F -` reads stdin", noun, inline))
}

// bodyFlagSpec describes one verb's free-text body inputs for resolveBodyFlag.
type bodyFlagSpec struct {
	// Verb names the command in error messages ("note", "cancel", …).
	Verb string
	// InlineName is the long name of the inline body flag whose file form
	// -F provides: "message" on note/done/claim/release, "reason" on
	// cancel, "desc" on add/edit.
	InlineName string
	// Inline is that flag's value as parsed.
	Inline string
	// File is the -F/--file value as parsed.
	File string
	// InlineLiteral suppresses @path / `-` expansion of the inline value.
	// add and edit set it: --desc has always been literal there, and a
	// description that legitimately begins with "@" must survive.
	InlineLiteral bool
	// HasPositional reports that the verb also received its body as a
	// positional argument, which -F conflicts with too.
	HasPositional bool
}

// resolveBodyFlag folds -F/--file into a verb's inline body flag and resolves
// the result, so every body-taking verb shares one set of conflict checks and
// one reader instead of seven copies.
//
// -F <path> is exactly `--<inline> @<path>`; `-F -` is `--<inline> -`. Passing
// -F alongside the inline flag, or alongside a positional body, is an error —
// git rejects `commit -m … -F …` the same way, and silently preferring one
// input over the other loses whichever the operator meant.
//
// provided reports whether either spelling supplied a body at all, so callers
// keep their own "no body given" and "clear the field" handling.
func resolveBodyFlag(cmd *cobra.Command, spec bodyFlagSpec) (text string, provided bool, err error) {
	hasFile := cmd.Flags().Changed("file")
	hasInline := cmd.Flags().Changed(spec.InlineName)

	switch {
	case hasFile && hasInline:
		return "", false, fmt.Errorf("%s: -F/--file and --%s are mutually exclusive — pass the body one way",
			spec.Verb, spec.InlineName)
	case hasFile && spec.HasPositional:
		return "", false, fmt.Errorf("%s: -F/--file and a positional body are mutually exclusive — pass the body one way",
			spec.Verb)
	case hasFile:
		raw := spec.File
		if raw != "-" {
			raw = "@" + raw
		}
		resolved, rerr := resolveMessageAs(raw, cmd.InOrStdin(), dashFSource)
		if rerr != nil {
			return "", false, rerr
		}
		return resolved, true, nil
	case hasInline:
		if spec.InlineLiteral {
			return spec.Inline, true, nil
		}
		resolved, rerr := resolveMessageAs(spec.Inline, cmd.InOrStdin(), dashMSource)
		if rerr != nil {
			return "", false, rerr
		}
		return resolved, true, nil
	}
	return "", false, nil
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "job",
		Short:         "A lightweight CLI task manager",
		Long:          rootLongHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&dbPath, "db", "", "path to job database (default: .jobs.db)")
	cmd.PersistentFlags().StringVar(&asFlag, "as", "", "identity to use for writes (e.g. --as alice)")
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newDoneCmd())
	cmd.AddCommand(newReopenCmd())
	cmd.AddCommand(newEditCmd())
	cmd.AddCommand(newNoteCmd())
	cmd.AddCommand(newCancelCmd())
	cmd.AddCommand(newMoveCmd())
	cmd.AddCommand(newKindCmd())
	cmd.AddCommand(newSplitCmd())
	cmd.AddCommand(newInfoCmd())
	cmd.AddCommand(newBlockCmd())
	cmd.AddCommand(newUnblockCmd())
	cmd.AddCommand(newFoundInCmd())
	cmd.AddCommand(newClaimCmd())
	cmd.AddCommand(newHeartbeatCmd())
	cmd.AddCommand(newReleaseCmd())
	cmd.AddCommand(newUnclaimCmd())
	cmd.AddCommand(newLabelCmd())
	cmd.AddCommand(newIdentityCmd())
	cmd.AddCommand(newNextCmd())
	cmd.AddCommand(newLogCmd())
	cmd.AddCommand(newTailCmd())
	cmd.AddCommand(newImportCmd())
	cmd.AddCommand(newSchemaCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newOrientCmd())
	cmd.AddCommand(newFocusCmd())
	cmd.AddCommand(newServeCmd())
	return cmd
}

const rootLongHelp = `job — the CLI for Jobs, a hierarchical task tracker backed by an event store in SQLite.

Tasks form a tree. Every write is attributed to a named identity and recorded as an event, so history is replayable and multiple agents can coordinate through durable short-TTL claims.

QUICKSTART

  1. Initialize:  job init
     Records your $USER as the default identity; subsequent writes need no --as.

  2. Open with status:  job status
     Run at the start of every session — it's both the identity check and the landscape briefing.

  3. Plan (for multi-task work):

       ` + "```" + `yaml
       tasks:
         - title: Root task
           children:
             - title: First subtask
             - title: Second subtask
       ` + "```" + `

     Then:     job import plan.md (preview first with --dry-run)

     Plans can indicate labels and blockers. See the full schema using 'job schema'

  4. Work:     job claim --next            (grab the next available leaf)
               job claim <id> 2h          (long-running claim — overrides 30m default)
               job note <id> -m "progress"
               job done <id> -m "completion notes"
               job done <id> -m "notes" --claim-next  (closes <id> with a note and opens the next available leaf in one move)

  5. Observe:  job ls                      (actionable tasks in tree view)
               job ls --all                (live work + recently closed footer; --since/--no-truncate to widen)
               job ls <i>                  (print the children of a task)
               job show <id>               (full description and children; --ancestors prepends parent chain)
               job log <id>                (event history)

IDENTITY

  Every write is attributed. Resolution order, first match wins:
    1. --as <name> on the call
    2. A DB-level default identity (set at init — defaults to $USER)
    3. Otherwise: error

    job identity set <name>         change the default (itself requires --as)
    job init --strict               opt out of defaults; require --as on every write

  Unless --strict is set, you can usually skip --as, reserving it only for sub-agents; pass unique names to agents to use with --as.

VERBS (grouped by role)

  Setup:        init, identity, schema
  Planning:     add, import, edit, block, move, label
    Reserved label:  "decision" → surfaces as "Decision:" in status until done/canceled
  Execution:    claim, claim --next, release/unclaim, note, done, reopen, cancel, heartbeat
  Observation:  ls, show, log, status, next, next all, orient, focus, tail
  Web UI:       serve (read-only dashboard, binds 127.0.0.1:7823 by default)

  Grammar:
    Multi-operation verbs (label, block):  job <verb> <add|remove> <args>
    Single-operation verbs:                job <verb> <id> [--flags]

  Short flags:
    -m  free-text body (note -m, done -m, cancel -m)
    -F  --file: the same body read from a path, as git spells it (-F - reads stdin)
    -d  --desc
    -t  --title (edit) / --timeout (tail)
    -l  --label
    -p  --parent (import)
    -n  --dry-run
    -s  --since (log)
    -e  --events
    -u  --users
    -q  --quiet
    -y  --yes

  For full options on any verb:  job <verb> --help

CLAIMS

  Claims default to 30m. Any write to a claimed task by its holder (note, edit, label add/remove) auto-extends the TTL. Reach for ` + "`heartbeat`" + ` only during a genuine pause ("thinking, not writing").

  After a successful ` + "`done`" + `, the ack ends with a "Next:" hint naming the next suggested leaf. The walk prefers forward siblings, then earlier siblings, walking up the closed task's ancestor chain before crossing root trees; follow the hint to stay inside the current plan.

FOCUS

  Claiming any task focuses its root for you (per-actor, last claim wins). While focused, no-arg ` + "`claim --next`" + `, ` + "`next`" + `, ` + "`status`" + `'s Next: hint, and ` + "`orient`" + ` stay inside that root; an exhausted focused root fails loudly instead of handing you a leaf from another tree. Explicit ids always override. Focus releases when the root closes, or manually via ` + "`job focus --clear`" + `. ` + "`job focus`" + ` shows yours.

OUTPUT

  Markdown by default; ` + "`--format=json`" + ` on any read verb for machine-parsable output.

ORCHESTRATION

  For multi-agent workflows:
    job next all                       # full claimable frontier
    job tail <id> --format=json        # streaming JSON-lines event stream
    job tail --until-close <id>        # block until <id> closes
`

// renderClaimBriefing prints the `show <id>` briefing after a successful
// claim ack, separated by a blank line. The ack's first line stays the
// scriptable signal (scripts grep for "Claimed:"); the briefing folds
// the universal `claim X && show X` pattern into one call. Errors fetching
// the briefing are silently swallowed: the claim already succeeded and is
// already acknowledged — the briefing is the friendly add-on, not the
// load-bearing output.
func renderClaimBriefing(w io.Writer, db *sql.DB, shortID string) {
	info, err := job.RunInfo(db, shortID)
	if err != nil {
		return
	}
	fmt.Fprintln(w)
	job.RenderInfoMarkdown(w, info)
}

func openDBFromCmd() (*sql.DB, error) {
	path := job.ResolveDBPath(dbPath)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no job database found in %s. Run `job init` or specify a database with --db", path)
	}
	return job.OpenDB(path)
}

// requireAs resolves the writer identity for this call. Precedence:
//  1. --as flag
//  2. DB-level default identity (config.default_identity), unless strict mode
//  3. error: "identity required. Pass --as <name> ..."
//
// Lives in the CLI layer because it depends on the cobra-bound --as flag
// global; the domain layer supplies the underlying resolver via
// job.ResolveIdentity.
func requireAs(db *sql.DB) (string, error) {
	name, err := job.ResolveIdentity(db, asFlag)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("identity required. Pass --as <name> before the verb.")
	}
	if _, err := job.EnsureUser(db, name); err != nil {
		return "", err
	}
	return name, nil
}

func humanJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

func countTasks(db *sql.DB, parentShortID string) (total, done int, err error) {
	if parentShortID == "" {
		if err = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE deleted_at IS NULL").Scan(&total); err != nil {
			return 0, 0, err
		}
		if err = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE deleted_at IS NULL AND status = 'done'").Scan(&done); err != nil {
			return 0, 0, err
		}
		return total, done, nil
	}

	parent, lerr := job.GetTaskByShortID(db, parentShortID)
	if lerr != nil || parent == nil {
		return 0, 0, lerr
	}
	var nullDone sql.NullInt64
	err = db.QueryRow(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
			UNION ALL
			SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT COUNT(*), SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END)
		FROM tasks WHERE id IN (SELECT id FROM subtree) AND deleted_at IS NULL
	`, parent.ID).Scan(&total, &nullDone)
	if err != nil {
		return 0, 0, err
	}
	if nullDone.Valid {
		done = int(nullDone.Int64)
	}
	return total, done, nil
}

func collectLabels(db *sql.DB, nodes []*job.TaskNode) (map[int64][]string, error) {
	var ids []int64
	var walk func([]*job.TaskNode)
	walk = func(nodes []*job.TaskNode) {
		for _, node := range nodes {
			ids = append(ids, node.Task.ID)
			walk(node.Children)
		}
	}
	walk(nodes)
	return job.GetLabelsForTaskIDs(db, ids)
}

// AckLine is a single pre-formatted line in the done-ack output plan.
// Building a plan before rendering (instead of inlining fmt.Fprintfs) lets
// the builder apply append/skip conditions (suppress redundant lines,
// suppress Next when a claim fired) without tangling the render into
// nested branches.
type AckLine string

// doneAckOptions carries out-of-band context the plan builder needs that
// isn't part of the domain's DoneContext: whether --claim-next produced a
// Claimed line in this same call (which makes a Next line redundant).
type doneAckOptions struct {
	// suppressNext is true when a successful claim-next claim will be
	// emitted after the ack. The Claimed line already names the same
	// next-work; a separate Next line would duplicate it.
	suppressNext bool
	// actor, when set, is appended to single-result done lines as
	// "as=<actor>" so misattributed writes surface at the moment of action.
	actor string
}

func buildDoneAckLines(closed []*job.ClosedResult, alreadyDone []string, finalCtx *job.DoneContext, opts doneAckOptions) []AckLine {
	var lines []AckLine

	appendNoteEcho := func(note string) {
		if note == "" {
			return
		}
		count, preview := job.NotePreview(note)
		lines = append(lines, AckLine(fmt.Sprintf("  note: %d chars · %q", count, preview)))
	}

	asTag := ""
	if opts.actor != "" {
		asTag = " as=" + opts.actor
	}
	single := len(closed) == 1 && len(alreadyDone) == 0 && len(closed[0].CascadeClosed) == 0
	if single {
		c := closed[0]
		lines = append(lines, AckLine(fmt.Sprintf("Done: %s %q%s", c.ShortID, c.Title, asTag)))
		appendNoteEcho(c.Note)
	} else if len(closed) == 1 && len(closed[0].CascadeClosed) > 0 && len(alreadyDone) == 0 {
		c := closed[0]
		lines = append(lines, AckLine(fmt.Sprintf("Done: %s %q (and %d subtasks)%s", c.ShortID, c.Title, len(c.CascadeClosed), asTag)))
		appendNoteEcho(c.Note)
	} else if len(closed) > 0 {
		lines = append(lines, AckLine(fmt.Sprintf("Closed %d tasks:", len(closed))))
		for _, c := range closed {
			if len(c.CascadeClosed) > 0 {
				lines = append(lines, AckLine(fmt.Sprintf("- Done: %s %q (and %d subtasks)", c.ShortID, c.Title, len(c.CascadeClosed))))
			} else {
				lines = append(lines, AckLine(fmt.Sprintf("- Done: %s %q", c.ShortID, c.Title)))
			}
			appendNoteEcho(c.Note)
		}
	}
	if len(alreadyDone) > 0 {
		lines = append(lines, AckLine(fmt.Sprintf("  already done: %s", strings.Join(alreadyDone, ", "))))
	}

	// Surface auto-closed ancestors (leaf-frontier cascade) on every closed
	// result. Track the highest auto-closed ancestor across all results so we
	// can suppress a redundant "All tasks in X complete." that would just
	// restate what the last Auto-closed line already said.
	highestAutoClosed := ""
	for _, c := range closed {
		for _, anc := range c.AutoClosedAncestors {
			lines = append(lines, AckLine(fmt.Sprintf("  Auto-closed: %s %q", anc.ShortID, anc.Title)))
			// AutoClosedAncestors is ordered nearest-parent first, so the
			// last element is the highest. Overwriting on each iteration
			// naturally leaves `highestAutoClosed` pointing at that element.
			highestAutoClosed = anc.ShortID
		}
	}

	if finalCtx == nil {
		return lines
	}
	// Trailing context is only meaningful when we actually closed something;
	// an already-done-only call has nothing to say about "what's next."
	if len(closed) == 0 {
		return lines
	}

	ctx := finalCtx

	// Improvement 1: suppress "All tasks in X complete." when X equals the
	// highest auto-closed ancestor. The Auto-closed line already emitted
	// conveys the same "this whole thing just closed" signal; saying it
	// twice is noise.
	if ctx.WholeTreeComplete && ctx.WholeTreeRootID != highestAutoClosed {
		lines = append(lines, AckLine(fmt.Sprintf("  All tasks in %s complete. (%d done, 0 open)", ctx.WholeTreeRootID, ctx.WholeTreeDoneCount)))
	}

	// Improvement 3: suppress Next: when --claim-next already claimed the
	// next work in the same call. The Claimed line names the same target,
	// so a Next line would be a stale duplicate. Skip-blocked info is kept
	// even when suppressing Next — it's context on a different sibling.
	if ctx.SkippedBlocked != nil && ctx.Next != nil {
		lines = append(lines, AckLine(fmt.Sprintf("  Next sibling %s is blocked on %s. Skipping to %s.",
			ctx.SkippedBlocked.ShortID, ctx.SkippedBlockedBy, ctx.Next.ShortID)))
	} else if !opts.suppressNext && ctx.Next != nil {
		lines = append(lines, AckLine(fmt.Sprintf("  Next: %s %q", ctx.Next.ShortID, ctx.Next.Title)))
	}

	// Only show "Parent X: N of M complete" when the parent is still open.
	// An auto-closed parent has nothing meaningful to report here.
	if ctx.ParentID != "" && !ctx.ParentWasDone && !ctx.ParentAutoClosed {
		lines = append(lines, AckLine(fmt.Sprintf("  Parent %s: %d of %d complete", ctx.ParentID, ctx.ParentDoneCount, ctx.ParentTotalCount)))
	}

	return lines
}

func renderDoneAck(w io.Writer, closed []*job.ClosedResult, alreadyDone []string, finalCtx *job.DoneContext) {
	renderDoneAckWithOptions(w, closed, alreadyDone, finalCtx, doneAckOptions{})
}

func renderDoneAckWithOptions(w io.Writer, closed []*job.ClosedResult, alreadyDone []string, finalCtx *job.DoneContext, opts doneAckOptions) {
	for _, line := range buildDoneAckLines(closed, alreadyDone, finalCtx, opts) {
		fmt.Fprintln(w, string(line))
	}
}

type doneJSONClosed struct {
	ID                  string               `json:"id"`
	Title               string               `json:"title"`
	CascadeClosed       []string             `json:"cascade_closed"`
	AutoClosedAncestors []doneJSONAutoClosed `json:"auto_closed_ancestors,omitempty"`
}

type doneJSONAutoClosed struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type doneJSONNext struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type doneJSONParent struct {
	ID    string `json:"id"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type doneJSON struct {
	Closed      []doneJSONClosed `json:"closed"`
	AlreadyDone []string         `json:"already_done"`
	Next        *doneJSONNext    `json:"next"`
	Parent      *doneJSONParent  `json:"parent"`
}

func renderDoneJSON(w io.Writer, closed []*job.ClosedResult, alreadyDone []string, ctx *job.DoneContext) error {
	out := doneJSON{
		AlreadyDone: alreadyDone,
	}
	if out.AlreadyDone == nil {
		out.AlreadyDone = []string{}
	}
	for _, c := range closed {
		jc := doneJSONClosed{ID: c.ShortID, Title: c.Title, CascadeClosed: c.CascadeClosed}
		if jc.CascadeClosed == nil {
			jc.CascadeClosed = []string{}
		}
		for _, anc := range c.AutoClosedAncestors {
			jc.AutoClosedAncestors = append(jc.AutoClosedAncestors, doneJSONAutoClosed{ID: anc.ShortID, Title: anc.Title})
		}
		out.Closed = append(out.Closed, jc)
	}
	if out.Closed == nil {
		out.Closed = []doneJSONClosed{}
	}
	if ctx != nil && len(closed) > 0 {
		if ctx.Next != nil {
			out.Next = &doneJSONNext{ID: ctx.Next.ShortID, Title: ctx.Next.Title}
		}
		if ctx.ParentID != "" {
			out.Parent = &doneJSONParent{ID: ctx.ParentID, Done: ctx.ParentDoneCount, Total: ctx.ParentTotalCount}
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
