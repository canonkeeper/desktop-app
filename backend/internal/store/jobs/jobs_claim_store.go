package jobstore

import (
	"bytes"
	"context"
	"errors"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) RequeueWorkerClaim(ctx context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	switch input.Kind {
	case jobs.KindResearchChatTurn:
		return jobs.Job{}, jobs.ErrModelRetryFenceRequired
	case jobs.KindImport, jobs.KindCanonTextIndex, jobs.KindChapterCards, jobs.KindIllustration, jobs.KindSagaLayer, jobs.KindChapterDraft, jobs.KindAuthorMaterialsImport:
		if input.ExpectedRetryCount == jobs.MaxJobAttempts-1 {
			if _, err := s.validateExhaustedWorkerClaim(ctx, input); err != nil {
				return jobs.Job{}, err
			}
			return jobs.Job{}, jobs.ErrRetryExhausted
		}
	default:
		return jobs.Job{}, jobs.ErrValidation
	}

	delay, err := jobs.RetryDelay(input.ExpectedRetryCount + 1)
	if err != nil {
		return jobs.Job{}, err
	}
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(input.Claim)
	if err != nil {
		return jobs.Job{}, err
	}
	row, err := sqlc.New(s.Pool).RequeueWorkerClaim(ctx, sqlc.RequeueWorkerClaimParams{
		RetryDelaySeconds:  delay.Seconds(),
		ProjectID:          projectID,
		JobID:              jobID,
		Kind:               string(input.Kind),
		ClaimIncarnation:   incarnation,
		ExpectedRetryCount: int32(input.ExpectedRetryCount),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	requeued, err := JobFromSQLJob(row)
	if err != nil {
		return jobs.Job{}, err
	}
	requeued.WorkerClaim = jobs.WorkerClaim{
		ProjectID:        input.Claim.ProjectID,
		JobID:            input.Claim.JobID,
		ClaimIncarnation: input.Claim.ClaimIncarnation + 1,
	}
	return requeued, nil
}

// ReleaseWorkerClaim возвращает в очередь задание, обработчик которого прервала штатная остановка
// процесса: точный захват `running` без запроса отмены становится `queued` без расхода попытки,
// `claim_incarnation` +1. Ход чата при следующем захвате сверяет уже начатый ход Codex по id
// сообщения клиента и не запускает его повторно.
func (s *Store) ReleaseWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(claim)
	if err != nil {
		return jobs.Job{}, err
	}
	row, err := sqlc.New(s.Pool).ReleaseWorkerClaim(ctx, sqlc.ReleaseWorkerClaimParams{
		ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	released, err := JobFromSQLJob(row)
	if err != nil {
		return jobs.Job{}, err
	}
	released.WorkerClaim = jobs.WorkerClaim{
		ProjectID: claim.ProjectID, JobID: claim.JobID, ClaimIncarnation: claim.ClaimIncarnation + 1,
	}
	return released, nil
}

// ValidateModelLateEffect повторяет точную проверку session, membership, target,
// claim, owner manifest и binding в транзакции caller-а перед каждым tool,
// delta, product write или seal. Terminal seal дополнительно обязан вызвать
// terminalizeModelJobInTx с owner-aware очисткой в той же транзакции.
func (s *Store) ValidateModelLateEffect(
	ctx context.Context,
	tx pgx.Tx,
	claim jobs.ModelClaim,
	lockTarget lockModelTargetInTx,
) error {
	if s == nil || tx == nil || lockTarget == nil || claim.Validate() != nil {
		return jobs.ErrValidation
	}
	preview := jobs.ModelClaimLockPreview{
		Claim:   claim.Claim,
		Subject: claim.Subject,
		Binding: jobs.ModelBinding{
			JobID: claim.Claim.JobID, ProjectID: claim.Claim.ProjectID, Kind: claim.Kind,
			Origin: claim.Origin, ActorUserID: claim.ActorUserID,
			InitiatingAuthSessionID: claim.InitiatingAuthSessionID,
			InputContractVersion:    claim.InputContractVersion,
		},
	}
	expectedManifest, err := lockModelClaimAuthority(ctx, tx, preview, lockTarget)
	if err != nil {
		return err
	}
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(claim.Claim)
	if err != nil {
		return err
	}
	_, _, _, _, err = lockExactModelClaim(
		ctx, tx, projectID, jobID, incarnation, preview, expectedManifest, jobs.StatusRunning,
	)
	return err
}

func lockModelClaimAuthority(
	ctx context.Context,
	tx pgx.Tx,
	preview jobs.ModelClaimLockPreview,
	lockTarget lockModelTargetInTx,
) (jobs.JobSourceManifest, error) {
	if tx == nil || lockTarget == nil || validateModelClaimLockPreview(preview) != nil {
		return jobs.JobSourceManifest{}, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(preview.Binding.ProjectID)
	if err != nil {
		return jobs.JobSourceManifest{}, jobs.ErrConflict
	}
	if preview.Binding.Origin == jobs.ModelOriginUser {
		if err := lockExactModelClaimSession(ctx, tx, preview.Binding); err != nil {
			return jobs.JobSourceManifest{}, err
		}
	}
	if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
		return jobs.JobSourceManifest{}, err
	}
	if preview.Binding.Origin == jobs.ModelOriginUser {
		actorID, err := storedb.ParseUUID(preview.Binding.ActorUserID)
		if err != nil {
			return jobs.JobSourceManifest{}, jobs.ErrNotFound
		}
		if _, err := lockJobsMutationMembership(ctx, tx, projectID, actorID); err != nil {
			return jobs.JobSourceManifest{}, err
		}
	}
	return lockTarget(ctx, tx, preview.Subject)
}

func lockExactModelClaimSession(ctx context.Context, tx pgx.Tx, binding jobs.ModelBinding) error {
	if tx == nil || binding.Validate() != nil || binding.Origin != jobs.ModelOriginUser {
		return jobs.ErrValidation
	}
	actorID, err := storedb.ParseUUID(binding.ActorUserID)
	if err != nil {
		return auth.ErrUnauthenticated
	}
	sessionID, err := storedb.ParseUUID(binding.InitiatingAuthSessionID)
	if err != nil {
		return auth.ErrUnauthenticated
	}
	var lockedSessionID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT session.id
		FROM sessions AS session
		JOIN users AS actor ON actor.id = session.user_id
		WHERE session.id = $1
		  AND session.user_id = $2
		  AND session.revoked_at IS NULL
		  AND session.rotated_at IS NULL
		  -- Enrichment is durable; natural expiry is not revocation or rotation.
		  AND (session.expires_at > clock_timestamp() OR $3::text = 'canon_book_enrichment')
		  AND actor.disabled_at IS NULL
		FOR SHARE OF session, actor
	`, sessionID, actorID, string(binding.Kind)).Scan(&lockedSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrUnauthenticated
	}
	return err
}

func lockExactModelClaim(
	ctx context.Context,
	tx pgx.Tx,
	projectID, jobID pgtype.UUID,
	incarnation int64,
	preview jobs.ModelClaimLockPreview,
	expectedManifest jobs.JobSourceManifest,
	expectedStatus jobs.Status,
) (jobs.Job, jobs.ModelBinding, pgtype.UUID, int, error) {
	var ownerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT artifact_source_owner_id
		FROM jobs
		WHERE project_id = $1 AND id = $2
	`, projectID, jobID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	} else if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, err
	}
	if !ownerID.Valid {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	queries := sqlc.New(tx)
	owner, err := queries.LockModelClaimJobOwner(ctx, sqlc.LockModelClaimJobOwnerParams{
		ProjectID: projectID, OwnerID: ownerID, JobID: jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, err
	}
	expected, err := newModelArtifactManifest(uint32(owner.ManifestVersion), governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: preview.Claim.ProjectID, ArtifactID: preview.Claim.JobID,
		OwnerSourceView: expectedManifest.SourceView, Refs: expectedManifest.Refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	expectedHash := expected.Hash()
	if owner.ID != ownerID || !modelSourceViewMatches(owner.SourceView, expectedManifest.SourceView) ||
		owner.ManifestVersion != int32(expected.Version()) ||
		owner.RefCount != int32(expected.RefCount()) || !bytes.Equal(owner.ManifestHash, expectedHash[:]) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}

	locked, err := queries.LockJobForStatusUpdate(ctx, sqlc.LockJobForStatusUpdateParams{
		ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, err
	}
	if locked.ArtifactSourceOwnerID != ownerID || jobs.Kind(locked.Kind) != preview.Binding.Kind ||
		!modelClaimLockStatusMatches(locked, expectedStatus) ||
		locked.RetryCount < 0 || locked.RetryCount >= jobs.MaxJobAttempts {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	job, err := JobFromSQLJob(locked)
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	job.WorkerClaim = preview.Claim
	lockedSubject, err := jobs.EncodeSubject(job.Kind, job.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	previewSubject, err := jobs.EncodeSubject(preview.Binding.Kind, preview.Subject)
	if err != nil || !bytes.Equal(lockedSubject, previewSubject) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}

	lockedBinding, err := queries.LockJobModelBinding(ctx, sqlc.LockJobModelBindingParams{
		ProjectID: projectID, JobID: jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, err
	}
	binding := modelBindingFromSQL(lockedBinding)
	if lockedBinding.ArtifactSourceOwnerID != ownerID || binding.Validate() != nil || binding != preview.Binding {
		return jobs.Job{}, jobs.ModelBinding{}, pgtype.UUID{}, 0, jobs.ErrConflict
	}
	return job, binding, ownerID, int(locked.RetryCount), nil
}

func modelSourceViewMatches(stored pgtype.Text, expected governance.SourceViewArm) bool {
	view, present := expected.View()
	return stored.Valid == present && (!present || stored.String == string(view))
}

func (s *Store) validateExhaustedWorkerClaim(ctx context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	projectID, jobID, incarnation, err := WorkerClaimSQLValues(input.Claim)
	if err != nil {
		return jobs.Job{}, err
	}
	var locked sqlc.Job
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
			return err
		}
		locked, err = sqlc.New(tx).LockJobForStatusUpdate(ctx, sqlc.LockJobForStatusUpdateParams{
			ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation,
		})
		if err != nil {
			return err
		}
		if jobs.Kind(locked.Kind) != input.Kind || jobs.Status(locked.Status) != jobs.StatusRunning ||
			locked.CancelRequestedAt.Valid || int(locked.RetryCount) != input.ExpectedRetryCount {
			return jobs.ErrConflict
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	job, err := JobFromSQLJob(locked)
	if err != nil || job.Subject.Kind() != input.Kind || job.Subject.Project() != input.Claim.ProjectID {
		return jobs.Job{}, jobs.ErrConflict
	}
	job.WorkerClaim = input.Claim
	return job, nil
}
