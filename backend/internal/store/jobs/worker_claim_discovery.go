package jobstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (authority *WorkerClaimAuthority) ClaimNextImportExpiryForWorker(ctx context.Context) (jobs.WorkerClaim, error) {
	if authority == nil || authority.queryer == nil {
		return jobs.WorkerClaim{}, library.ErrValidation
	}
	var projectID, jobID pgtype.UUID
	var claimIncarnation int64
	err := authority.queryer.QueryRow(ctx, `
		SELECT project_id, job_id, claim_incarnation
		FROM canonkeeper_worker.claim_next_import_expiry_for_worker()
	`).Scan(&projectID, &jobID, &claimIncarnation)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.WorkerClaim{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.WorkerClaim{}, err
	}
	claim := jobs.WorkerClaim{
		ProjectID: projectID.String(), JobID: jobID.String(), ClaimIncarnation: claimIncarnation,
	}
	if err := claim.Validate(); err != nil {
		return jobs.WorkerClaim{}, jobs.ErrConflict
	}
	return claim, nil
}
