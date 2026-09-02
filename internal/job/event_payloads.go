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
	EventRekeyed        EventType = "rekeyed"
	EventSnapshot       EventType = "snapshot"
	EventReplica        EventType = "replica"
)

// StoreFormatVersion is the version of the log's *semantics* — the vocabulary
// above plus what applying each type means. It is not the envelope version
// (eventlog.Version), which describes the shape of a line.
type StoreFormatVersion int

// StoreFormat is the format this binary writes and the newest it can read.
// Every log file declares its own in the `replica` event that opens it, and a
// file declaring more than this is refused rather than applied (store_format.go).
//
// **Bump it whenever a new event type lands above, or the meaning of applying
// an existing one changes.** A binary that does not know a type applies it as
// a no-op — by design, and exactly the silent case this guards: without a
// bump, an old binary renders the record incompletely and then appends events
// computed from that incomplete state. Adding a type therefore means two
// edits in one diff: a new entry in storeFormatAdded, and this constant.
const StoreFormat StoreFormatVersion = 1

// storeFormatAdded is the event vocabulary, by the format that introduced it.
// A format that changed only apply's semantics has an entry with no types.
//
// TestStoreFormatCoversEveryEventType reads the constants above out of this
// file's source and fails unless they are exactly the union here, with the
// highest key equal to StoreFormat — so the diff that adds a type is the diff
// that bumps the format.
var storeFormatAdded = map[StoreFormatVersion][]EventType{
	1: {
		EventCreated,
		EventLabeled,
		EventUnlabeled,
		EventReleased,
		EventCanceled,
		EventBlocked,
		EventUnblocked,
		EventPurged,
		EventClaimExpired,
		EventNoted,
		EventClaimed,
		EventCriteriaAdded,
		EventCriterionState,
		EventHeartbeat,
		EventFoundInSet,
		EventFoundInCleared,
		EventKindChanged,
		EventDone,
		EventReopened,
		EventEdited,
		EventMoved,
		EventReparented,
		EventRekeyed,
		EventSnapshot,
		EventReplica,
	},
}

// ReleaseReason names why a claim ended when the holder did not ask. It is a
// closed set, so it is a type rather than a free string in the payload.
type ReleaseReason string

