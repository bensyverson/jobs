package job

// EventType names the vocabulary of events recorded to the audit log.
// Every recordEvent / recordOrphanEvent call site passes one of these
// constants, never a string literal, so the vocabulary lives in one place.
// The payload structs below are the one true documentation of a given
// event's detail shape — the JSON key names and value shapes here are
// exactly what the dashboard's JS and the Go readers already expect; any
// reference to a task or criterion inside a payload is a short id, never a
// row id, because row ids are minted by the local cache and differ per
// machine (see project/2026-09-01-git-native-event-log.md, "The event").
type EventType string

const (
	EventCreated        EventType = "created"
	EventLabeled        EventType = "labeled"
	EventUnlabeled      EventType = "unlabeled"
	EventReleased       EventType = "released"
	EventCanceled       EventType = "canceled"
	EventBlocked        EventType = "blocked"
	EventUnblocked      EventType = "unblocked"
	EventPurged         EventType = "purged"
	EventClaimExpired   EventType = "claim_expired"
	EventNoted          EventType = "noted"
	EventClaimed        EventType = "claimed"
	EventCriteriaAdded  EventType = "criteria_added"
	EventCriterionState EventType = "criterion_state"
	EventHeartbeat      EventType = "heartbeat"
	EventFoundInSet     EventType = "found_in_set"
	EventFoundInCleared EventType = "found_in_cleared"
	EventKindChanged    EventType = "kind_changed"
	EventDone           EventType = "done"
	EventReopened       EventType = "reopened"
	EventEdited         EventType = "edited"
	EventMoved          EventType = "moved"
	EventReparented     EventType = "reparented"
)

