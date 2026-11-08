package jobstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type lockedCanonTextIndexIntent struct {
	job         jobs.Job
	owner       sqlc.LockModelClaimJobOwnerRow
	coalescible bool
}

func (s *Store) UpsertCanonTextIndexIntent(
	ctx context.Context,
	tx pgx.Tx,
	input jobs.CanonTextIndexIntentInput,
) (jobs.Job, bool, error) {
	if tx == nil {
		return jobs.Job{}, false, jobs.ErrValidation
	}
	var err error
	input, err = completeCanonTextIndexIntentInput(input)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if input.Validate() != nil {
		return jobs.Job{}, false, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.Subject.ProjectID)
	if err != nil {
		return jobs.Job{}, false, jobs.ErrValidation
	}
	creatorUserID, err := optionalUUIDValue(input.CreatorUserID)
	if err != nil {
		return jobs.Job{}, false, jobs.ErrValidation
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, input.Subject)
	if err != nil {
		return jobs.Job{}, false, jobs.ErrValidation
	}
	// Блокировка удерживает жизненный цикл проекта, а уникальный индекс — одну последующую задачу в очереди.
	if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
		return jobs.Job{}, false, err
	}
	view := input.Subject.SourceView
	if err := tombstoneUnreadableViewIntents(ctx, tx, jobs.KindCanonTextIndex, projectID, view); err != nil {
		return jobs.Job{}, false, err
	}

	writer := s.artifactSourcesStore(s.textIndexIntentFailpoint)
	queries := sqlc.New(tx)
	locked, found, err := lockCanonTextIndexIntent(ctx, queries, projectID, input.Source.SourceView)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if found {
		if locked.coalescible {
			job, err := updateCanonTextIndexIntent(ctx, tx, queries, writer, locked, input, creatorUserID)
			if err != nil {
				return jobs.Job{}, false, err
			}
			if err := supersedeCanonTextIndexPredecessors(ctx, queries, job, input.CreatedAt); err != nil {
				return jobs.Job{}, false, err
			}
			return job, true, nil
		}
		current := locked.job.Subject.(jobs.CanonTextIndexSubject)
		if current.CatalogRevision >= input.Subject.CatalogRevision {
			return locked.job, true, nil
		}
	} else if !input.CreateIfMissing {
		return jobs.Job{}, false, nil
	}

	candidate, err := tx.Begin(ctx)
	if err != nil {
		return jobs.Job{}, false, err
	}
	created, createErr := createCanonTextIndexIntent(ctx, candidate, writer, input, subjectJSON, creatorUserID)
	if createErr == nil {
		if err := candidate.Commit(ctx); err != nil {
			return jobs.Job{}, false, err
		}
		if err := supersedeCanonTextIndexPredecessors(ctx, queries, created, input.CreatedAt); err != nil {
			return jobs.Job{}, false, err
		}
		return created, true, nil
	}
	_ = candidate.Rollback(ctx)
	if !isQueuedCanonTextIndexIntentUniqueViolation(createErr) {
		return jobs.Job{}, false, mapCanonTextIndexIntentError(createErr)
	}

	locked, found, err = lockCanonTextIndexIntent(ctx, queries, projectID, input.Source.SourceView)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if !found || !locked.coalescible {
		return jobs.Job{}, false, jobs.ErrConflict
	}
	job, err := updateCanonTextIndexIntent(ctx, tx, queries, writer, locked, input, creatorUserID)
	if err != nil {
		return jobs.Job{}, false, err
	}
	if err := supersedeCanonTextIndexPredecessors(ctx, queries, job, input.CreatedAt); err != nil {
		return jobs.Job{}, false, err
	}
	return job, true, nil
}

