package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// Naming a replica.
//
// A replica id is six base62 characters — enough to keep two checkouts'
// files apart, and useless to a reader trying to remember which machine
// arMAXc is. So every log file opens with a `replica` event carrying a
// human-readable label plus the facts it was built from: hostname, checkout
// path and OS user.
//
// It is an event rather than a tracked sidecar file for the reason everything
// else here is: a file two machines both edit is a merge conflict, and a log
// line is not. The latest `replica` event per replica wins, so `job replica
// rename` is history rather than a rewrite, and no migration is needed —
// the label is derived at read time from the events table.

// ReplicaPayload's fields are in event_payloads.go with the rest of the
// vocabulary. Everything below builds one, reads them back, or renders them.

// newReplicaPayload describes this checkout. label is the operator's chosen
// name; empty means the default, which is the hostname and the checkout path.
//
// Nothing here fails: a machine with no resolvable hostname or user still gets
// a log file, and an empty field is simply not rendered.
func newReplicaPayload(cachePath, label string) ReplicaPayload {
	p := ReplicaPayload{Label: label}
	p.Host, _ = os.Hostname()
	// .jobs.db names the cache; the checkout is the directory holding it.
	if abs, err := filepath.Abs(filepath.Dir(cachePath)); err == nil {
		p.Path = abs
	} else {
		p.Path = filepath.Dir(cachePath)
	}
	if u, err := user.Current(); err == nil {
		p.User = u.Username
	} else {
		p.User = os.Getenv("USER")
	}
	if p.Label == "" {
		p.Label = defaultReplicaLabel(p.Host, p.Path)
	}
	return p
}

// defaultReplicaLabel is "<host>:<path>", with the operator's home directory
// abbreviated to ~ so the label stays readable in a status line. The full
// path is recorded separately, so nothing is lost.
func defaultReplicaLabel(host, path string) string {
	short := abbreviateHome(path)
	if host == "" {
		return short
	}
	return host + ":" + short
}

// abbreviateHome rewrites a path under $HOME as ~/…, and leaves everything
// else alone.
func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || path == home {
		if path == home {
			return "~"
		}
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// replicaEventMissing reports whether this cache holds no `replica` event for
// rep. It is the cheap check every write makes: one indexed EXISTS, answered
// false forever after the first write of a replica's life.
func replicaEventMissing(tx dbtx, rep string) (bool, error) {
	var found int
	err := tx.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM events WHERE rep = ? AND event_type = ?)",
		rep, string(EventReplica),
	).Scan(&found)
	if err != nil {
		return false, err
	}
	return found == 0, nil
}

// ReplicaNames resolves a replica id to the name a reader should see, and
// knows which replica is this checkout.
type ReplicaNames struct {
	// Local is this checkout's replica id, "" before it has ever written.
	Local string
	// Labels is the latest label per replica id.
	Labels map[string]string
}

// Foreign returns the name to show beside an event written elsewhere, and ""
// for an event of this checkout's own — a single-machine user therefore never
// sees a replica named anywhere. A foreign replica that has never announced
// itself falls back to its id, which is still more than nothing.
func (n ReplicaNames) Foreign(rep string) string {
	if rep == "" || rep == n.Local {
		return ""
	}
	if label := n.Labels[rep]; label != "" {
		return label
	}
	return rep
}

// LoadReplicaNames reads every replica's latest label out of the cache, and
// this checkout's own id out of local.json.
//
// The labels are derived rather than stored in a table of their own: there are
// as many `replica` rows as there are renames, which is a handful, and a
// derived read cannot fall out of step with the log.
func LoadReplicaNames(db *sql.DB) (ReplicaNames, error) {
	names := ReplicaNames{Labels: map[string]string{}}
	if path, err := CachePathOf(db); err == nil {
		if local, err := LoadLocalState(path); err == nil {
			names.Local = local.Rep
		}
	}
	labels, err := replicaLabels(db)
	if err != nil {
		return names, err
	}
	names.Labels = labels
	return names, nil
}

// replicaPayloads reads the latest `replica` payload per replica, ordered so
// the last one applied wins.
func replicaPayloads(db *sql.DB) (map[string]ReplicaPayload, error) {
	rows, err := db.Query(
		"SELECT rep, COALESCE(detail, '') FROM events WHERE event_type = ? AND rep != '' ORDER BY ts, seq",
		string(EventReplica),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ReplicaPayload{}
	for rows.Next() {
		var rep, detail string
		if err := rows.Scan(&rep, &detail); err != nil {
			return nil, err
		}
		if detail == "" {
			continue
		}
		var p ReplicaPayload
		if err := json.Unmarshal([]byte(detail), &p); err != nil {
			// A line this reader cannot parse is not worth failing a status
			// over; the replica simply shows under its id.
			continue
		}
		out[rep] = p
	}
	return out, rows.Err()
}

func replicaLabels(db *sql.DB) (map[string]string, error) {
	payloads, err := replicaPayloads(db)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(payloads))
	for rep, p := range payloads {
		if p.Label != "" {
			out[rep] = p.Label
		}
	}
	return out, nil
}

