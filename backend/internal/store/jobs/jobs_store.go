package jobstore

import (
	"context"
	"crypto/rand"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	*storedb.DB
	textIndexIntentFailpoint func(string) error
	modelStartFailpoint      func(string) error
}

type Option func(*Store)

func withTextIndexIntentFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.textIndexIntentFailpoint = failpoint
	}
}

func New(db *storedb.DB, options ...Option) *Store {
	store := &Store{DB: db}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

func (s *Store) artifactSourcesStore(failpoint func(string) error) *governancestore.Store {
	if failpoint == nil {
		return governancestore.New(s.DB)
	}
	return governancestore.New(s.DB, governancestore.WithFailpoint(failpoint))
}

func modelBindingFromSQL(binding sqlc.JobModelBinding) jobs.ModelBinding {
	result := jobs.ModelBinding{
		JobID: binding.JobID.String(), ProjectID: binding.ProjectID.String(),
		Kind: jobs.Kind(binding.JobKind), Origin: jobs.ModelOrigin(binding.Origin),
		InputContractVersion: uint32(binding.InputContractVersion),
	}
	if binding.ActorUserID.Valid {
		result.ActorUserID = binding.ActorUserID.String()
	}
	if binding.InitiatingAuthSessionID.Valid {
		result.InitiatingAuthSessionID = binding.InitiatingAuthSessionID.String()
	}
	return result
}

func lockActiveJobsProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) error {
	var lockedProjectID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM projects
		WHERE id = $1 AND deleted_at IS NULL
		FOR SHARE
	`, projectID).Scan(&lockedProjectID); errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrNotFound
	} else {
		return err
	}
}

func lockActiveJobsMembership(ctx context.Context, tx pgx.Tx, projectID, actorUserID pgtype.UUID) (projects.Role, error) {
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role
		FROM project_memberships
		WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
		FOR SHARE
	`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return "", jobs.ErrNotFound
	} else {
		return projects.Role(role), err
	}
}

func lockJobsMutationMembership(ctx context.Context, tx pgx.Tx, projectID, actorUserID pgtype.UUID) (projects.Role, error) {
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role
		FROM project_memberships
		WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
		FOR SHARE
	`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return "", jobs.ErrNotFound
	} else if err != nil {
		return "", err
	}
	currentRole := projects.Role(role)
	if !projects.CanMutateProject(currentRole) {
		return "", jobs.ErrForbidden
	}
	return currentRole, nil
}

// LockMutationMembership locks the membership of an actor who changes a project: a stranger is not
// found, a viewer is forbidden.
func LockMutationMembership(ctx context.Context, tx pgx.Tx, projectID, actorUserID pgtype.UUID) (projects.Role, error) {
	return lockJobsMutationMembership(ctx, tx, projectID, actorUserID)
}

func JobFromSQLJob(row sqlc.Job) (jobs.Job, error) { return jobFromColumns(row) }

func jobFromColumns(row sqlc.Job) (jobs.Job, error) {
	kind := jobs.Kind(row.Kind)
	status := jobs.Status(row.Status)
	var progress *float64
	var phase *jobs.ResearchChatPhase
	var importProgress *jobs.ImportProgress
	var err error
	if kind == jobs.KindImport {
		importProgress, err = ParseImportProgress(row.ProgressJson, status)
	} else {
		progress, phase, err = parseJobProgress(row.ProgressJson, kind, status)
	}
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	subject, err := jobs.DecodeSubject(kind, row.SubjectJson)
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	result, err := parseJobResult(kind, row.ResultJson)
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	jobError, err := parseJobError(row.ErrorJson)
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	creatorUserID := ""
	if row.CreatorUserID.Valid {
		creatorUserID = row.CreatorUserID.String()
	}
	creatorRequestID := ""
	if row.CreatorRequestID.Valid {
		creatorRequestID = row.CreatorRequestID.String()
	}
	return jobs.Job{
		ID:               row.ID.String(),
		ProjectID:        row.ProjectID.String(),
		Kind:             kind,
		Status:           status,
		Progress:         progress,
		Phase:            phase,
		ImportProgress:   importProgress,
		Subject:          subject,
		Result:           result,
		Error:            jobError,
		CanCancel:        row.CanCancel,
		CreatorUserID:    creatorUserID,
		CreatorRequestID: creatorRequestID,
		QueueClass:       jobs.QueueClass(row.QueueClass),
		RetryCount:       int(row.RetryCount),
		MaxAttempts:      int(row.MaxAttempts),
		NextAttemptAt:    row.NextAttemptAt.Time,
		WorkerClaim: jobs.WorkerClaim{
			ProjectID: row.ProjectID.String(), JobID: row.ID.String(), ClaimIncarnation: row.ClaimIncarnation,
		},
		ExpiresAt:   storedb.OptionalTime(row.ExpiresAt),
		StartedAt:   storedb.OptionalTime(row.StartedAt),
		CompletedAt: storedb.OptionalTime(row.CompletedAt),
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}, nil
}

func optionalUUIDValue(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}

func newUUIDValue() (pgtype.UUID, error) {
	var uuid pgtype.UUID
	if _, err := rand.Read(uuid.Bytes[:]); err != nil {
		return pgtype.UUID{}, err
	}
	uuid.Bytes[6] = (uuid.Bytes[6] & 0x0f) | 0x40
	uuid.Bytes[8] = (uuid.Bytes[8] & 0x3f) | 0x80
	uuid.Valid = true
	return uuid, nil
}
