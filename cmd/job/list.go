package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
	"strconv"
)

func newListCmd() *cobra.Command {
	var format string
	var labelFilter string
	var mine bool
	var claimedBy string
	var grepPattern string
	var allFlag bool
	var statusFilter string
	var openFlag bool
	var sinceFlag string
	var noTruncate bool
	var issuesFlag bool
	cmd := &cobra.Command{
		Use:     "ls [parent]",
		Aliases: []string{"list", "tree"},
		Short:   "List tasks in tree format",
		Long: `List tasks. By default shows only actionable (available, unblocked, unclaimed) tasks.

Scope: when given a parent, ls returns the full subtree under that parent (not just direct children). Combine with the filters below to narrow the recursive walk.

Use --all to include claimed and blocked tasks. Recently closed tasks render inline under their open parent when context is local; otherwise they collect into a flat "Recently closed (N of M)" footer below the tree, capped at the 10 most recent. Pass --since <window> (5m, 2h, 7d) or --since <count> (e.g. 50) to widen the footer, or --no-truncate for full closed history. The two are mutually exclusive.
Use --label <name> to filter to tasks carrying that label.
Use --mine to show only tasks claimed by the caller (via --as or default identity).
Use --claimed-by <name> to show tasks claimed by a specific agent.
Use --grep <pattern> for case-insensitive title search.
Composes: 'ls --mine --label p0', 'ls --claimed-by alice --all'.
Use --format=json for machine-readable output (full closed history, no cap).

Issue-tree roots are omitted from the default forest and summarized in one trailer line, 'Issues: N open · job ls --issues'. Pass --issues to see only the issue-trees instead (same filters apply within them). An explicit parent id names its own tree either way, so --issues has no effect once you've already scoped to one. --format=json always includes every root (with its 'kind'); --issues narrows that array to issue roots too.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			var parentShortID string
			showAll := allFlag
			for _, arg := range args {
				if arg == "all" {
					showAll = true
				} else {
					parentShortID = arg
				}
			}

			if mine && claimedBy != "" {
				return fmt.Errorf("cannot use both --mine and --claimed-by")
			}
			if openFlag && statusFilter != "" && statusFilter != "open" {
				return fmt.Errorf("cannot use --open with --status=%s", statusFilter)
			}
			effectiveStatus := statusFilter
			if openFlag {
				effectiveStatus = "open"
			}
			if effectiveStatus != "" {
				normalized, verr := job.ValidateStatusFilter(effectiveStatus)
				if verr != nil {
					return verr
				}
				effectiveStatus = normalized
			}

			var claimedByFilter string
			if mine {
				name, err := job.ResolveIdentity(db, asFlag)
				if err != nil {
					return err
				}
				if name == "" {
					return fmt.Errorf("no identity to scope to. Use --as <name> or set a default identity.")
				}
				claimedByFilter = name
			} else if claimedBy != "" {
				claimedByFilter = claimedBy
			}

			// Closed-tail cap: --format=json bypasses the cap (callers
			// paginate themselves); markdown uses the default 10. The tail
			// is gathered for `--all` only — bare `ls` keeps its
			// actionable-only contract.
			closedCap := 0
			var sinceUnix int64
			if sinceFlag != "" && noTruncate {
				return fmt.Errorf("--since and --no-truncate are mutually exclusive")
			}
			if noTruncate {
				closedCap = -1
			} else if sinceFlag != "" {
				cap, since, perr := parseSinceFlag(sinceFlag)
				if perr != nil {
					return perr
				}
				closedCap = cap
				sinceUnix = since
			}
			if format == "json" {
				closedCap = -1
				sinceUnix = 0
			}
			renderTail := showAll

			// Kind-scoping only means something over the unscoped forest —
			// an explicit parent already names its own tree (decision: `ls
			// <issue-root-id>` is unchanged). JSON's default array never
			// drops roots (a consumer can split them itself via `kind`);
			// --issues opts a JSON caller into the same split text gets.
			issuesScope := parentShortID == ""
			scopeByKind := issuesScope && (format != "json" || issuesFlag)

			kindScope := job.ListKindScopeAny
			if scopeByKind {
				if issuesFlag {
					kindScope = job.ListKindScopeIssues
				} else {
					kindScope = job.ListKindScopeTasks
				}
			}

			filter := job.ListFilter{
				ParentID:            parentShortID,
				ShowAll:             showAll,
				Label:               labelFilter,
				ClaimedByActor:      claimedByFilter,
				GrepPattern:         grepPattern,
				Status:              effectiveStatus,
				ClosedTailCap:       closedCap,
				ClosedTailSinceUnix: sinceUnix,
				KindScope:           kindScope,
			}
			result, err := job.RunListWithTail(db, filter)
			if err != nil {
				return err
			}
			if !renderTail {
				result.ClosedTail = nil
				result.ClosedTotal = 0
			}

			if issuesFlag && issuesScope && format != "json" && result.IssuesOpen == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No issue-tree roots. Run `job add <title> --kind issue` (or `job kind <id> issue`) to create one.")
				return nil
			}
			nodes := result.Open

			// printIssuesTrailer appends the `Issues: N open · job ls
			// --issues` pointer after every other line of output, in every
			// default-mode markdown code path below (including the "empty"
			// branches) — it is a no-op whenever it doesn't apply.
			printIssuesTrailer := func() error {
				if !issuesScope || issuesFlag || format == "json" {
					return nil
				}
				if result.IssuesOpen != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Issues: %d open · job ls --issues\n", *result.IssuesOpen)
				}
				return nil
			}

			if format == "json" {
				if renderTail {
					if err := writeListJSONWithTail(cmd, db, result); err != nil {
						return err
					}
					return nil
				} else {
					blockers, err := job.CollectBlockers(db, nodes)
					if err != nil {
						return err
					}
					_ = blockers
					b, err := job.FormatTaskNodesJSON(nodes)
					if err != nil {
						return err
					}
					cmd.OutOrStdout().Write(b)
					fmt.Fprintln(cmd.OutOrStdout())
				}
				return nil
			}

			if len(nodes) == 0 && len(result.ClosedTail) == 0 {
				if grepPattern != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "No tasks match `%s`. Try --all to include blocked / done / canceled.\n", grepPattern)
					return printIssuesTrailer()
				}
				if claimedByFilter != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "No tasks claimed by %s.\n", claimedByFilter)
					return printIssuesTrailer()
				}
				total, done, cerr := countTasks(db, parentShortID)
				if cerr != nil {
					return cerr
				}
				job.RenderListEmpty(cmd.OutOrStdout(), total, done)
				return printIssuesTrailer()
			}
			if len(nodes) > 0 {
				blockers, err := job.CollectBlockers(db, nodes)
				if err != nil {
					return err
				}
				labels, err := collectLabels(db, nodes)
				if err != nil {
					return err
				}
				if issuesFlag && issuesScope {
					job.RenderIssueRootList(cmd.OutOrStdout(), nodes, blockers, labels)
				} else {
					job.RenderMarkdownList(cmd.OutOrStdout(), nodes, blockers, labels, 0)
				}
			}
			if renderTail && len(result.ClosedTail) > 0 {
				var parents map[int64]job.ParentInfo
				omitBreadcrumb := parentShortID != ""
				if !omitBreadcrumb {
					parents, err = job.LoadParentBreadcrumbs(db, result.ClosedTail)
					if err != nil {
						return err
					}
				}
				job.RenderClosedTail(cmd.OutOrStdout(), result.ClosedTail, result.ClosedTotal, parents, omitBreadcrumb)
				if len(result.ClosedTail) < result.ClosedTotal {
					fmt.Fprintf(cmd.OutOrStdout(),
						"%d of %d recent closures shown — pass --since <window>, --since <count>, or --no-truncate for more.\n",
						len(result.ClosedTail), result.ClosedTotal)
				}
			}

			unscopedUnfiltered := parentShortID == "" && !showAll && !issuesFlag &&
				labelFilter == "" && claimedByFilter == "" && grepPattern == "" &&
				effectiveStatus == ""
			if unscopedUnfiltered && len(nodes) < 5 {
				fmt.Fprintln(cmd.OutOrStdout(), "Showing actionable tasks only. Use --all to include blocked / done / canceled tasks.")
			}
			return printIssuesTrailer()
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().StringVarP(&labelFilter, "label", "l", "", "filter to tasks carrying this label")
	cmd.Flags().BoolVar(&mine, "mine", false, "show only tasks claimed by the caller")
	cmd.Flags().StringVar(&claimedBy, "claimed-by", "", "show only tasks claimed by this agent")
	cmd.Flags().StringVar(&grepPattern, "grep", "", "case-insensitive substring filter on title")
	cmd.Flags().BoolVar(&allFlag, "all", false, "include done, claimed, and blocked tasks")
	cmd.Flags().StringVar(&statusFilter, "status", "", "filter to one status (available|claimed|done|canceled|open)")
	cmd.Flags().BoolVar(&openFlag, "open", false, "shortcut for --status=open (anything not done or canceled)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "limit Recently closed footer to a duration (5m, 2h, 7d) or a count (50)")
	cmd.Flags().BoolVar(&noTruncate, "no-truncate", false, "disable the Recently closed footer cap (full closed history)")
	cmd.Flags().BoolVar(&issuesFlag, "issues", false, "show only issue-tree roots (omitted from the default forest; see the Issues: trailer)")
	return cmd
}