// CreatedPayload is recorded by RunAdd and the import inserter. ParentID is
// "" for a root task.
//
// ShortID is the new task's own id. Apply never mints one — ids are minted by
// the handler and travel in the event, because a replay on another machine
// has to land on the same id the note and the commit message cite.
type CreatedPayload struct {
	ShortID     string `json:"short_id"`
	ParentID    string `json:"parent_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SortKey     string `json:"sort_key"`
	Kind        string `json:"kind,omitempty"`
}

// LabeledPayload is recorded whenever labels are attached — task creation
// (inline labels), import, and RunLabelAdd.
type LabeledPayload struct {
	Names    []string `json:"names"`
	Existing []string `json:"existing"`
}

// UnlabeledPayload is recorded by RunLabelRemove.
type UnlabeledPayload struct {
	Names  []string `json:"names"`
	Absent []string `json:"absent"`
}

// ReleasedPayload covers both an explicit RunRelease and the leaf-frontier
// auto-release of a claimed parent when a child is added or reparented
// under it. AutoReleased and TriggeredByChild are set only for the
// auto-release case.
type ReleasedPayload struct {
	AutoReleased     bool   `json:"auto_released,omitempty"`
	TriggeredByChild string `json:"triggered_by_child,omitempty"`
	WasClaimedBy     string `json:"was_claimed_by"`
	WasExpiresAt     int64  `json:"was_expires_at"`
}

// CanceledPayload covers a cascaded descendant cancel, an explicit cancel
// target, and the leaf-frontier auto-close of an ancestor whose last open
// child just canceled. Reason is empty only for the auto-close case, which
// carries AutoClosed/TriggerKind/TriggeredBy/CascadeStatus instead. Cascade
// is a pointer because the explicit-target shape always emits it (true or
// false) while the other two shapes never do.
type CanceledPayload struct {
	Reason                string   `json:"reason,omitempty"`
	Cascade               *bool    `json:"cascade,omitempty"`
	CascadeClosed         []string `json:"cascade_closed,omitempty"`
	CascadeClosedByParent string   `json:"cascade_closed_by_parent,omitempty"`
	WasStatus             string   `json:"was_status"`
	WasClaimedBy          string   `json:"was_claimed_by,omitempty"`
	WasExpiresAt          int64    `json:"was_expires_at,omitempty"`
	AutoClosed            bool     `json:"auto_closed,omitempty"`
	TriggerKind           string   `json:"trigger_kind,omitempty"`
	TriggeredBy           string   `json:"triggered_by,omitempty"`
	CascadeStatus         string   `json:"cascade_status,omitempty"`
}

// DonePayload covers a cascaded descendant close, an explicit close target,
// and the leaf-frontier auto-close of an ancestor whose last open child just
// closed. See CanceledPayload for why Cascade is a pointer.
type DonePayload struct {
	Note                  string   `json:"note,omitempty"`
	Result                any      `json:"result,omitempty"`
	Cascade               *bool    `json:"cascade,omitempty"`
	CascadeClosed         []string `json:"cascade_closed,omitempty"`
	CascadeClosedByParent string   `json:"cascade_closed_by_parent,omitempty"`
	CriteriaWaived        []string `json:"criteria_waived,omitempty"`
	CriteriaBulkState     string   `json:"criteria_bulk_state,omitempty"`
	WasStatus             string   `json:"was_status"`
	WasClaimedBy          string   `json:"was_claimed_by,omitempty"`
	WasExpiresAt          int64    `json:"was_expires_at,omitempty"`
	AutoClosed            bool     `json:"auto_closed,omitempty"`
	TriggerKind           string   `json:"trigger_kind,omitempty"`
	TriggeredBy           string   `json:"triggered_by,omitempty"`
	CascadeStatus         string   `json:"cascade_status,omitempty"`
}

// BlockedPayload is recorded by RunBlockMany and the import inserter.
type BlockedPayload struct {
	BlockedID string `json:"blocked_id"`
	BlockerID string `json:"blocker_id"`
}

// UnblockedPayload is recorded when a block edge is removed — manually
// (RunUnblockMany), or automatically because the blocker was done or
// canceled.
type UnblockedPayload struct {
	BlockedID string `json:"blocked_id"`
	BlockerID string `json:"blocker_id"`
	Reason    string `json:"reason"`
}

// PurgedPayload is recorded on the parent (or as an orphan event, for a
// purged root) before the purged subtree's rows are erased.
type PurgedPayload struct {
	Reason        string   `json:"reason"`
	PurgedID      string   `json:"purged_id"`
	PurgedTitle   string   `json:"purged_title"`
	Cascade       bool     `json:"cascade"`
	CascadePurged []string `json:"cascade_purged"`
}

// ClaimExpiredPayload is recorded when a stale claim is swept.
type ClaimExpiredPayload struct {
	WasClaimedBy string `json:"was_claimed_by"`
	WasExpiresAt int64  `json:"was_expires_at"`
}

// NotedPayload is recorded by RunNote and by the note-before-claimed /
// note-before-released atomic pairs in claims.go.
type NotedPayload struct {
	Text   string `json:"text"`
	Result any    `json:"result,omitempty"`
}

// ClaimedPayload is recorded by RunClaim. WasClaimedBy/WasExpiresAt are set
// only when --force overrode a live claim.
type ClaimedPayload struct {
	Duration     string `json:"duration"`
	ExpiresAt    int64  `json:"expires_at"`
	WasClaimedBy string `json:"was_claimed_by,omitempty"`
	WasExpiresAt int64  `json:"was_expires_at,omitempty"`
}

// CriterionEntry describes one criterion within a criteria_added payload.
// ShortID rides along so the JS replay-fold can establish the criterion's
// stable identity at add time.
type CriterionEntry struct {
	Label   string `json:"label"`
	State   string `json:"state"`
	ShortID string `json:"short_id,omitempty"`
}

// CriteriaAddedPayload is recorded by RunAddCriteria and the import
// inserter.
type CriteriaAddedPayload struct {
	Criteria []CriterionEntry `json:"criteria"`
}

// CriterionStatePayload is recorded by RunSetCriterion.
type CriterionStatePayload struct {
	Label   string `json:"label"`
	State   string `json:"state"`
	Prior   string `json:"prior"`
	ShortID string `json:"short_id,omitempty"`
}

// Focus is machine-local (see local.go), so there is no focus event type and
// no focus payload: focus_set and focus_released rows in an existing database
// are history, rendered by the generic fallback row.

// HeartbeatPayload is recorded by RunHeartbeat.
type HeartbeatPayload struct {
	NewExpiresAt int64 `json:"new_expires_at"`
}

// FoundInSetPayload is recorded by setFoundInTx. PreviousSourceID is set
// only when the set displaced a different source.
type FoundInSetPayload struct {
	TaskID           string `json:"task_id"`
	SourceID         string `json:"source_id"`
	PreviousSourceID string `json:"previous_source_id,omitempty"`
}

// FoundInClearedPayload is recorded by RunClearFoundIn.
type FoundInClearedPayload struct {
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
}

// KindChangedPayload is recorded by RunSetKind.
type KindChangedPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ReopenedPayload is recorded by RunReopen, once per cascaded descendant
// and once for the explicit target.
type ReopenedPayload struct {
	Cascade          bool     `json:"cascade"`
	ReopenedChildren []string `json:"reopened_children"`
	FromStatus       string   `json:"from_status"`
}

// EditedPayload is recorded by RunEdit. Each old/new pair is present
// together or not at all, depending on which of --title/--desc were given.
// The fields are pointers so an empty value is still present: the replay
// fold rewinds an edit only when both halves of the pair are there, and a
// description edited from or to "" is a real edit.
type EditedPayload struct {
	OldTitle *string `json:"old_title,omitempty"`
	NewTitle *string `json:"new_title,omitempty"`
	OldDesc  *string `json:"old_desc,omitempty"`
	NewDesc  *string `json:"new_desc,omitempty"`
}

// MovedPayload is recorded by RunMove. SortKey is the task's new fractional
// sort key — applying the event is a plain column write — and OldSortKey is
// what it replaced, so the scrubber can rewind the move.
type MovedPayload struct {
	Direction  string `json:"direction"`
	RelativeTo string `json:"relative_to"`
	SortKey    string `json:"sort_key"`
	OldSortKey string `json:"old_sort_key"`
}

// ReparentedPayload is recorded by RunReparent. Direction/RelativeTo are
// set only when the reparent named a sibling to move before/after. See
// MovedPayload for the two keys.
type ReparentedPayload struct {
	PriorParentID string `json:"prior_parent_id"`
	NewParentID   string `json:"new_parent_id"`
	SortKey       string `json:"sort_key"`
	OldSortKey    string `json:"old_sort_key"`
	Direction     string `json:"direction,omitempty"`
	RelativeTo    string `json:"relative_to,omitempty"`
}
