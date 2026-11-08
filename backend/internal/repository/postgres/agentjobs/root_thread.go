package agentjobs

import (
	"context"
	"errors"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound   = errors.New("agent jobs repository: not found")
	ErrValidation = errors.New("agent jobs repository: validation failed")
)

type Store struct{ *storedb.DB }

func New(db *storedb.DB) *Store { return &Store{DB: db} }

type JobKey struct {
	ProjectID string
	JobID     string
}

type JobBinding struct {
	Flow         agentdomain.Flow
	RootThreadID string
	TurnID       string
}

// JobBinding reads the native thread of a readable chat turn Job: the flow of its session's kind, the chat root
// thread and the provider turn, both empty until the turn has started.
func (store *Store) JobBinding(ctx context.Context, key JobKey) (JobBinding, error) {
	project, projectErr := storedb.ParseUUID(key.ProjectID)
	job, jobErr := storedb.ParseUUID(key.JobID)
	if store == nil || store.DB == nil || store.Pool == nil || projectErr != nil || jobErr != nil {
		return JobBinding{}, ErrValidation
	}
	row, err := sqlc.New(store.Pool).GetNativeJobBinding(ctx, sqlc.GetNativeJobBindingParams{
		JobID: job, ProjectID: project,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return JobBinding{}, ErrNotFound
	}
	if err != nil {
		return JobBinding{}, err
	}
	flow, known := agentdomain.SessionFlow(row.Kind)
	if !known {
		return JobBinding{}, ErrNotFound
	}
	return JobBinding{Flow: flow, RootThreadID: row.RootThreadID.String, TurnID: row.TurnID.String}, nil
}
