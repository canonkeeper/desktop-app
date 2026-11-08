package librarystore

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func (s *Store) LoadImportBodyForClaim(ctx context.Context, claim jobs.WorkerClaim, storageKey string) ([]byte, error) {
	if err := claim.Validate(); err != nil || strings.TrimSpace(storageKey) == "" {
		return nil, library.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	jobUUID, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return nil, library.ErrNotFound
	}
	queries := sqlc.New(s.Pool)
	params := sqlc.LoadImportBodyForClaimParams{
		JobID:            jobUUID,
		ProjectID:        projectUUID,
		ClaimIncarnation: claim.ClaimIncarnation,
		StorageKey:       storageKey,
	}
	data, err := queries.LoadImportBodyForClaim(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		expired, dispositionErr := queries.IsImportBodyExpiredForClaim(ctx, sqlc.IsImportBodyExpiredForClaimParams(params))
		if dispositionErr == nil && expired {
			return nil, library.ErrImportUploadExpired
		}
		if errors.Is(dispositionErr, pgx.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		return nil, dispositionErr
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, library.ErrNotFound
	}
	return append([]byte(nil), data...), nil
}
func (s *Store) GetImportJobForExecution(ctx context.Context, projectID, jobID string) (library.ImportJob, error) {
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.ImportJob{}, library.ErrNotFound
	}
	jobUUID, err := storedb.ParseUUID(jobID)
	if err != nil {
		return library.ImportJob{}, library.ErrNotFound
	}
	row, err := sqlc.New(s.Pool).GetImportJobForExecution(ctx, sqlc.GetImportJobForExecutionParams{
		JobID: jobUUID, ProjectID: projectUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ImportJob{}, library.ErrNotFound
	}
	if err != nil {
		return library.ImportJob{}, err
	}
	return importJobFromExecutionRow(row)
}
