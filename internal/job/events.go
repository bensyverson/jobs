package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// ErrTailTimeout is returned by RunTailUntilClose when --timeout expires
// with at least one watched task still open. main.go maps this to exit code 2.
var ErrTailTimeout = errors.New("tail --until-close timed out")

// defaultTailHiddenEvents are excluded from `tail` by default. Subscribers
// that want them (e.g. a watchdog watching for missing heartbeats) must opt
// in via --events. Phase 5 hard-codes "heartbeat" before the emitter ships
// in Phase 6 so the filter contract is stable across the boundary.
var defaultTailHiddenEvents = map[string]bool{
	"heartbeat": true,
}

type EventFilter struct {
	Types map[string]bool
	Users map[string]bool
}

func ParseFilterList(s string) map[string]bool {
	if s == "" {
		return nil
	}
	out := make(map[string]bool)
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func FilterEvents(events []EventEntry, filter EventFilter) []EventEntry {
	out := make([]EventEntry, 0, len(events))
	for _, e := range events {
		if filter.Types != nil {
			if !filter.Types[e.EventType] {
				continue
			}
		} else if defaultTailHiddenEvents[e.EventType] {
			continue
		}
		if filter.Users != nil && !filter.Users[e.Actor] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// RunLog returns the log for a single task-subtree when shortID is non-empty,
// or the global event log across every top-level tree when shortID is "".
// The empty form powers `job log` / `job log all`.
func RunLog(db *sql.DB, shortID string, since *int64) ([]EventEntry, error) {
	return RunLogFiltered(db, shortID, since, "")
}

// RunLogFiltered is RunLog plus an optional actor filter.
func RunLogFiltered(db *sql.DB, shortID string, since *int64, actor string) ([]EventEntry, error) {
	if shortID != "" {
		task, err := GetTaskByShortID(db, shortID)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("task %q not found", shortID)
		}
	}

	var extras []string
	var args []any
	if since != nil {
		extras = append(extras, "AND e.created_at >= ?")
		args = append(args, *since)
	}
	if actor != "" {
		extras = append(extras, "AND e.actor = ?")
		args = append(args, actor)
	}
	var extra strings.Builder
	for _, e := range extras {
		extra.WriteString(" " + e)
	}
	query, qargs := buildTreeEventsQuery(shortID, extra.String(), args)
	return queryEventEntries(db, query, qargs)
}

// RunTailUntilClose wraps RunTail with a watch-set that drains as watched
// tasks reach done/canceled. Terminal-event detection bypasses the display
// filter (filters hide events from the stream; they don't gate exit).
//
// Exit semantics:
//   - watch set drains before ctx done → return nil (caller exits 0)
//   - timeout fires with watched tasks still open → return ErrTailTimeout
//   - any other error → returned as-is (exit 1)
//
// Pre-flight: any id already done/canceled is reported and dropped before
// entering the poll loop. If the set drains entirely pre-flight, we exit 0
// without polling.
// DefaultTailUntilClosePollInterval is the cadence the cobra layer uses when
// invoking RunTailUntilClose. Tests pass a shorter interval so they don't pay
// a poll round-trip per fired event.
const DefaultTailUntilClosePollInterval = 1 * time.Second

func RunTailUntilClose(
	parentCtx context.Context,
	db *sql.DB,
	shortID string,
	watchIDs []string,
	timeout time.Duration,
	pollInterval time.Duration,
	quiet bool,
	format string,
	filter EventFilter,
	w io.Writer,
) error {
	if pollInterval <= 0 {
		pollInterval = DefaultTailUntilClosePollInterval
	}
	// Validate the positional id when provided. Empty shortID means global
	// scope (stream from all top-level trees); the watch set still names
	// tasks explicitly and those are validated below in the pre-flight loop.
	if shortID != "" {
		posTask, err := GetTaskByShortID(db, shortID)
		if err != nil {
			return err
		}
		if posTask == nil {
			return fmt.Errorf("task %q not found", shortID)
		}
	}

	watchSet := make(map[string]bool)
	for _, id := range watchIDs {
		watchSet[id] = true
	}

	// Pre-flight: check for already-closed watched tasks.
	for id := range watchSet {
		t, err := GetTaskByShortID(db, id)
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("task %q not found", id)
		}
		if t.Status == "done" || t.Status == "canceled" {
			if err := renderClosedLine(w, id, t.Status, format, true); err != nil {
				return err
			}
			delete(watchSet, id)
		}
	}

	if len(watchSet) == 0 {
		return nil
	}

	// Suppress the streaming preamble in quiet or json mode.
	if format != "json" && !quiet {
		scopeLabel := shortID
		if scopeLabel == "" {
			scopeLabel = "all"
		}
		fmt.Fprintf(w, "Tailing events for %s (Ctrl+C to stop)...\n", scopeLabel)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// If a --timeout was requested, layer a deadline on top of ctx. We keep
	// a handle (timeoutCtx) so we can tell "deadline fired" apart from
	// "caller cancelled us" after the loop exits — reading Err() after
	// RunTail returns avoids a shared-variable race.
	var timeoutCtx context.Context
	if timeout > 0 {
		tctx, tcancel := context.WithTimeout(ctx, timeout)
		defer tcancel()
		ctx = tctx
		timeoutCtx = tctx
	}

	// Read once, outside the poll loop: a replica's name changes about as
	// often as a machine does.
	names, err := LoadReplicaNames(db)
	if err != nil {
		return err
	}

	var loopErr error
	err = RunTail(ctx, db, shortID, pollInterval, func(events []EventEntry) error {
		// Scan for terminal events on watched IDs before filtering for display.
		for _, e := range events {
			if !watchSet[e.ShortID] {
				continue
			}
			if e.EventType == "done" || e.EventType == "canceled" {
				if err := renderClosedLine(w, e.ShortID, e.EventType, format, false); err != nil {
					loopErr = err
					cancel()
					return err
				}
				delete(watchSet, e.ShortID)
			}
		}

		// Render the (filtered) stream unless quiet.
		if !quiet {
			display := FilterEvents(events, filter)
			if len(display) > 0 {
				if format == "json" {
					if err := FormatEventLogJSONLines(w, display); err != nil {
						loopErr = err
						cancel()
						return err
					}
				} else {
					RenderEventLogMarkdown(w, display, names)
				}
			}
		}

		if len(watchSet) == 0 {
			cancel()
		}
		return nil
	})
	if loopErr != nil {
		return loopErr
	}
	if err != nil {
		return err
	}

	if len(watchSet) > 0 {
		// Loop exited because of timeout or parent ctx cancel.
		stillOpen := make([]string, 0, len(watchSet))
		for id := range watchSet {
			stillOpen = append(stillOpen, id)
		}
		// Deterministic ordering for output/tests.
		sort.Strings(stillOpen)
		if err := renderTimeoutSummary(w, stillOpen, format); err != nil {
			return err
		}
		if timeoutCtx != nil && errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%d watched task(s) still open: %w",
				len(stillOpen), ErrTailTimeout)
		}
	}
	return nil
}

// RunTail streams events for a single task-subtree when shortID is non-empty,
// or for every top-level tree when shortID is "". The empty form powers
// `job tail` / `job tail all`. The positional task lookup is skipped when
// the scope is global.
func RunTail(ctx context.Context, db *sql.DB, shortID string, pollInterval time.Duration, callback func([]EventEntry) error) error {
	if shortID != "" {
		task, err := GetTaskByShortID(db, shortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", shortID)
		}
	}

	// The cursor is the log position of the last event handed to the
	// callback, never its row id: a rebuild triggered by a pull renumbers
	// every row, so an id cursor would replay or skip whatever the foreign
	// events displaced.
	var cursor eventlog.Position
	for {
		events, err := GetEventsAfterPosition(db, shortID, cursor)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			if err := callback(events); err != nil {
				return err
			}
			cursor = events[len(events)-1].Position()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval):
		}
	}
}
