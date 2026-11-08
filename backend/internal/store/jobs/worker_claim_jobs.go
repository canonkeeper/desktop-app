package jobstore

import (
	"context"
	"errors"
	"slices"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (authority *WorkerClaimAuthority) ClaimNextQueuedJobForWorker(ctx context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	if authority == nil || authority.queryer == nil || len(kinds) == 0 {
		return jobs.WorkerClaimDecision{}, jobs.ErrValidation
	}
	wantedKinds := slices.Clone(kinds)
	slices.Sort(wantedKinds)
	values := make([]string, len(wantedKinds))
	for index, kind := range wantedKinds {
		if !isKnownClaimKind(kind) || (index > 0 && kind == wantedKinds[index-1]) {
			return jobs.WorkerClaimDecision{}, jobs.ErrValidation
		}
		values[index] = string(kind)
	}
	query := `
		SELECT project_id, work_id, kind, state, claim_incarnation, exhausted_status
		FROM canonkeeper_worker.claim_next_queued_job_for_worker($1, $2)
	`
	var projectID, jobID pgtype.UUID
	var claimedKind, state string
	var incarnation int64
	var exhaustedStatus pgtype.Text
	if err := authority.queryer.QueryRow(ctx, query, values, authority.releasedBefore).Scan(
		&projectID,
		&jobID,
		&claimedKind,
		&state,
		&incarnation,
		&exhaustedStatus,
	); errors.Is(err, pgx.ErrNoRows) {
		return jobs.WorkerClaimDecision{}, jobs.ErrNotFound
	} else if err != nil {
		return jobs.WorkerClaimDecision{}, err
	}
	claim := jobs.WorkerClaim{
		ProjectID:        projectID.String(),
		JobID:            jobID.String(),
		ClaimIncarnation: incarnation,
	}
	if !slices.Contains(wantedKinds, jobs.Kind(claimedKind)) ||
		(state != string(jobs.StatusRunning) && state != string(jobs.StatusCanceling)) || claim.Validate() != nil {
		return jobs.WorkerClaimDecision{}, jobs.ErrConflict
	}
	decision := jobs.WorkerClaimDecision{Claim: claim}
	if exhaustedStatus.Valid {
		decision.ExhaustedStatus = jobs.Status(exhaustedStatus.String)
		if (decision.ExhaustedStatus != jobs.StatusFailed && decision.ExhaustedStatus != jobs.StatusCanceled) ||
			(decision.ExhaustedStatus == jobs.StatusCanceled) != (state == string(jobs.StatusCanceling)) {
			return jobs.WorkerClaimDecision{}, jobs.ErrConflict
		}
	}
	return decision, nil
}

// releaseClaimsOfEarlierProcesses запоминает время старта процесса по часам базы. Backend —
// единственный процесс, который захватывает задания, поэтому захват, сделанный раньше, принадлежит
// завершившемуся процессу: функция захвата отдаёт его сразу, как захват без heartbeat 15 минут
// (`claim_incarnation` +1, попытка засчитана). Без него действует только правило 15 минут.
func (authority *WorkerClaimAuthority) releaseClaimsOfEarlierProcesses(ctx context.Context) error {
	if authority == nil || authority.queryer == nil {
		return jobs.ErrValidation
	}
	var startedAt pgtype.Timestamptz
	if err := authority.queryer.QueryRow(ctx, `SELECT pg_catalog.clock_timestamp()`).Scan(&startedAt); err != nil {
		return err
	}
	if !startedAt.Valid {
		return jobs.ErrValidation
	}
	authority.releasedBefore = startedAt
	return nil
}

func isKnownClaimKind(kind jobs.Kind) bool {
	return slices.Contains(jobs.CurrentKinds(), kind)
}

func (s *Store) LoadJobForWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(claim)
	if err != nil {
		return jobs.Job{}, err
	}
	row, err := sqlc.New(s.Pool).LoadJobForWorkerClaim(ctx, sqlc.LoadJobForWorkerClaimParams{
		ProjectID:        projectID,
		JobID:            jobID,
		ClaimIncarnation: incarnation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	job, err := jobFromColumns(row)
	if err != nil {
		return jobs.Job{}, err
	}
	if row.ClaimIncarnation != claim.ClaimIncarnation {
		return jobs.Job{}, jobs.ErrConflict
	}
	job.WorkerClaim = claim
	return job, nil
}

func (s *Store) HeartbeatWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(claim)
	if err != nil {
		return err
	}
	_, err = sqlc.New(s.Pool).HeartbeatWorkerClaim(ctx, sqlc.HeartbeatWorkerClaimParams{
		ProjectID:        projectID,
		JobID:            jobID,
		ClaimIncarnation: incarnation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	return err
}

func WorkerClaimSQLValues(claim jobs.WorkerClaim) (pgtype.UUID, pgtype.UUID, int64, error) {
	if claim.Validate() != nil {
		return pgtype.UUID{}, pgtype.UUID{}, 0, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, 0, jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, 0, jobs.ErrValidation
	}
	return projectID, jobID, claim.ClaimIncarnation, nil
}
