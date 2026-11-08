package jobstore

import (
	"bytes"
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CancelJobStatus(ctx context.Context, input jobs.CancelJobStatusStoreInput) (jobs.Job, error) {
	var canceled jobs.Job
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		canceled, err = s.CancelJobStatusInTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return jobs.Job{}, err
	}
	return canceled, nil
}

func (s *Store) CancelJobStatusInTx(ctx context.Context, tx pgx.Tx, input jobs.CancelJobStatusStoreInput) (jobs.Job, error) {
	if tx == nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	resultJSON, err := marshalJobResult(input.Result)
	if err != nil {
		return jobs.Job{}, err
	}
	errorJSON, err := marshalJobError(input.Error)
	if err != nil {
		return jobs.Job{}, err
	}
	preview, err := sqlc.New(tx).PreviewJobForCancel(ctx, sqlc.PreviewJobForCancelParams{
		ProjectID: projectID, JobID: jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, err
	}
	kind := jobs.Kind(preview.Kind)
	subject, err := jobs.DecodeSubject(kind, preview.SubjectJson)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if terminalJobStatus(input.To) && modelJobKindRequiresOwnerCleanup(kind) {
		return jobs.Job{}, jobs.ErrModelOwnerCleanupRequired
	}
	if !cancelTargetSupported(kind) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	var row sqlc.Job
	err = func() error {
		if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
			return err
		}
		role, err := lockActiveJobsMembership(ctx, tx, projectID, actorUserID)
		if err != nil {
			return err
		}
		if err := s.lockCancelTarget(ctx, tx, projectID, kind, subject); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		locked, err := queries.LockJobForCancel(ctx, sqlc.LockJobForCancelParams{
			ProjectID: projectID, JobID: jobID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrNotFound
		}
		if err != nil {
			return err
		}
		if locked.Kind != preview.Kind ||
			!bytes.Equal(locked.SubjectJson, preview.SubjectJson) ||
			locked.CreatorUserID != preview.CreatorUserID {
			return jobs.ErrNotFound
		}
		if !actorCanCancelLockedJob(actorUserID, role, locked.CreatorUserID) {
			return jobs.ErrForbidden
		}
		if jobs.Status(locked.Status) != input.From {
			return jobs.ErrConflict
		}
		if input.From == jobs.StatusQueued && input.To == jobs.StatusCanceled {
			switch kind {
			case jobs.KindImport:
				err = FinalizeImportTerminalInTx(ctx, tx, ImportTerminalInTxInput{
					ProjectID: projectID, JobID: jobID, From: input.From, To: input.To,
					ResultJSON: resultJSON, ErrorJSON: errorJSON,
					UpdatedAt: storedb.Timestamptz(input.UpdatedAt), QueuedCancel: true,
				}, nil)
				if errors.Is(err, ErrImportTerminalConflict) {
					return jobs.ErrNotFound
				}
				if err != nil {
					return err
				}
				row, err = queries.LockJobForCancel(ctx, sqlc.LockJobForCancelParams{
					ProjectID: projectID, JobID: jobID,
				})
				return err
			case jobs.KindCanonTextIndex, jobs.KindChapterCards, jobs.KindSagaLayer, jobs.KindChapterDraft, jobs.KindAuthorMaterialsImport:
			default:
				return jobs.ErrNotFound
			}
		} else if input.From != jobs.StatusRunning || input.To != jobs.StatusCanceling {
			return jobs.ErrConflict
		}
		row, err = queries.CancelJobStatus(ctx, sqlc.CancelJobStatusParams{
			ToStatus:    string(input.To),
			Progress:    input.Progress,
			ResultJson:  resultJSON,
			ErrorJson:   errorJSON,
			UpdatedAt:   storedb.Timestamptz(input.UpdatedAt),
			ProjectID:   projectID,
			JobID:       jobID,
			FromStatus:  string(input.From),
			ActorUserID: actorUserID,
		})
		return err
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	return JobFromSQLJob(row)
}

func actorCanCancelLockedJob(actorUserID pgtype.UUID, role projects.Role, creatorUserID pgtype.UUID) bool {
	return (creatorUserID.Valid && creatorUserID == actorUserID) || projects.CanManageMembers(role)
}

func terminalJobStatus(status jobs.Status) bool {
	switch status {
	case jobs.StatusSucceeded, jobs.StatusFailed, jobs.StatusCanceled, jobs.StatusExpired:
		return true
	default:
		return false
	}
}

func modelJobKindRequiresOwnerCleanup(kind jobs.Kind) bool {
	switch kind {
	case jobs.KindResearchChatTurn:
		return true
	default:
		return false
	}
}

func cancelTargetSupported(kind jobs.Kind) bool {
	switch kind {
	case jobs.KindImport, jobs.KindCanonTextIndex, jobs.KindChapterCards, jobs.KindSagaLayer, jobs.KindChapterDraft, jobs.KindAuthorMaterialsImport:
		return true
	default:
		return false
	}
}

func (s *Store) lockCancelTarget(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	kind jobs.Kind,
	subject jobs.Subject,
) error {
	switch typed := subject.(type) {
	case jobs.ImportSubject:
		if kind != jobs.KindImport {
			return jobs.ErrNotFound
		}
		return nil
	case jobs.CanonTextIndexSubject:
		if kind != jobs.KindCanonTextIndex || typed.ProjectID != projectID.String() {
			return jobs.ErrNotFound
		}
		return nil
	case jobs.ChapterCardsSubject:
		if kind != jobs.KindChapterCards || typed.ProjectID != projectID.String() {
			return jobs.ErrNotFound
		}
		return nil
	case jobs.SagaLayerSubject:
		if kind != jobs.KindSagaLayer || typed.ProjectID != projectID.String() {
			return jobs.ErrNotFound
		}
		return nil
	// The author's «Остановить» of the writing of a chapter (specs/2026-11-02-writer-workshop.md W4.9): its draft
	// keeps what it holds, nothing of the stopped run reaches it.
	case jobs.ChapterDraftSubject:
		if kind != jobs.KindChapterDraft || typed.ProjectID != projectID.String() {
			return jobs.ErrNotFound
		}
		return nil
	// The author's «Отменить» of an import (specs/2026-11-04-saga-map.md, R9.2): the Job removes the import when it stops.
	case jobs.AuthorMaterialsImportSubject:
		if kind != jobs.KindAuthorMaterialsImport || typed.ProjectID != projectID.String() {
			return jobs.ErrNotFound
		}
		return nil
	default:
		return jobs.ErrNotFound
	}
}

type lockModelTargetInTx func(context.Context, pgx.Tx, jobs.Subject) (jobs.JobSourceManifest, error)

type scrubPrivateInputInTx func(context.Context, pgx.Tx, jobs.ModelBinding) (pgtype.UUID, error)

type modelTerminalJobInTxInput struct {
	projectID        pgtype.UUID
	jobID            pgtype.UUID
	claimIncarnation int64
	from             jobs.Status
	to               jobs.Status
	resultJSON       []byte
	errorJSON        []byte
	expiresAt        pgtype.Timestamptz
	queuedCancel     bool
	sourceDeleted    bool
	purge            bool
}

func (s *Store) TombstoneSourceDeletedModelJobInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectIDValue, jobIDValue string,
) error {
	if s == nil || tx == nil || ctx == nil {
		return jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(projectIDValue)
	if err != nil {
		return jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(jobIDValue)
	if err != nil {
		return jobs.ErrValidation
	}
	bindingRow, err := sqlc.New(tx).LockJobModelBinding(ctx, sqlc.LockJobModelBindingParams{
		ProjectID: projectID,
		JobID:     jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	binding := modelBindingFromSQL(bindingRow)
	if binding.Validate() != nil {
		return jobs.ErrConflict
	}

	if !userModelKind(binding.Kind) || binding.Origin != jobs.ModelOriginUser {
		return jobs.ErrConflict
	}
	scrub := func(ctx context.Context, tx pgx.Tx, binding jobs.ModelBinding) (pgtype.UUID, error) {
		return s.scrubAgentWorkflowInput(ctx, tx, binding, true, true)
	}
	return terminalizeModelJobInTx(ctx, tx, modelTerminalJobInTxInput{
		projectID:     projectID,
		jobID:         jobID,
		to:            jobs.StatusExpired,
		sourceDeleted: true,
	}, scrub)
}

func terminalizeModelJobInTx(
	ctx context.Context,
	tx pgx.Tx,
	input modelTerminalJobInTxInput,
	scrub scrubPrivateInputInTx,
) error {
	if scrub == nil || !input.projectID.Valid || !input.jobID.Valid || input.sourceDeleted && input.purge {
		return jobs.ErrModelOwnerCleanupRequired
	}
	var previewKind string
	var previewOwnerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT kind, artifact_source_owner_id
		FROM jobs
		WHERE project_id = $1 AND id = $2
	`, input.projectID, input.jobID).Scan(&previewKind, &previewOwnerID); errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	} else if err != nil {
		return err
	}
	if !previewOwnerID.Valid {
		return jobs.ErrConflict
	}
	lifecycleCancel := input.to == jobs.StatusCanceled && !input.sourceDeleted && !input.purge &&
		userModelKind(jobs.Kind(previewKind))
	var lockedOwnerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT owner.id
		FROM artifact_source_owners AS owner
		WHERE owner.project_id = $1 AND owner.id = $2
		  AND owner.artifact_kind = 'job' AND owner.artifact_id = $3
		  AND (
		    $4 OR CASE WHEN $5
		      THEN ck_artifact_source_owner_is_lifecycle_readable(owner.id, owner.project_id, 'job', owner.artifact_id)
		      ELSE ck_artifact_source_owner_is_readable(owner.id, owner.project_id, 'job', owner.artifact_id)
		    END
		  )
		FOR UPDATE
	`, input.projectID, previewOwnerID, input.jobID, input.sourceDeleted || input.purge, lifecycleCancel).Scan(&lockedOwnerID); errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	} else if err != nil {
		return err
	}

	var kind, status string
	var claimIncarnation pgtype.Int8
	var cancelRequestedAt pgtype.Timestamptz
	var jobOwnerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT kind, status, claim_incarnation, cancel_requested_at, artifact_source_owner_id
		FROM jobs
		WHERE project_id = $1 AND id = $2
		FOR UPDATE
	`, input.projectID, input.jobID).Scan(
		&kind, &status, &claimIncarnation, &cancelRequestedAt, &jobOwnerID,
	); errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	} else if err != nil {
		return err
	}
	if kind != previewKind || jobOwnerID != lockedOwnerID || !modelJobKindRequiresOwnerCleanup(jobs.Kind(kind)) ||
		!modelTerminalPreconditionMatches(input, jobs.Status(status), claimIncarnation, cancelRequestedAt) {
		return jobs.ErrConflict
	}

	queries := sqlc.New(tx)
	binding, err := queries.LockJobModelBinding(ctx, sqlc.LockJobModelBindingParams{
		ProjectID: input.projectID, JobID: input.jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	if binding.ArtifactSourceOwnerID != jobOwnerID || binding.JobKind != kind {
		return jobs.ErrConflict
	}
	modelBinding := modelBindingFromSQL(binding)
	if modelBinding.Validate() != nil {
		return jobs.ErrConflict
	}
	privateOwnerID, err := scrub(ctx, tx, modelBinding)
	if err != nil {
		return err
	}
	if !privateOwnerID.Valid {
		return jobs.ErrModelOwnerCleanupRequired
	}
	var privateOwnerOrRefsRemain bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM artifact_source_owners WHERE project_id = $1 AND id = $2
		  UNION ALL
		  SELECT 1 FROM artifact_source_refs WHERE project_id = $1 AND owner_id = $2
		)
	`, input.projectID, privateOwnerID).Scan(&privateOwnerOrRefsRemain); err != nil {
		return err
	}
	if privateOwnerOrRefsRemain {
		return jobs.ErrModelOwnerCleanupRequired
	}
	if _, err := queries.DeleteJobModelBinding(ctx, sqlc.DeleteJobModelBindingParams{
		ProjectID: input.projectID, JobID: input.jobID,
		ArtifactSourceOwnerID: jobOwnerID,
	}); err != nil {
		return err
	}

	switch {
	case input.purge:
		return purgeLockedModelJob(ctx, tx, input, jobOwnerID, kind)
	case input.sourceDeleted:
		return tombstoneLockedModelJob(ctx, tx, input, jobOwnerID, kind)
	default:
		return sealLockedModelJobTerminal(ctx, tx, input, jobOwnerID, kind)
	}
}

func modelTerminalPreconditionMatches(
	input modelTerminalJobInTxInput,
	status jobs.Status,
	claimIncarnation pgtype.Int8,
	cancelRequestedAt pgtype.Timestamptz,
) bool {
	if input.sourceDeleted || input.purge {
		return !terminalJobStatus(status)
	}
	if status != input.from {
		return false
	}
	if input.queuedCancel {
		return input.from == jobs.StatusQueued && input.to == jobs.StatusCanceled && !cancelRequestedAt.Valid
	}
	if !claimIncarnation.Valid || claimIncarnation.Int64 != input.claimIncarnation {
		return false
	}
	return input.from == jobs.StatusRunning &&
		(input.to == jobs.StatusSucceeded || input.to == jobs.StatusFailed) && !cancelRequestedAt.Valid ||
		input.from == jobs.StatusCanceling && input.to == jobs.StatusCanceled && cancelRequestedAt.Valid
}

func sealLockedModelJobTerminal(
	ctx context.Context,
	tx pgx.Tx,
	input modelTerminalJobInTxInput,
	ownerID pgtype.UUID,
	kind string,
) error {
	var terminalAt pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		WITH terminal_clock AS (
		  SELECT date_trunc('microseconds', clock_timestamp()) AS value
		)
		UPDATE jobs
		SET status = $1,
		    progress_json = '{}'::jsonb,
		    result_json = $2::jsonb, error_json = $3::jsonb,
		    cancel_requested_at = CASE WHEN $4 THEN clock_timestamp() ELSE cancel_requested_at END,
		    claimed_at = NULL, expires_at = $5,
		    completed_at = terminal_clock.value, updated_at = terminal_clock.value
		FROM terminal_clock
		WHERE project_id = $6 AND id = $7 AND kind = $8
		  AND artifact_source_owner_id = $9 AND status = $10
		RETURNING completed_at
	`, string(input.to), input.resultJSON, input.errorJSON,
		input.queuedCancel, input.expiresAt, input.projectID, input.jobID, kind, ownerID, string(input.from)).Scan(&terminalAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrConflict
		}
		return err
	}
	if !terminalAt.Valid {
		return jobs.ErrConflict
	}
	return nil
}

func tombstoneLockedModelJob(
	ctx context.Context,
	tx pgx.Tx,
	input modelTerminalJobInTxInput,
	ownerID pgtype.UUID,
	kind string,
) error {
	updated, err := tx.Exec(ctx, `
		WITH terminal_clock AS (
		  SELECT date_trunc('microseconds', clock_timestamp()) AS value
		)
		UPDATE jobs
		SET status = 'expired', progress_json = '{}'::jsonb, subject_json = NULL,
		    creator_user_id = NULL, can_cancel = false, result_json = NULL,
		    error_json = NULL, claimed_at = NULL, cancel_requested_at = NULL,
		    artifact_source_owner_id = NULL, governance_payload_state = 'source_deleted',
		    expires_at = terminal_clock.value,
		    completed_at = COALESCE(completed_at, terminal_clock.value),
		    updated_at = terminal_clock.value
		FROM terminal_clock
		WHERE project_id = $1 AND id = $2 AND kind = $3
		  AND artifact_source_owner_id = $4
	`, input.projectID, input.jobID, kind, ownerID)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return jobs.ErrConflict
	}
	return deleteLockedJobOwner(ctx, tx, input.projectID, input.jobID, ownerID)
}

func purgeLockedModelJob(
	ctx context.Context,
	tx pgx.Tx,
	input modelTerminalJobInTxInput,
	ownerID pgtype.UUID,
	kind string,
) error {
	deleted, err := tx.Exec(ctx, `
		DELETE FROM jobs
		WHERE project_id = $1 AND id = $2 AND kind = $3
		  AND artifact_source_owner_id = $4
	`, input.projectID, input.jobID, kind, ownerID)
	if err != nil {
		return err
	}
	if deleted.RowsAffected() != 1 {
		return jobs.ErrConflict
	}
	return deleteLockedJobOwner(ctx, tx, input.projectID, input.jobID, ownerID)
}

func deleteLockedJobOwner(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	jobID pgtype.UUID,
	ownerID pgtype.UUID,
) error {
	deleted, err := tx.Exec(ctx, `
		DELETE FROM artifact_source_owners
		WHERE project_id = $1 AND id = $2 AND artifact_kind = 'job' AND artifact_id = $3
	`, projectID, ownerID, jobID)
	if err != nil {
		return err
	}
	if deleted.RowsAffected() != 1 {
		return jobs.ErrConflict
	}
	return nil
}
