package librarystore

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) FinalizeImport(ctx context.Context, input library.FinalizeImportStoreInput) (library.FinalizedImport, error) {
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil || input.ExpectedClaimIncarnation < 1 {
		return library.FinalizedImport{}, library.ErrConflict
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.FinalizedImport{}, library.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return library.FinalizedImport{}, library.ErrNotFound
	}
	initialPublication := input.InitialPublication
	if initialPublication == "" {
		initialPublication = library.InitialPublicationDraftOnly
	}
	if initialPublication != library.InitialPublicationDraftOnly && initialPublication != library.InitialPublicationPublishImported {
		return library.FinalizedImport{}, library.ErrValidation
	}
	if strings.TrimSpace(input.StorageKey) == "" ||
		strings.TrimSpace(input.BookTitle) == "" ||
		strings.TrimSpace(input.SourceFileName) == "" ||
		len(input.Chapters) == 0 {
		return library.FinalizedImport{}, library.ErrValidation
	}
	updatesBook := input.ImportMode.UpdatesBook()
	previewsBook := input.ImportMode == library.ImportModeUpdatePreview
	var targetBookID pgtype.UUID
	switch {
	case updatesBook:
		// An update publishes the new text at once, as every supported file format does; its preview reads the
		// same plan and writes nothing into the book.
		if initialPublication != library.InitialPublicationPublishImported {
			return library.FinalizedImport{}, library.ErrValidation
		}
		if targetBookID, err = storedb.ParseUUID(input.BookID); err != nil {
			return library.FinalizedImport{}, library.ErrNotFound
		}
	case input.ImportMode != library.ImportModeNewBook || input.BookID != "":
		return library.FinalizedImport{}, library.ErrValidation
	}

	var finalized library.FinalizedImport
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
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role
			FROM project_memberships
			WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
			FOR SHARE
		`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if !projects.CanMutateProject(projects.Role(role)) {
			return library.ErrForbidden
		}

		var finalizedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&finalizedAt); err != nil {
			return err
		}
		selectedView := mustDraftSourceView()
		var written importedBookWrite
		switch {
		case previewsBook:
			written, err = s.previewImportedBookInTx(ctx, tx, input, projectID, targetBookID, projectGeneration)
		case updatesBook:
			written, err = s.updateImportedBookInTx(ctx, tx, input, projectID, targetBookID, projectGeneration, finalizedAt)
		default:
			// Only a new book opens the wave of «Сага готова» (FS1.2), and before its own text index Job is queued.
			if err = openReadyWaveInTx(ctx, tx, projectID, jobID, actorUserID); err != nil {
				return err
			}
			written, err = s.createImportedBookInTx(ctx, tx, input, jobID, projectID, projectGeneration, finalizedAt, initialPublication)
		}
		if err != nil {
			return err
		}
		// The written text is checked against the owner's quotas before anything else commits:
		// a refusal rolls the book back and the import Job fails with the quota code (R3.3). A preview
		// writes no text; «Обновить из файла» grows no book (specs/2026-11-08-subscription-tiers.md TR2.5). The one
		// import of the demo saga is its seed, admitted at staging and out of the limits (DS3.4).
		if !previewsBook {
			grows := quota.MeasureBooks | quota.MeasureChapters | quota.MeasureCharacters
			if updatesBook {
				grows = quota.MeasureChapters | quota.MeasureCharacters
			}
			if err := s.quota.CheckAdmittedSourcesInTx(ctx, tx, projectID.String(), grows); err != nil {
				return err
			}
		}
		producedRefs, paragraphCount := written.refs, written.paragraphCount

		var status string
		var claimIncarnation pgtype.Int8
		var jobOwnerID pgtype.UUID
		var progressJSON []byte
		if err := tx.QueryRow(ctx, `
			SELECT status, claim_incarnation, artifact_source_owner_id, progress_json
			FROM jobs
			WHERE id = $1
			  AND project_id = $2
			  AND kind = 'import'
			  AND status = 'running'
			  AND claim_incarnation = $3
			  AND cancel_requested_at IS NULL
			  AND ck_artifact_source_owner_is_readable(
			    artifact_source_owner_id, project_id, 'job', id
			  )
			FOR UPDATE
		`, jobID, projectID, input.ExpectedClaimIncarnation).Scan(&status, &claimIncarnation, &jobOwnerID, &progressJSON); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}
		if status != "running" || !claimIncarnation.Valid || claimIncarnation.Int64 != input.ExpectedClaimIncarnation {
			return library.ErrConflict
		}
		progress, err := jobstore.ParseImportProgress(progressJSON, jobs.StatusRunning)
		if err != nil || progress.Phase != jobs.ImportPhaseSaving || progress.TotalChapters == nil ||
			*progress.TotalChapters != len(input.Chapters) {
			return library.ErrConflict
		}

		var requestActorID, requestOwnerID pgtype.UUID
		var requestStorageKey, requestImportMode, requestInitialPublication, requestSourceFileName, requestSHA256 string
		var requestSize int64
		if err := tx.QueryRow(ctx, `
			SELECT actor_user_id, storage_key, import_mode, initial_publication, source_file_name, size_bytes, file_sha256,
			       artifact_source_owner_id
			FROM import_job_requests AS request
			WHERE job_id = $1 AND project_id = $2
			  AND ck_artifact_source_owner_is_readable(
			    request.artifact_source_owner_id, request.project_id, 'import_job_request', request.job_id
			  )
			FOR UPDATE
		`, jobID, projectID).Scan(
			&requestActorID, &requestStorageKey, &requestImportMode, &requestInitialPublication, &requestSourceFileName, &requestSize, &requestSHA256,
			&requestOwnerID,
		); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if requestActorID != actorUserID ||
			requestStorageKey != input.StorageKey ||
			requestImportMode != string(input.ImportMode) ||
			requestInitialPublication != string(initialPublication) ||
			requestSourceFileName != input.SourceFileName {
			return library.ErrConflict
		}

		var uploadSize int64
		var uploadSHA256 string
		var uploadArtifactID, uploadOwnerID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT size_bytes, sha256, artifact_id, artifact_source_owner_id
			FROM import_uploads AS upload
			WHERE storage_key = $1
			  AND project_id = $2
			  AND content IS NOT NULL
			  AND clock_timestamp() < expires_at
			  AND ck_artifact_source_owner_is_readable(
			    upload.artifact_source_owner_id, upload.project_id, 'import_upload', upload.artifact_id
			  )
			FOR UPDATE
		`, input.StorageKey, projectID).Scan(&uploadSize, &uploadSHA256, &uploadArtifactID, &uploadOwnerID); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if uploadSize != requestSize || !strings.EqualFold(uploadSHA256, requestSHA256) {
			return library.ErrConflict
		}
		if err := lockImportArtifactOwners(
			ctx, tx, projectID, jobOwnerID, requestOwnerID, uploadOwnerID,
		); err != nil {
			return err
		}

		if err := s.replaceImportArtifactSources(
			ctx,
			tx,
			input.ProjectID,
			governance.LifecycleGeneration(projectGeneration),
			input.JobID,
			input.ExpectedClaimIncarnation,
			input.StorageKey,
			importArtifactOwnerSet{
				jobOwnerID:       jobOwnerID.String(),
				requestOwnerID:   requestOwnerID.String(),
				uploadOwnerID:    uploadOwnerID.String(),
				uploadArtifactID: uploadArtifactID.String(),
			},
			producedRefs,
			selectedView,
			s.hitImportFinalize,
		); err != nil {
			return err
		}
		finalized = written.counts
		finalized.BookID, finalized.ChapterCount, finalized.ParagraphCount = written.bookID.String(), len(input.Chapters), paragraphCount
		finalized.ImportMode, finalized.SourceFileName = input.ImportMode, input.SourceFileName
		result := jobs.ImportJobResult{
			Type: jobs.KindImport, BookID: finalized.BookID,
			ChapterCount: finalized.ChapterCount, ParagraphCount: finalized.ParagraphCount,
			ImportMode: string(finalized.ImportMode), SourceFileName: finalized.SourceFileName,
		}
		if updatesBook {
			count := func(value int) *int { return &value }
			result.UnchangedChapters, result.ChangedChapters = count(finalized.UnchangedChapters), count(finalized.ChangedChapters)
			result.AddedChapters, result.RemovedChapters = count(finalized.AddedChapters), count(finalized.RemovedChapters)
			result.PunctuationChapters, result.RenamedChapters = count(finalized.PunctuationChapters), count(finalized.RenamedChapters)
			result.Preview = finalized.Preview
		}
		resultJSON, err := jobs.EncodeResult(jobs.KindImport, result)
		if err != nil {
			return library.ErrValidation
		}
		var savedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&savedAt); err != nil {
			return err
		}
		savedChapters := len(input.Chapters)
		savedProgress := *progress
		savedProgress.Phase = jobs.ImportPhaseSaved
		savedProgress.SavedChapters = &savedChapters
		savedProgress.SavedAt = &savedAt.Time
		savedProgressJSON, err := jobstore.MarshalImportProgress(savedProgress, jobs.StatusSucceeded)
		if err != nil {
			return library.ErrConflict
		}
		requestTag, err := tx.Exec(ctx, `
			UPDATE import_job_requests
			SET status = 'succeeded', updated_at = $1
			WHERE job_id = $2 AND project_id = $3 AND status = 'pending'
			  AND artifact_source_owner_id = $4
		`, finalizedAt, jobID, projectID, requestOwnerID)
		if err != nil {
			return err
		}
		if requestTag.RowsAffected() != 1 {
			return library.ErrConflict
		}
		if err := s.hitImportFinalize("after_import_request_terminal"); err != nil {
			return err
		}
		commandTag, err := tx.Exec(ctx, `
			UPDATE jobs
			SET status = 'succeeded',
			    progress_json = $1::jsonb,
			    result_json = $2::jsonb,
			    error_json = NULL,
			    claimed_at = NULL,
			    completed_at = $3,
			    updated_at = $3
			WHERE id = $4 AND project_id = $5
			  AND status = 'running' AND claim_incarnation = $6
			  AND cancel_requested_at IS NULL
			  AND artifact_source_owner_id = $7
			  AND progress_json = $8::jsonb
		`, savedProgressJSON, resultJSON, savedAt, jobID, projectID, input.ExpectedClaimIncarnation, jobOwnerID, progressJSON)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() != 1 {
			return library.ErrConflict
		}
		if err := s.hitImportFinalize("after_import_job_terminal"); err != nil {
			return err
		}
		commandTag, err = tx.Exec(ctx, `
			UPDATE import_uploads
			SET content = NULL
			WHERE artifact_id = $1 AND project_id = $2 AND storage_key = $3
			  AND artifact_source_owner_id = $4 AND content IS NOT NULL
		`, uploadArtifactID, projectID, input.StorageKey, uploadOwnerID)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() != 1 {
			return library.ErrConflict
		}
		if err := s.hitImportFinalize("after_import_upload_scrub"); err != nil {
			return err
		}
		return nil
	})
	if isUniqueViolation(err) {
		return library.FinalizedImport{}, library.ErrConflict
	}
	if isCheckViolation(err) {
		return library.FinalizedImport{}, library.ErrValidation
	}
	if err != nil {
		return library.FinalizedImport{}, err
	}
	return finalized, nil
}
