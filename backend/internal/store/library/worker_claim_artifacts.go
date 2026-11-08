package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ExpireImportStagingForClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Status, error) {
	if err := claim.Validate(); err != nil {
		return "", library.ErrConflict
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return "", library.ErrNotFound
	}
	jobID, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return "", library.ErrConflict
	}

	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var projectGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var jobOwnerID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT artifact_source_owner_id
			FROM jobs AS job
			WHERE id = $1 AND project_id = $2 AND kind = 'import'
			  AND status = 'running' AND claim_incarnation = $3
			  AND ck_artifact_source_owner_is_readable(
			    job.artifact_source_owner_id, job.project_id, 'job', job.id
			  )
			FOR UPDATE
		`, jobID, projectID, claim.ClaimIncarnation).Scan(&jobOwnerID); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}

		var storageKey string
		var requestOwnerID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT storage_key, artifact_source_owner_id
			FROM import_job_requests AS request
			WHERE job_id = $1 AND project_id = $2 AND status = 'pending'
			  AND ck_artifact_source_owner_is_readable(
			    request.artifact_source_owner_id, request.project_id, 'import_job_request', request.job_id
			  )
			FOR UPDATE
		`, jobID, projectID).Scan(&storageKey, &requestOwnerID); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}

		var uploadArtifactID, uploadOwnerID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT artifact_id, artifact_source_owner_id
			FROM import_uploads AS upload
			WHERE storage_key = $1 AND project_id = $2
			  AND content IS NOT NULL
			  AND expires_at <= clock_timestamp()
			  AND ck_artifact_source_owner_is_readable(
			    upload.artifact_source_owner_id, upload.project_id, 'import_upload', upload.artifact_id
			  )
			FOR UPDATE
		`, storageKey, projectID).Scan(&uploadArtifactID, &uploadOwnerID); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}

		if err := lockImportArtifactOwners(
			ctx, tx, projectID, jobOwnerID, requestOwnerID, uploadOwnerID,
		); err != nil {
			return err
		}
		terminalAt := pgtype.Timestamptz{}
		if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&terminalAt); err != nil {
			return err
		}
		projectRef, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
			ProjectID: claim.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		}, governance.AbsentChatArm())
		if err != nil {
			return library.ErrValidation
		}
		operations := []struct {
			kind       governance.ArtifactKind
			artifactID string
			ownerID    string
			mutate     governancestore.ArtifactSourceDeleteMutation
		}{
			{
				kind: governance.ArtifactJob, artifactID: claim.JobID, ownerID: jobOwnerID.String(),
				mutate: func(ctx context.Context, tx pgx.Tx) error {
					tag, err := tx.Exec(ctx, `
						UPDATE jobs
						SET status = 'expired', progress_json = '{}'::jsonb, subject_json = NULL,
						    creator_user_id = NULL, can_cancel = false, result_json = NULL,
						    error_json = NULL, claimed_at = NULL, cancel_requested_at = NULL,
						    artifact_source_owner_id = NULL,
						    governance_payload_state = 'source_deleted', updated_at = $1
						WHERE id = $2 AND project_id = $3 AND status = 'running'
						  AND claim_incarnation = $4 AND artifact_source_owner_id = $5
					`, terminalAt, jobID, projectID, claim.ClaimIncarnation, jobOwnerID)
					if err != nil {
						return err
					}
					if tag.RowsAffected() != 1 {
						return library.ErrConflict
					}
					return nil
				},
			},
			{
				kind: governance.ArtifactImportJobRequest, artifactID: claim.JobID, ownerID: requestOwnerID.String(),
				mutate: func(ctx context.Context, tx pgx.Tx) error {
					tag, err := tx.Exec(ctx, `
						UPDATE import_job_requests
						SET status = 'expired', governance_payload_state = 'source_deleted',
						    artifact_source_owner_id = NULL, updated_at = $1
						WHERE job_id = $2 AND project_id = $3 AND status = 'pending'
						  AND artifact_source_owner_id = $4
					`, terminalAt, jobID, projectID, requestOwnerID)
					if err != nil {
						return err
					}
					if tag.RowsAffected() != 1 {
						return library.ErrConflict
					}
					return nil
				},
			},
			{
				kind: governance.ArtifactImportUpload, artifactID: uploadArtifactID.String(), ownerID: uploadOwnerID.String(),
				mutate: func(ctx context.Context, tx pgx.Tx) error {
					tag, err := tx.Exec(ctx, `
						UPDATE import_uploads
						SET content = NULL, artifact_source_owner_id = NULL
						WHERE artifact_id = $1 AND project_id = $2 AND storage_key = $3
						  AND artifact_source_owner_id = $4 AND content IS NOT NULL
					`, uploadArtifactID, projectID, storageKey, uploadOwnerID)
					if err != nil {
						return err
					}
					if tag.RowsAffected() != 1 {
						return library.ErrConflict
					}
					return nil
				},
			},
		}

		writer := s.artifactSourcesStore(nil)
		for _, operation := range operations {
			manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
				Kind: operation.kind, ProjectID: claim.ProjectID, ArtifactID: operation.artifactID,
				OwnerSourceView: governance.AbsentSourceView(), Refs: []governance.ArtifactSourceRef{projectRef},
			})
			if err != nil {
				return library.ErrValidation
			}
			err = writer.DeleteArtifactWithSources(ctx, tx, governancestore.DeleteArtifactWithSourcesInput{
				OwnerID: operation.ownerID, ProjectID: claim.ProjectID, ArtifactID: operation.artifactID,
				Kind: operation.kind, ExpectedManifest: manifest,
			}, operation.mutate)
			if errors.Is(err, governancestore.ErrArtifactSourcesConflict) || errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
				return library.ErrConflict
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return jobs.StatusExpired, nil
}