// ReplicaInfo is one row of `job replicas`.
type ReplicaInfo struct {
	Rep       string `json:"rep"`
	Label     string `json:"label,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	Path      string `json:"path,omitempty"`
	Events    int    `json:"events"`
	LastEvent int64  `json:"last_event"`
	// IsLocal marks the replica this checkout writes to.
	IsLocal bool `json:"is_local"`
}

// RunReplicas lists every replica whose events this store holds, busiest last
// activity first, with this checkout's own replica always at the top.
func RunReplicas(db *sql.DB) ([]ReplicaInfo, error) {
	names, err := LoadReplicaNames(db)
	if err != nil {
		return nil, err
	}
	payloads, err := replicaPayloads(db)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(`
		SELECT rep, COUNT(*), COALESCE(MAX(created_at), 0)
		FROM events WHERE rep != '' GROUP BY rep`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReplicaInfo
	seen := map[string]bool{}
	for rows.Next() {
		var info ReplicaInfo
		if err := rows.Scan(&info.Rep, &info.Events, &info.LastEvent); err != nil {
			return nil, err
		}
		p := payloads[info.Rep]
		info.Label, info.Host, info.User, info.Path = p.Label, p.Host, p.User, p.Path
		info.IsLocal = info.Rep == names.Local
		seen[info.Rep] = true
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A checkout that has minted a replica id but not yet written is still a
	// replica, and saying so is friendlier than an empty list.
	if names.Local != "" && !seen[names.Local] {
		out = append(out, ReplicaInfo{Rep: names.Local, IsLocal: true})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsLocal != out[j].IsLocal {
			return out[i].IsLocal
		}
		if out[i].LastEvent != out[j].LastEvent {
			return out[i].LastEvent > out[j].LastEvent
		}
		return out[i].Rep < out[j].Rep
	})
	return out, nil
}

// ReplicaRenameResult is what `job replica rename` reports.
type ReplicaRenameResult struct {
	Rep   string `json:"rep"`
	Label string `json:"label"`
	Prior string `json:"prior,omitempty"`
}

// RunReplicaRename appends a fresh `replica` event for this checkout.
//
// It is an append, not an edit: the earlier event stays in the log, and every
// reader takes the latest one. Renaming on one machine therefore propagates
// like any other event, and the history says what the replica used to be
// called.
func RunReplicaRename(db *sql.DB, label, actor string) (*ReplicaRenameResult, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, fmt.Errorf("a replica name cannot be empty")
	}
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	prior, err := replicaLabels(db)
	if err != nil {
		return nil, err
	}

	res := &ReplicaRenameResult{Label: label}
	err = commit(db, func(tx dbtx, b *eventBatch) error {
		res.Rep = b.rec.rep
		res.Prior = prior[b.rec.rep]
		payload := newReplicaPayload(path, label)
		return b.emit(tx, EventReplica, "", actor, payload)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// SetReplicaName records the name to use when this checkout's replica id is
// minted, and renames the replica immediately if it already has one.
//
// `job init --replica-name` is the caller: at init there is usually no replica
// yet, so the name waits in local.json for the first write to mint it. Init on
// a checkout that has already written is the other case, and there the honest
// answer is a rename.
func SetReplicaName(db *sql.DB, label, actor string) (*ReplicaRenameResult, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, fmt.Errorf("a replica name cannot be empty")
	}
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	local, err := LoadLocalState(path)
	if err != nil {
		return nil, err
	}
	if local.Rep != "" {
		return RunReplicaRename(db, label, actor)
	}
	if err := UpdateLocalState(path, func(s *LocalState) error {
		s.ReplicaName = label
		return nil
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

// RenderReplicas writes the markdown listing: one replica per stanza, this
// checkout's first and marked.
func RenderReplicas(w io.Writer, replicas []ReplicaInfo) {
	if len(replicas) == 0 {
		fmt.Fprintln(w, "No replicas yet — nothing has been written to this store.")
		return
	}
	noun := "replicas"
	if len(replicas) == 1 {
		noun = "replica"
	}
	fmt.Fprintf(w, "%d %s\n", len(replicas), noun)
	now := nowUnix()
	for _, r := range replicas {
		label := r.Label
		if label == "" {
			label = "(unnamed)"
		}
		mark := ""
		if r.IsLocal {
			mark = "  ← this checkout"
		}
		fmt.Fprintf(w, "\n%s %q%s\n", r.Rep, label, mark)
		facts := []string{}
		if r.Host != "" {
			facts = append(facts, r.Host)
		}
		if r.User != "" {
			facts = append(facts, r.User)
		}
		if r.Path != "" {
			facts = append(facts, r.Path)
		}
		if len(facts) > 0 {
			fmt.Fprintf(w, "  %s\n", strings.Join(facts, " · "))
		}
		events := "events"
		if r.Events == 1 {
			events = "event"
		}
		if r.LastEvent > 0 {
			fmt.Fprintf(w, "  %d %s · last %s ago\n", r.Events, events, FormatDuration(max(now-r.LastEvent, 0)))
		} else {
			fmt.Fprintf(w, "  %d %s\n", r.Events, events)
		}
	}
}

// RenderReplicasJSON writes the listing as a JSON array.
func RenderReplicasJSON(w io.Writer, replicas []ReplicaInfo) error {
	if replicas == nil {
		replicas = []ReplicaInfo{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(replicas)
}
