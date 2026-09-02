package job

import (
	"github.com/bensyverson/jobs/internal/eventlog"
)

// The claims family's state writes: claimed, released, claim_expired and
// heartbeat.
//
// A claim is shared state — who holds a task, and until when — so every
// change to it is an event, and these four functions are the only things that
// touch claimed_by and claim_expires_at.
//
// The expiry is an ABSOLUTE unix second carried in the payload, never a
// duration applied at apply time. A duration would make the deadline depend
// on when the event was replayed, and two machines replaying the same log
// would disagree about who still holds the task
// (project/2026-09-01-git-native-event-log.md, the `claimed` merge rule:
// "Carries `until`"). The human-facing duration rides along for rendering
// only.

// applyClaimed puts the claim on the task. The holder is the event's actor —
// a claim is by definition made by whoever emitted it — and the deadline is
// the payload's absolute expires_at.
//
// --force takes over a live claim by simply winning: was_claimed_by and
// was_expires_at stay in the payload as breadcrumbs for a reverse-fold, and
// nothing here reads them.
func applyClaimed(tx dbtx, e eventlog.Envelope) error {
	var p ClaimedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	_, err := tx.Exec(`
		UPDATE tasks SET status = 'claimed', claimed_by = ?, claim_expires_at = ?, updated_at = ?
		WHERE short_id = ?`, e.Actor, p.ExpiresAt, eventSeconds(e), e.Task)
	return err
}

// applyClaimExpired drops a claim whose deadline passed. It is the same write
// as applyReleased — the difference is who decided, which the event type
// already records — but it stays its own function because the two are
// different facts and will diverge the moment either grows a column.
//
// Like applyReleased it is unconditional rather than guarded on the current
// status: a guard would make the write a no-op on a replay where the matching
// `claimed` has not been applied, and the row's updated_at would then differ
// between the original and the rebuild.
func applyClaimExpired(tx dbtx, e eventlog.Envelope) error {
	_, err := tx.Exec(`
		UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ?
		WHERE short_id = ?`, eventSeconds(e), e.Task)
	return err
}

// applyHeartbeat pushes the deadline out. It touches neither the holder nor
// the status: a heartbeat is only ever emitted for a claim that is live and
// held by the emitter, and a replay that has not seen the matching `claimed`
// must not invent one.
func applyHeartbeat(tx dbtx, e eventlog.Envelope) error {
	var p HeartbeatPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	_, err := tx.Exec(
		"UPDATE tasks SET claim_expires_at = ?, updated_at = ? WHERE short_id = ?",
		p.NewExpiresAt, eventSeconds(e), e.Task,
	)
	return err
}

// applyReleased drops a claim the holder gave back. It also covers the
// leaf-frontier auto-release the add and reparent handlers emit when a
// claimed parent gains an open child.
//
// The post-release status is always 'available'. This cache has no 'blocked'
// status — a blocker is a row in `blocks`, and the readers derive
// blockedness from it — so there is nothing for the handler to compute and
// nothing for the payload to carry.
//
// Unconditional rather than guarded on the current status, for the same
// reason as applyClaimExpired.
func applyReleased(tx dbtx, e eventlog.Envelope) error {
	_, err := tx.Exec(`
		UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ?
		WHERE short_id = ?`, eventSeconds(e), e.Task)
	return err
}