// parseSinceFlag interprets the --since argument either as a positive
// integer count (50 → cap of 50 rows, no time filter) or as a duration
// (5m / 2h / 7d → since-unix lower bound, default cap retained). The
// caller must reject combinations with --no-truncate before calling.
func parseSinceFlag(raw string) (cap int, sinceUnix int64, err error) {
	if n, perr := strconv.ParseInt(raw, 10, 64); perr == nil && n > 0 {
		return int(n), 0, nil
	}
	seconds, derr := job.ParseDuration(raw)
	if derr != nil {
		return 0, 0, fmt.Errorf("--since: expected duration (5m, 2h, 7d) or positive count (50), got %q", raw)
	}
	return 0, job.CurrentNowFunc().Unix() - seconds, nil
}

// writeListJSONWithTail emits the `ls --all --format=json` shape:
//
//	{"open": [...task tree...], "closed_tail": [...flat closed rows...]}
//
// JSON is unbounded by design — scripts handle pagination themselves —
// so the closed-tail cap is bypassed before this is called.
func writeListJSONWithTail(cmd *cobra.Command, db *sql.DB, result *job.ListResult) error {
	type closedRow struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		ClosedAt    int64  `json:"closed_at"`
		ParentID    string `json:"parent_id,omitempty"`
		ParentTitle string `json:"parent_title,omitempty"`
	}
	openBytes, err := job.FormatTaskNodesJSON(result.Open)
	if err != nil {
		return err
	}
	var openVal any
	if err := json.Unmarshal(openBytes, &openVal); err != nil {
		return err
	}
	parents, err := job.LoadParentBreadcrumbs(db, result.ClosedTail)
	if err != nil {
		return err
	}
	rows := make([]closedRow, 0, len(result.ClosedTail))
	for _, r := range result.ClosedTail {
		row := closedRow{
			ID:       r.Task.ShortID,
			Title:    r.Task.Title,
			Status:   r.Task.Status,
			ClosedAt: r.ClosedAt,
		}
		if r.Task.ParentID != nil {
			if p, ok := parents[*r.Task.ParentID]; ok {
				row.ParentID = p.ShortID
				row.ParentTitle = p.Title
			}
		}
		rows = append(rows, row)
	}
	out := map[string]any{
		"open":         openVal,
		"closed_tail":  rows,
		"closed_total": result.ClosedTotal,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	cmd.OutOrStdout().Write(b)
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}
