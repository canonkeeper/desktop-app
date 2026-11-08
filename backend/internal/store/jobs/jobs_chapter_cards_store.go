package jobstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// UpsertChapterCardsIntent queues the cards Job of one project text version in the caller's
// transaction (UC2 step 1). A Job already waiting for the version builds whatever is current
// when it runs, so it is reused instead of queueing a second one.
func (s *Store) UpsertChapterCardsIntent(ctx context.Context, tx pgx.Tx, input jobs.ChapterCardsIntentInput) (jobs.Job, error) {
	if tx == nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	if input.JobID == "" {
		jobID, err := newUUIDValue()
		if err != nil {
			return jobs.Job{}, err
		}
		input.JobID = jobID.String()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.Validate() != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindChapterCards, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	return s.upsertAutomaticIntent(ctx, tx, automaticIntent{
		kind: jobs.KindChapterCards, jobID: input.JobID, projectID: input.Subject.ProjectID, view: input.Subject.SourceView,
		source: input.Source, createdAt: input.CreatedAt,
		lockQueued: func(queries *sqlc.Queries, projectID pgtype.UUID) (sqlc.Job, error) {
			return queries.LockQueuedChapterCardsIntent(ctx, sqlc.LockQueuedChapterCardsIntentParams{
				ProjectID: projectID, SourceView: string(input.Subject.SourceView),
			})
		},
		create: func(queries *sqlc.Queries, jobID, projectID, owner pgtype.UUID) (sqlc.Job, error) {
			return queries.CreateChapterCardsIntent(ctx, sqlc.CreateChapterCardsIntentParams{
				ID: jobID, SubjectJson: subjectJSON, CreatedAt: storedb.Timestamptz(input.CreatedAt),
				ArtifactSourceOwnerID: owner, ProjectID: projectID,
			})
		},
	})
}

// UpsertSagaLayerIntent queues the thread layer Job of one project in the caller's transaction
// (specs/2026-11-01-writer-assistant-index.md, R2.1): a Job already waiting is reused, one per project.
func (s *Store) UpsertSagaLayerIntent(ctx context.Context, tx pgx.Tx, input jobs.SagaLayerIntentInput) (jobs.Job, error) {
	if tx == nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	if input.JobID == "" {
		jobID, err := newUUIDValue()
		if err != nil {
			return jobs.Job{}, err
		}
		input.JobID = jobID.String()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.Validate() != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindSagaLayer, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	return s.upsertAutomaticIntent(ctx, tx, automaticIntent{
		kind: jobs.KindSagaLayer, jobID: input.JobID, projectID: input.Subject.ProjectID, view: input.Subject.SourceView,
		source: input.Source, createdAt: input.CreatedAt,
		lockQueued: func(queries *sqlc.Queries, projectID pgtype.UUID) (sqlc.Job, error) {
			return queries.LockQueuedSagaLayerIntent(ctx, projectID)
		},
		create: func(queries *sqlc.Queries, jobID, projectID, owner pgtype.UUID) (sqlc.Job, error) {
			return queries.CreateSagaLayerIntent(ctx, sqlc.CreateSagaLayerIntentParams{
				ID: jobID, SubjectJson: subjectJSON, CreatedAt: storedb.Timestamptz(input.CreatedAt),
				ArtifactSourceOwnerID: owner, ProjectID: projectID,
			})
		},
	})
}

// automaticIntent is a Job the system queues for a project text version.
type automaticIntent struct {
	kind       jobs.Kind
	jobID      string
	projectID  string
	view       governance.SourceView
	source     jobs.JobSourceManifest
	createdAt  time.Time
	lockQueued func(*sqlc.Queries, pgtype.UUID) (sqlc.Job, error)
	create     func(queries *sqlc.Queries, jobID, projectID, owner pgtype.UUID) (sqlc.Job, error)
}

func (s *Store) upsertAutomaticIntent(ctx context.Context, tx pgx.Tx, intent automaticIntent) (jobs.Job, error) {
	projectID, err := storedb.ParseUUID(intent.projectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(intent.jobID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
		return jobs.Job{}, err
	}
	if err := tombstoneUnreadableViewIntents(ctx, tx, intent.kind, projectID, intent.view); err != nil {
		return jobs.Job{}, err
	}
	queued, err := intent.lockQueued(sqlc.New(tx), projectID)
	if err == nil {
		return JobFromSQLJob(queued)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, err
	}

	ownerID, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return jobs.Job{}, err
	}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: intent.projectID, ArtifactID: intent.jobID,
		OwnerSourceView: intent.source.SourceView, Refs: intent.source.Refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	var created jobs.Job
	err = s.artifactSourcesStore(nil).CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: intent.projectID, ArtifactID: intent.jobID,
		Kind: governance.ArtifactJob, SourceView: intent.source.SourceView, Refs: intent.source.Refs, Manifest: manifest,
	}, func(ctx context.Context, tx pgx.Tx, artifactOwnerID pgtype.UUID) error {
		row, err := intent.create(sqlc.New(tx), jobID, projectID, artifactOwnerID)
		if err != nil {
			return err
		}
		created, err = JobFromSQLJob(row)
		return err
	})
	if err != nil {
		return jobs.Job{}, mapCanonTextIndexIntentError(err)
	}
	return created, nil
}
