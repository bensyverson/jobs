package job

import "database/sql"

type Task struct {
	ID             int64
	ShortID        string
	ParentID       *int64
	Title          string
	Description    string
	Status         string
	SortKey        string
	ClaimedBy      *string
	ClaimExpiresAt *int64
	CompletionNote *string
	CreatedAt      int64
	UpdatedAt      int64
	DeletedAt      *int64
	// Kind is meaningful on roots only: children of an issue root are
	// ordinary tasks and always carry KindTask. See internal/job/kind.go.
	Kind TreeKind
}

type Event struct {
	ID        int64
	TaskID    int64
	EventType string
	Detail    string
	CreatedAt int64
}

type EventEntry struct {
	ID        int64
	TaskID    int64
	ShortID   string
	EventType string
	Actor     string
	Detail    string
	CreatedAt int64
}

type TaskNode struct {
	Task     *Task
	Children []*TaskNode
}

type User struct {
	ID        int64
	Name      string
	CreatedAt int64
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*Task, error) {
	var t Task
	var parentID sql.NullInt64
	var claimedBy sql.NullString
	var claimExpiresAt sql.NullInt64
	var completionNote sql.NullString
	var deletedAt sql.NullInt64

	err := s.Scan(
		&t.ID, &t.ShortID, &parentID, &t.Title, &t.Description,
		&t.Status, &t.SortKey, &claimedBy, &claimExpiresAt,
		&completionNote, &t.CreatedAt, &t.UpdatedAt, &deletedAt, &t.Kind,
	)
	if err != nil {
		return nil, err
	}

	if parentID.Valid {
		pid := parentID.Int64
		t.ParentID = &pid
	}
	if claimedBy.Valid {
		cb := claimedBy.String
		t.ClaimedBy = &cb
	}
	if claimExpiresAt.Valid {
		ce := claimExpiresAt.Int64
		t.ClaimExpiresAt = &ce
	}
	if completionNote.Valid {
		cn := completionNote.String
		t.CompletionNote = &cn
	}
	if deletedAt.Valid {
		da := deletedAt.Int64
		t.DeletedAt = &da
	}

	return &t, nil
}
