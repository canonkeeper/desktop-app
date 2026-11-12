package librarystore

import (
	"context"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateImportStagingForActor(ctx context.Context, input library.CreateImportStagingForActorInput) (jobs.Job, error) {
	return s.createImportStagingForActor(ctx, input, true)
}

func (s *Store) createImportStagingForActor(ctx context.Context, input library.CreateImportStagingForActorInput, retryUniqueConflict bool) (jobs.Job, error) {
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return jobs.Job{}, library.ErrValidation
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, library.ErrNotFound
	}
	actorUserUUID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.Job{}, library.ErrNotFound
	}
	if strings.TrimSpace(input.Metadata.StorageKey) == "" ||
		strings.TrimSpace(input.Metadata.MIMEType) == "" ||
		input.Metadata.SizeBytes <= 0 ||
		int64(len(input.FileBytes)) != input.Metadata.SizeBytes ||
		strings.TrimSpace(input.Metadata.FileSHA256) == "" ||
		input.Metadata.IdempotencyKey != input.IdempotencyKey ||
		strings.TrimSpace(input.IdempotencyKey) == "" ||
		strings.TrimSpace(input.RequestMethod) == "" ||
		strings.TrimSpace(input.RequestPath) == "" ||
		strings.TrimSpace(input.RequestFingerprint) == "" {
		return jobs.Job{}, library.ErrValidation
	}
	initialPublication := input.Metadata.InitialPublication
	if initialPublication == "" {
		initialPublication = library.InitialPublicationDraftOnly
	}
	if initialPublication != library.InitialPublicationDraftOnly && initialPublication != library.InitialPublicationPublishImported {
		return jobs.Job{}, library.ErrValidation
	}
	var targetBookID pgtype.UUID
	switch input.Metadata.ImportMode {
	case library.ImportModeNewBook:
		if input.Metadata.TargetBookID != "" {
			return jobs.Job{}, library.ErrValidation
		}
	case library.ImportModeUpdateBook, library.ImportModeUpdatePreview:
		if targetBookID, err = storedb.ParseUUID(input.Metadata.TargetBookID); err != nil {
			return jobs.Job{}, library.ErrNotFound
		}
		if input.ExpectedBookOrder > 0 {
			return jobs.Job{}, library.ErrValidation
		}
	default:
		return jobs.Job{}, library.ErrValidation
	}

	var job sqlc.Job
	var result jobs.Job
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actorUserUUID, input.SessionWriteProof); err != nil {
			return err
		}
		if err := lockImportProjectActor(ctx, tx, projectUUID, actorUserUUID); err != nil {
			return err
		}
		var projectGeneration int64
		var demo bool
		var createdAt, expiresAt time.Time
		if err := tx.QueryRow(ctx, `
			WITH stamp AS MATERIALIZED (
				SELECT date_trunc('microseconds', clock_timestamp()) AS created_at
			)
			SELECT project.lifecycle_generation,
			       project.demo,
			       stamp.created_at,
			       stamp.created_at + interval '24 hours'
			FROM projects AS project
			CROSS JOIN stamp
			WHERE project.id = $1 AND project.deleted_at IS NULL
		`, projectUUID).Scan(&projectGeneration, &demo, &createdAt, &expiresAt); err != nil {
			return err
		}
		// The demo saga takes its seed and no other import, in any mode, and the seed goes into no other saga
		// (specs/2026-11-09-demo-saga.md DS3.3, DS3.4).
		if demo && !input.Seed {
			return quota.ErrDemoSaga
		}
		if input.Seed && (!demo || targetBookID.Valid) {
			return library.ErrValidation
		}
		progressJSON, err := jobstore.MarshalImportProgress(jobs.ImportProgress{
			Phase: jobs.ImportPhaseQueued, BytesTransferred: input.Metadata.SizeBytes,
			BytesTotal: input.Metadata.SizeBytes, QueuedAt: createdAt, TransferredAt: createdAt,
		}, jobs.StatusQueued)
		if err != nil {
			return library.ErrValidation
		}
		queries := sqlc.New(tx)
		existing, existingErr := queries.GetImportJobByIdempotencyKey(ctx, sqlc.GetImportJobByIdempotencyKeyParams{
			ActorUserID: actorUserUUID, RequestMethod: input.RequestMethod,
			RequestPath: input.RequestPath, IdempotencyKey: input.IdempotencyKey,
		})
		if existingErr == nil {
			if existing.RequestFingerprint != input.RequestFingerprint {
				return library.ErrConflict
			}
			current, err := queries.GetJob(ctx, sqlc.GetJobParams{
				JobID: existing.ID, ProjectID: existing.ProjectID, ActorUserID: actorUserUUID,
			})
			if err != nil {
				return err
			}
			result, err = jobstore.JobFromSQLJob(current)
			if errors.Is(err, jobs.ErrConflict) {
				return library.ErrConflict
			}
			return err
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		if err := ensureNoActiveImportQueueReservation(ctx, tx, projectUUID); err != nil {
			return err
		}
		expectedBookOrder := 0
		if targetBookID.Valid {
			// An update rewrites a live book of this project in place; it takes no new place.
			if err := tx.QueryRow(ctx, `
				SELECT id FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL FOR SHARE
			`, targetBookID, projectUUID).Scan(&targetBookID); errors.Is(err, pgx.ErrNoRows) {
				return library.ErrNotFound
			} else if err != nil {
				return err
			}
		} else {
			expectedBookOrder, err = validatedNextBookOrder(ctx, tx, projectUUID)
			if err != nil {
				return err
			}
			if input.ExpectedBookOrder > 0 && input.ExpectedBookOrder != expectedBookOrder {
				return library.ErrImportExpectedOrderConflict
			}
		}
		// Quotas count only a new idempotency key (R1.3): books and text of a new book for the
		// project owner (R3.2, R1.4), then the upload for the actor (R3.1). The owner's quota lock
		// comes before the actor's day row, as on every path (owner-tools lock order).
		//
		// The seed of the demo saga stands out of the limits and of the day (DS3.4): under the same lock of the
		// owner the saga holds neither its book nor an import of it, or the seed is a conflict.
		switch {
		case input.Seed:
			if err := s.quota.CheckDemoSeedInTx(ctx, tx, projectUUID.String()); errors.Is(err, quota.ErrDemoSeed) {
				return library.ErrConflict
			} else if err != nil {
				return err
			}
		case !targetBookID.Valid:
			if err := s.quota.CheckNewBooksInTx(ctx, tx, projectUUID.String(), 1, true); err != nil {
				return err
			}
		}
		// «Обновить из файла» costs one upload: the preview counts it, and the update of the same book by the
		// same author with the file the preview read does not count it again (specs/2026-10-27-update-book-from-file.md, R6.4).
		previewed := false
		if input.Metadata.ImportMode == library.ImportModeUpdateBook {
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM import_job_requests AS request
					WHERE request.project_id = $1 AND request.actor_user_id = $2 AND request.target_book_id = $3
					  AND request.import_mode = 'update_preview' AND request.status = 'succeeded'
					  AND lower(request.file_sha256) = lower($4) AND request.created_at > $5::timestamptz - interval '24 hours'
				)
			`, projectUUID, actorUserUUID, targetBookID, input.Metadata.FileSHA256, createdAt).Scan(&previewed); err != nil {
				return err
			}
		}
		if !previewed && !input.Seed {
			if err := s.quota.ReserveDailyInTx(ctx, tx, actorUserUUID.String(), quota.KeyImportsPerDay, 1, createdAt); err != nil {
				return err
			}
		}
		var uploadArtifactID, uploadOwnerID, uploadRefID pgtype.UUID
		var jobOwnerID, jobRefID, requestOwnerID, requestRefID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(),
			       gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid()
		`).Scan(
			&uploadArtifactID, &uploadOwnerID, &uploadRefID,
			&jobOwnerID, &jobRefID, &requestOwnerID, &requestRefID,
		); err != nil {
			return err
		}
		if err := s.createImportProjectArtifact(
			ctx, tx, projectUUID, projectGeneration,
			governance.ArtifactImportUpload, uploadArtifactID, uploadOwnerID, uploadRefID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				rowsAffected, err := sqlc.New(tx).StoreImportFile(ctx, sqlc.StoreImportFileParams{
					StorageKey:            input.Metadata.StorageKey,
					MimeType:              input.Metadata.MIMEType,
					SizeBytes:             input.Metadata.SizeBytes,
					Sha256:                input.Metadata.FileSHA256,
					Content:               input.FileBytes,
					ExpiresAt:             storedb.Timestamptz(expiresAt),
					CreatedAt:             storedb.Timestamptz(createdAt),
					ArtifactID:            uploadArtifactID,
					ArtifactSourceOwnerID: ownerID,
					ProjectID:             projectUUID,
				})
				if err != nil {
					return err
				}
				if rowsAffected != 1 {
					return library.ErrNotFound
				}
				return nil
			},
		); err != nil {
			return err
		}
		if err := s.createImportProjectArtifact(
			ctx, tx, projectUUID, projectGeneration,
			governance.ArtifactJob, jobID, jobOwnerID, jobRefID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				job, err = sqlc.New(tx).CreateImportJobRecord(ctx, sqlc.CreateImportJobRecordParams{
					ID:                    jobID,
					Kind:                  library.ImportJobKind,
					Status:                library.ImportJobStatusQueued,
					ProgressJson:          progressJSON,
					ExpectedBookOrder:     int32(expectedBookOrder),
					BookID:                targetBookID,
					Preview:               input.Metadata.ImportMode == library.ImportModeUpdatePreview,
					CreatorUserID:         actorUserUUID,
					CanCancel:             true,
					CreatedAt:             storedb.Timestamptz(createdAt),
					UpdatedAt:             storedb.Timestamptz(createdAt),
					ArtifactSourceOwnerID: ownerID,
					CreatorRequestID:      storedb.CreatorRequestID(ctx),
					ProjectID:             projectUUID,
				})
				return err
			},
		); err != nil {
			return err
		}
		if err := s.createImportProjectArtifact(
			ctx, tx, projectUUID, projectGeneration,
			governance.ArtifactImportJobRequest, jobID, requestOwnerID, requestRefID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				return sqlc.New(tx).CreateImportJobRequest(ctx, sqlc.CreateImportJobRequestParams{
					JobID:                 jobID,
					ProjectID:             projectUUID,
					ActorUserID:           actorUserUUID,
					StorageKey:            input.Metadata.StorageKey,
					IdempotencyKey:        input.IdempotencyKey,
					RequestMethod:         input.RequestMethod,
					RequestPath:           input.RequestPath,
					RequestFingerprint:    input.RequestFingerprint,
					SourceFileName:        input.Metadata.SourceFileName,
					Extension:             input.Metadata.Extension,
					MimeType:              input.Metadata.MIMEType,
					SizeBytes:             input.Metadata.SizeBytes,
					FileSha256:            input.Metadata.FileSHA256,
					ImportMode:            string(input.Metadata.ImportMode),
					TargetBookID:          targetBookID,
					InitialPublication:    string(initialPublication),
					Title:                 textValue(input.Metadata.Title),
					CanCancel:             true,
					CreatedAt:             storedb.Timestamptz(createdAt),
					UpdatedAt:             storedb.Timestamptz(createdAt),
					ArtifactSourceOwnerID: ownerID,
				})
			},
		); err != nil {
			return err
		}
		rowsAffected, err := queries.MarkImportJobQueued(ctx, sqlc.MarkImportJobQueuedParams{
			EnqueuedAt: storedb.Timestamptz(createdAt),
			JobID:      jobID,
			ProjectID:  projectUUID,
			StorageKey: input.Metadata.StorageKey,
		})
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return library.ErrNotFound
		}
		result, err = jobstore.JobFromSQLJob(job)
		if errors.Is(err, jobs.ErrConflict) {
			return library.ErrConflict
		}
		if err != nil {
			return err
		}
		if err := s.hitImportStaging("before_commit"); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, library.ErrNotFound
	}
	if isUniqueViolation(err) {
		if retryUniqueConflict {
			return s.createImportStagingForActor(ctx, input, false)
		}
		return jobs.Job{}, library.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	return result, nil
}

