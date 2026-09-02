package job

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

type StatusSummary struct {
	Open            int
	Claimed         int
	Done            int
	Canceled        int
	ClaimedByYou    int
	HasActor        bool
	LastActivity    int64
	Total           int
	IdentityDefault string
	Strict          bool
	Store           *StoreStatus
}

// StoreStatus is the store line: which replica this checkout is, how much log
// there is, and what the last open did about it. The log is the record and the
// cache is disposable, so "is the cache current?" is a question worth being
// able to ask.
type StoreStatus struct {
	Rep string
	// Label is this replica's human-readable name — the latest `replica`
	// event's label. Empty until this checkout has written once.
	Label  string
	Files  int
	Events int
	State  StoreState
}

// storeStatus reads the store beside db's cache. A database with no file on
// disk — an in-memory test database — has no store, and reports none rather
// than failing the whole status.
func storeStatus(db *sql.DB) *StoreStatus {
	path, err := CachePathOf(db)
	if err != nil {
		return nil
	}
	s := &StoreStatus{State: StoreInSync}
	if sync := StoreSyncOf(db); sync != nil {
		s.State = sync.State
	}
	// The file count and the replica id are read now rather than taken from
	// the open: this process may have minted the replica and written its first
	// log file since.
	if local, err := LoadLocalState(path); err == nil {
		s.Rep = local.Rep
	}
	if s.Rep != "" {
		if labels, err := replicaLabels(db); err == nil {
			s.Label = labels[s.Rep]
		}
	}
	if files, err := eventlog.Files(eventlog.StoreDir(path)); err == nil {
		s.Files = len(files)
	}
	if n, err := StoreEventCount(path); err == nil {
		s.Events = n
	}
	return s
}

func RunStatus(db *sql.DB, actor string) (*StatusSummary, error) {
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}

	s := &StatusSummary{HasActor: actor != ""}

	rows, err := db.Query("SELECT status, COUNT(*) FROM tasks WHERE deleted_at IS NULL GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		s.Total += count
		switch status {
		case "done":
			s.Done = count
		case "claimed":
			s.Claimed = count
		case "canceled":
			s.Canceled = count
		default:
			s.Open += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if s.HasActor {
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM tasks WHERE claimed_by = ? AND status = 'claimed' AND deleted_at IS NULL",
			actor,
		).Scan(&s.ClaimedByYou); err != nil {
			return nil, err
		}
	}

	var lastActivity sql.NullInt64
	if err := db.QueryRow("SELECT MAX(created_at) FROM events").Scan(&lastActivity); err != nil {
		return nil, err
	}
	if lastActivity.Valid {
		s.LastActivity = lastActivity.Int64
	}

	defaultID, err := GetDefaultIdentity(db)
	if err != nil {
		return nil, err
	}
	s.IdentityDefault = defaultID

	strict, err := IsStrict(db)
	if err != nil {
		return nil, err
	}
	s.Strict = strict
	s.Store = storeStatus(db)

	return s, nil
}

// StaleClaim describes a task whose claim is past its TTL but has not
// yet been auto-expired. Callers should snapshot with FindStaleClaims
// BEFORE any other code path that may expire stale claims as a side
// effect (RunStatus, RunClaim, RunDone, …) — once the auto-expiry
// fires, the task row is reverted to `available` and the snapshot is
// gone.
type StaleClaim struct {
	ShortID      string
	Title        string
	ClaimedBy    string
	ExpiredAt    int64
	SecondsStale int64
}

// FindStaleClaims returns every currently-claimed task whose expiry is
// in the past. When scopeID is non-nil, results are limited to the
// subtree rooted at scopeID. Results are ordered by expired-longest-
// ago first, so the most urgent recovery signals surface at the top.
func FindStaleClaims(db *sql.DB, scopeID *int64) ([]StaleClaim, error) {
	now := CurrentNowFunc().Unix()

	var rows *sql.Rows
	var err error
	if scopeID == nil {
		rows, err = db.Query(`
			SELECT short_id, title, claimed_by, claim_expires_at
			FROM tasks
			WHERE status = 'claimed'
			  AND claim_expires_at < ?
			  AND deleted_at IS NULL
			ORDER BY claim_expires_at ASC
		`, now)
	} else {
		rows, err = db.Query(`
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT t.short_id, t.title, t.claimed_by, t.claim_expires_at
			FROM tasks t
			JOIN subtree s ON s.id = t.id
			WHERE t.status = 'claimed'
			  AND t.claim_expires_at < ?
			  AND t.deleted_at IS NULL
			ORDER BY t.claim_expires_at ASC
		`, *scopeID, now)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StaleClaim
	for rows.Next() {
		var c StaleClaim
		var claimedBy sql.NullString
		var exp sql.NullInt64
		if err := rows.Scan(&c.ShortID, &c.Title, &claimedBy, &exp); err != nil {
			return nil, err
		}
		if claimedBy.Valid {
			c.ClaimedBy = claimedBy.String
		}
		if exp.Valid {
			c.ExpiredAt = exp.Int64
			c.SecondsStale = now - c.ExpiredAt
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RenderStaleClaims writes one line per stale claim. Emits nothing when
// the list is empty; callers control the preceding blank-line spacer.
func RenderStaleClaims(w io.Writer, claims []StaleClaim) {
	for _, c := range claims {
		fmt.Fprintf(w, "Stale: %s %q — claimed by %s, expired %s ago\n",
			c.ShortID, c.Title, c.ClaimedBy, FormatDuration(c.SecondsStale))
	}
}

func RenderStatus(w io.Writer, s *StatusSummary) {
	var parts []string
	// Claimed term is scoped to the caller when HasActor, else the global
	// live-claim count. Suppressed entirely when zero to avoid noise for
	// non-claiming callers.
	claimed := s.Claimed
	if s.HasActor {
		claimed = s.ClaimedByYou
	}
	if claimed > 0 {
		parts = append(parts, fmt.Sprintf("%d claimed", claimed))
	}
	parts = append(parts, fmt.Sprintf("%d open", s.Open))
	parts = append(parts, fmt.Sprintf("%d done", s.Done))
	if s.Canceled > 0 {
		parts = append(parts, fmt.Sprintf("%d canceled", s.Canceled))
	}

	line := strings.Join(parts, ", ")
	if s.LastActivity > 0 {
		ago := max(nowUnix()-s.LastActivity, 0)
		line += fmt.Sprintf(" (last activity: %s ago)", FormatDuration(ago))
	}
	fmt.Fprintln(w, line)

	if s.IdentityDefault != "" {
		strictWord := "off"
		if s.Strict {
			strictWord = "on"
		}
		fmt.Fprintf(w, "Identity: %s (default) · strict mode %s\n", s.IdentityDefault, strictWord)
	} else {
		fmt.Fprintln(w, "Identity: none set · --as required on writes")
	}

	if st := s.Store; st != nil {
		rep := st.Rep
		if rep == "" {
			rep = "unminted"
		}
		files, events := "log files", "events"
		if st.Files == 1 {
			files = "log file"
		}
		if st.Events == 1 {
			events = "event"
		}
		name := ""
		if st.Label != "" {
			name = fmt.Sprintf(" %q", st.Label)
		}
		fmt.Fprintf(w, "Store: replica %s%s · %d %s, %d %s · cache %s\n",
			rep, name, st.Files, files, st.Events, events, st.State)
	}
}