func completeCanonTextIndexIntentInput(input jobs.CanonTextIndexIntentInput) (jobs.CanonTextIndexIntentInput, error) {
	if input.JobID == "" {
		jobID, err := newUUIDValue()
		if err != nil {
			return jobs.CanonTextIndexIntentInput{}, err
		}
		input.JobID = jobID.String()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	return input, nil
}

func supersedeCanonTextIndexPredecessors(
	ctx context.Context,
	queries *sqlc.Queries,
	successor jobs.Job,
	updatedAt time.Time,
) error {
	subject, ok := successor.Subject.(jobs.CanonTextIndexSubject)
	if !ok {
		return jobs.ErrConflict
	}
	projectID, err := storedb.ParseUUID(successor.ProjectID)
	if err != nil {
		return jobs.ErrConflict
	}
	jobID, err := storedb.ParseUUID(successor.ID)
	if err != nil {
		return jobs.ErrConflict
	}
	// Единственная актуальная queued-задача остаётся преемником; более старые
	// задачи больше не должны успешно завершиться или попасть на повтор.
	_, err = queries.SupersedeCanonTextIndexPredecessors(ctx, sqlc.SupersedeCanonTextIndexPredecessorsParams{
		UpdatedAt:       storedb.Timestamptz(updatedAt),
		ProjectID:       projectID,
		SuccessorJobID:  jobID,
		SourceView:      string(subject.SourceView),
		CatalogRevision: int64(subject.CatalogRevision),
	})
	return err
}

func lockCanonTextIndexIntent(
	ctx context.Context,
	queries *sqlc.Queries,
	projectID pgtype.UUID,
	sourceView governance.SourceViewArm,
) (lockedCanonTextIndexIntent, bool, error) {
	view, present := sourceView.View()
	if !present {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrValidation
	}
	row, err := queries.LockCanonTextIndexActiveWork(ctx, sqlc.LockCanonTextIndexActiveWorkParams{
		ProjectID: projectID, SourceView: string(view),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedCanonTextIndexIntent{}, false, nil
	}
	if err != nil {
		return lockedCanonTextIndexIntent{}, false, err
	}
	job, err := JobFromSQLJob(row)
	if err != nil || job.Kind != jobs.KindCanonTextIndex || !job.CanCancel ||
		job.QueueClass != jobs.QueueClassUserDemanded {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrConflict
	}
	subject, ok := job.Subject.(jobs.CanonTextIndexSubject)
	if !ok || subject.ProjectID != job.ProjectID || subject.SourceView != view {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrConflict
	}
	coalescible := job.Status == jobs.StatusQueued && row.ClaimIncarnation == 0 && !row.CancelRequestedAt.Valid
	if !coalescible {
		return lockedCanonTextIndexIntent{job: job}, true, nil
	}
	if !row.ArtifactSourceOwnerID.Valid || job.RetryCount != 0 || job.MaxAttempts != jobs.MaxJobAttempts ||
		job.Progress != nil || job.Result != nil || job.Error != nil {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrConflict
	}
	owner, err := queries.LockModelClaimJobOwner(ctx, sqlc.LockModelClaimJobOwnerParams{
		ProjectID: projectID, OwnerID: row.ArtifactSourceOwnerID, JobID: row.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrConflict
	}
	if err != nil {
		return lockedCanonTextIndexIntent{}, false, err
	}
	if owner.ID != row.ArtifactSourceOwnerID || !modelSourceViewMatches(owner.SourceView, sourceView) ||
		owner.ManifestVersion != 1 || len(owner.ManifestHash) != 32 || owner.RefCount < 1 {
		return lockedCanonTextIndexIntent{}, false, jobs.ErrConflict
	}
	return lockedCanonTextIndexIntent{job: job, owner: owner, coalescible: true}, true, nil
}

type unreadableViewIntent struct {
	jobID   pgtype.UUID
	ownerID pgtype.UUID
}

// tombstoneUnreadableViewIntents expires the text index or cards Jobs of a project text
// version whose owner lost source access.
func tombstoneUnreadableViewIntents(
	ctx context.Context,
	tx pgx.Tx,
	kind jobs.Kind,
	projectID pgtype.UUID,
	sourceView governance.SourceView,
) error {
	// После смены поколения проекта прежняя задача остаётся в БД, но больше не имеет
	// доступа к источнику. Закрываем её под блокировкой проекта до поиска актуальной задачи.
	rows, err := tx.Query(ctx, `
		SELECT job.id, owner.id
		FROM jobs AS job
		JOIN artifact_source_owners AS owner
		  ON owner.id = job.artifact_source_owner_id
		 AND owner.project_id = job.project_id
		 AND owner.artifact_kind = 'job'
		 AND owner.artifact_id = job.id
		WHERE job.project_id = $1
		  AND job.kind = $3
		  AND job.status IN ('queued', 'running', 'canceling')
		  AND job.subject_json->>'type' = $3
		  AND job.subject_json->>'projectId' = job.project_id::text
		  AND job.subject_json->>'sourceView' = $2
		  AND NOT ck_artifact_source_owner_is_readable(
		    owner.id, owner.project_id, 'job', owner.artifact_id
		  )
		ORDER BY job.id
		FOR UPDATE OF job, owner
	`, projectID, string(sourceView), string(kind))
	if err != nil {
		return err
	}
	intents := make([]unreadableViewIntent, 0, 2)
	for rows.Next() {
		var intent unreadableViewIntent
		if err := rows.Scan(&intent.jobID, &intent.ownerID); err != nil {
			rows.Close()
			return err
		}
		if !intent.jobID.Valid || !intent.ownerID.Valid {
			rows.Close()
			return jobs.ErrConflict
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, intent := range intents {
		updated, err := tx.Exec(ctx, `
			UPDATE jobs
			SET status = 'expired', progress_json = '{}'::jsonb, subject_json = NULL,
			    creator_user_id = NULL, can_cancel = false, result_json = NULL,
			    error_json = NULL, claimed_at = NULL, cancel_requested_at = NULL,
			    artifact_source_owner_id = NULL, governance_payload_state = 'source_deleted',
			    expires_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE project_id = $1 AND id = $2 AND kind = $4
			  AND artifact_source_owner_id = $3
			  AND status IN ('queued', 'running', 'canceling')
		`, projectID, intent.jobID, intent.ownerID, string(kind))
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return jobs.ErrConflict
		}
		if err := deleteLockedJobOwner(ctx, tx, projectID, intent.jobID, intent.ownerID); err != nil {
			return err
		}
	}
	return nil
}

func updateCanonTextIndexIntent(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
	writer *governancestore.Store,
	locked lockedCanonTextIndexIntent,
	input jobs.CanonTextIndexIntentInput,
	creatorUserID pgtype.UUID,
) (jobs.Job, error) {
	current := locked.job.Subject.(jobs.CanonTextIndexSubject)
	replaceSources := current.CatalogRevision < input.Subject.CatalogRevision
	if current.CatalogRevision > input.Subject.CatalogRevision {
		input.Subject.CatalogRevision = current.CatalogRevision
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	ownerID := locked.owner.ID
	projectID, err := storedb.ParseUUID(locked.job.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	jobID, err := storedb.ParseUUID(locked.job.ID)
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	if !replaceSources {
		if locked.job.CreatorUserID != "" || !creatorUserID.Valid {
			return locked.job, nil
		}
		row, err := queries.AdoptQueuedCanonTextIndexIntentCreator(ctx, sqlc.AdoptQueuedCanonTextIndexIntentCreatorParams{
			CreatorUserID: creatorUserID, UpdatedAt: storedb.Timestamptz(input.CreatedAt),
			ProjectID: projectID, JobID: jobID, ArtifactSourceOwnerID: ownerID,
		})
		if err != nil {
			return jobs.Job{}, mapCanonTextIndexIntentQueryError(err)
		}
		return JobFromSQLJob(row)
	}

	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: input.Subject.ProjectID, ArtifactID: locked.job.ID,
		OwnerSourceView: input.Source.SourceView, Refs: input.Source.Refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	var updated jobs.Job
	err = writer.ReplaceArtifactSourcesInTx(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: locked.owner.ID.String(), ProjectID: input.Subject.ProjectID, ArtifactID: locked.job.ID,
		Kind: governance.ArtifactJob, SourceView: input.Source.SourceView, Refs: input.Source.Refs, Manifest: manifest,
	}, governancestore.ExpectedArtifactSourcesManifest{
		Version: locked.owner.ManifestVersion, Hash: locked.owner.ManifestHash, RefCount: locked.owner.RefCount,
	}, func(ctx context.Context, tx pgx.Tx, artifactOwnerID string) error {
		row, err := sqlc.New(tx).UpdateQueuedCanonTextIndexIntent(ctx, sqlc.UpdateQueuedCanonTextIndexIntentParams{
			SubjectJson: subjectJSON, CreatorUserID: creatorUserID, UpdatedAt: storedb.Timestamptz(input.CreatedAt),
			ProjectID: projectID, JobID: jobID, ArtifactSourceOwnerID: ownerID,
		})
		if err != nil {
			return err
		}
		if artifactOwnerID != locked.owner.ID.String() {
			return jobs.ErrConflict
		}
		updated, err = JobFromSQLJob(row)
		return err
	})
	if err != nil {
		return jobs.Job{}, mapCanonTextIndexIntentError(err)
	}
	return updated, nil
}

func createCanonTextIndexIntent(
	ctx context.Context,
	tx pgx.Tx,
	writer *governancestore.Store,
	input jobs.CanonTextIndexIntentInput,
	subjectJSON []byte,
	creatorUserID pgtype.UUID,
) (jobs.Job, error) {
	ownerID, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return jobs.Job{}, err
	}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: input.Subject.ProjectID, ArtifactID: input.JobID,
		OwnerSourceView: input.Source.SourceView, Refs: input.Source.Refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	var created jobs.Job
	err = writer.CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: input.Subject.ProjectID, ArtifactID: input.JobID,
		Kind: governance.ArtifactJob, SourceView: input.Source.SourceView, Refs: input.Source.Refs, Manifest: manifest,
	}, func(ctx context.Context, tx pgx.Tx, artifactOwnerID pgtype.UUID) error {
		jobID, err := storedb.ParseUUID(input.JobID)
		if err != nil {
			return jobs.ErrValidation
		}
		projectID, err := storedb.ParseUUID(input.Subject.ProjectID)
		if err != nil {
			return jobs.ErrValidation
		}
		row, err := sqlc.New(tx).CreateCanonTextIndexIntent(ctx, sqlc.CreateCanonTextIndexIntentParams{
			ID: jobID, SubjectJson: subjectJSON, CreatorUserID: creatorUserID,
			CreatedAt: storedb.Timestamptz(input.CreatedAt), ArtifactSourceOwnerID: artifactOwnerID,
			CreatorRequestID: storedb.CreatorRequestID(ctx), ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		created, err = JobFromSQLJob(row)
		return err
	})
	return created, err
}

func isQueuedCanonTextIndexIntentUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505" &&
		pgError.ConstraintName == "jobs_one_queued_text_successor_idx"
}

func mapCanonTextIndexIntentQueryError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	return err
}

func mapCanonTextIndexIntentError(err error) error {
	if errors.Is(err, governancestore.ErrArtifactSourcesValidation) || errors.Is(err, jobs.ErrValidation) {
		return jobs.ErrValidation
	}
	if errors.Is(err, governancestore.ErrArtifactSourcesConflict) || errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	return err
}