// ReleaseLostMerge is the reconcile pass releasing the later of two claims
// made on one task by two replicas that were apart.
const ReleaseLostMerge ReleaseReason = "lost-merge"

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
// Reason and LostClaim are set only by the reconcile pass: LostClaim is the
// position "<ts>-<rep>-<seq>" of the `claimed` event this release undoes, and
// it is what stops the next rebuild repairing the same conflict again.
type ReleasedPayload struct {
	AutoReleased     bool          `json:"auto_released,omitempty"`
	TriggeredByChild string        `json:"triggered_by_child,omitempty"`
	WasClaimedBy     string        `json:"was_claimed_by"`
	WasExpiresAt     int64         `json:"was_expires_at"`
	Reason           ReleaseReason `json:"reason,omitempty"`
	LostClaim        string        `json:"lost_claim,omitempty"`
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

// UnblockReason names why a block edge went away. It is a closed set — the
// operator asked, or the blocker closed one of two ways — so it is a type
// rather than a free string in the payload.
type UnblockReason string

const (
	// UnblockManual is `job unblock`.
	UnblockManual UnblockReason = "manual"
	// UnblockBlockerDone is the edge dropped because the blocker closed.
	UnblockBlockerDone UnblockReason = "blocker_done"
	// UnblockBlockerCanceled is the edge dropped because the blocker was
	// canceled.
	UnblockBlockerCanceled UnblockReason = "blocker_canceled"
)

// UnblockedPayload is recorded when a block edge is removed — manually
// (RunUnblockMany), or automatically because the blocker was done or
// canceled.
type UnblockedPayload struct {
	BlockedID string        `json:"blocked_id"`
	BlockerID string        `json:"blocker_id"`
	Reason    UnblockReason `json:"reason"`
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
//
// ShortID and SortKey are what make the event replayable: apply inserts the
// row with the id and the fractional key the handler minted, rather than
// minting either itself, so criteria_added is idempotent by (task, short id)
// and a rebuild lands the same order whatever sequence the events arrive in.
// ShortID also lets the JS replay-fold establish the criterion's stable
// identity at add time.
type CriterionEntry struct {
	Label   string `json:"label"`
	State   string `json:"state"`
	ShortID string `json:"short_id,omitempty"`
	SortKey string `json:"sort_key,omitempty"`
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

// ReplicaPayload names the checkout that owns a log file. It is the first
// line a replica ever appends, and `job replica rename` appends another; the
// latest one per replica is the name every reader shows.
//
// It applies no state — there is no replicas table, and nothing here can
// disagree with anything else — so it has no entry in applyTable. Host, Path
// and User are recorded separately from Label so a rename never loses the
// facts the default label was built from.
// Format is the store format the file was written at — see StoreFormat. It is
// absent from every file written before the field existed, and absent reads as
// format 1.
type ReplicaPayload struct {
	Label  string             `json:"label"`
	Host   string             `json:"host,omitempty"`
	Path   string             `json:"path,omitempty"`
	User   string             `json:"user,omitempty"`
	Format StoreFormatVersion `json:"format,omitempty"`
}

// SnapshotPayload is the whole state of a cache in one event: every task, every
// relation and every criterion, addressed by short id.
//
// Applying it is an overwrite, so it is the only event that does not describe a
// change. Adoption writes one to carry a legacy database's state across, since
// the legacy rows themselves were never replayable; `job compact` would write
// one to summarize the files it archives (backlog).
type SnapshotPayload struct {
	Tasks    []SnapshotTask      `json:"tasks"`
	Blocks   []SnapshotBlock     `json:"blocks,omitempty"`
	Labels   []SnapshotLabel     `json:"labels,omitempty"`
	Criteria []SnapshotCriterion `json:"criteria,omitempty"`
	FoundIn  []SnapshotFoundIn   `json:"found_in,omitempty"`
	Users    []SnapshotUser      `json:"users,omitempty"`
}

// SnapshotTask is one row of `tasks`. The nullable columns are pointers so a
// snapshot round-trips NULL and the empty string distinctly.
type SnapshotTask struct {
	ShortID        string  `json:"short_id"`
	ParentID       string  `json:"parent_id,omitempty"`
	Title          string  `json:"title"`
	Description    string  `json:"description,omitempty"`
	Status         string  `json:"status"`
	SortKey        string  `json:"sort_key"`
	ClaimedBy      *string `json:"claimed_by,omitempty"`
	ClaimExpiresAt *int64  `json:"claim_expires_at,omitempty"`
	CompletionNote *string `json:"completion_note,omitempty"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
	DeletedAt      *int64  `json:"deleted_at,omitempty"`
	Kind           string  `json:"kind"`
}

// SnapshotBlock is one edge of `blocks`.
type SnapshotBlock struct {
	BlockerID string `json:"blocker_id"`
	BlockedID string `json:"blocked_id"`
	CreatedAt int64  `json:"created_at"`
}

// SnapshotLabel is one row of `task_labels`.
type SnapshotLabel struct {
	TaskID    string `json:"task_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// SnapshotCriterion is one row of `task_criteria`.
type SnapshotCriterion struct {
	TaskID    string  `json:"task_id"`
	ShortID   *string `json:"short_id,omitempty"`
	Label     string  `json:"label"`
	State     string  `json:"state"`
	SortKey   string  `json:"sort_key"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

// SnapshotFoundIn is one row of `found_in`.
type SnapshotFoundIn struct {
	TaskID    string `json:"task_id"`
	SourceID  string `json:"source_id"`
	CreatedAt int64  `json:"created_at"`
}

// SnapshotUser is one row of `users`.
type SnapshotUser struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}
