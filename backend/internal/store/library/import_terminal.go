package librarystore

import (
	"context"
	"encoding/json"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) FinalizeImportTerminalForClaim(
	ctx context.Context,
	input library.FinalizeImportTerminalForClaimInput,
) (jobs.Status, error) {
	if err := input.Claim.Validate(); err != nil {
		return "", library.ErrConflict
	}
	switch input.Status {
	case jobs.StatusFailed:
		if input.Error == nil || input.Error.Code == "" || input.Error.Message == "" {
			return "", library.ErrValidation
		}
	case jobs.StatusCanceled:
		if input.Error != nil {
			return "", library.ErrValidation
		}
	default:
		return "", library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.Claim.ProjectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	jobID, err := storedb.ParseUUID(input.Claim.JobID)
	if err != nil {
		return "", library.ErrConflict
	}
	var errorJSON []byte
	if input.Error != nil {
		errorJSON, err = json.Marshal(input.Error)
		if err != nil {
			return "", library.ErrValidation
		}
	}
	from := jobs.StatusRunning
	if input.Status == jobs.StatusCanceled {
		from = jobs.StatusCanceling
	}

	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var lockedProjectID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, projectID).Scan(&lockedProjectID); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		return jobstore.FinalizeImportTerminalInTx(ctx, tx, jobstore.ImportTerminalInTxInput{
			ProjectID: projectID, JobID: jobID, ClaimIncarnation: input.Claim.ClaimIncarnation,
			From: from,
			To:   input.Status, ResultJSON: nil, ErrorJSON: errorJSON,
		}, s.hitImportFinalize)
	})
	if errors.Is(err, jobstore.ErrImportTerminalConflict) {
		return "", library.ErrConflict
	}
	if err != nil {
		return "", err
	}
	return input.Status, nil
}