func (s *Store) createImportProjectArtifact(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	kind governance.ArtifactKind,
	artifactID pgtype.UUID,
	ownerID pgtype.UUID,
	refID pgtype.UUID,
	mutate governancestore.ArtifactSourceCreateMutation,
) error {
	if !projectID.Valid || !artifactID.Valid || !ownerID.Valid || !refID.Valid ||
		projectGeneration < 1 || mutate == nil {
		return library.ErrValidation
	}
	vector := governance.SourceGenerationVector{
		ProjectID:         projectID.String(),
		ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
	}
	ref, err := governance.NewSourceIndependentProjectRef(vector, governance.AbsentChatArm())
	if err != nil {
		return library.ErrValidation
	}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind:            kind,
		ProjectID:       projectID.String(),
		ArtifactID:      artifactID.String(),
		OwnerSourceView: governance.AbsentSourceView(),
		Refs:            []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		return library.ErrValidation
	}
	sourcesStore := s.artifactSourcesStore(func(point string) error {
		return s.hitImportStaging(string(kind) + "." + point)
	})
	return sourcesStore.CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID.String(), ProjectID: projectID.String(), ArtifactID: artifactID.String(),
		Kind: kind, SourceView: governance.AbsentSourceView(),
		Refs: []governance.ArtifactSourceRef{ref}, RefIDs: []string{refID.String()}, Manifest: manifest,
	}, mutate)
}

func (s *Store) hitImportStaging(point string) error {
	if s == nil || s.importStagingFailpoint == nil {
		return nil
	}
	return s.importStagingFailpoint(point)
}
