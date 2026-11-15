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
	"github.com/jackc/pgx/v5/pgconn"
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
		unit: string(input.Subject.SourceView), source: input.Source, createdAt: input.CreatedAt,
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

// CreateChapterCardsSample queues the sample of three chapters of the desktop app in the caller's transaction
// (specs/2026-11-13-desk-consent.md CN2.2): a cards Job of its own — nothing waiting takes it and it takes nothing; one
// sample of a saga lives at a time (jobs_one_live_cards_sample_idx), a second is jobs.ErrConflict.
func (s *Store) CreateChapterCardsSample(ctx context.Context, tx pgx.Tx, input jobs.ChapterCardsIntentInput) (jobs.Job, error) {
	if tx == nil || input.Subject.Sample == nil {
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
	job, err := s.upsertAutomaticIntent(ctx, tx, automaticIntent{
		kind: jobs.KindChapterCards, jobID: input.JobID, projectID: input.Subject.ProjectID, view: input.Subject.SourceView,
		unit: "sample", source: input.Source, createdAt: input.CreatedAt,
		lockQueued: func(*sqlc.Queries, pgtype.UUID) (sqlc.Job, error) { return sqlc.Job{}, pgx.ErrNoRows },
		create: func(queries *sqlc.Queries, jobID, projectID, owner pgtype.UUID) (sqlc.Job, error) {
			return queries.CreateChapterCardsIntent(ctx, sqlc.CreateChapterCardsIntentParams{
				ID: jobID, SubjectJson: subjectJSON, CreatedAt: storedb.Timestamptz(input.CreatedAt),
				ArtifactSourceOwnerID: owner, ProjectID: projectID,
			})
		},
	})
	var unique *pgconn.PgError
	if errors.As(err, &unique) && unique.Code == "23505" {
		return jobs.Job{}, jobs.ErrConflict
	}
	return job, err
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
	kind      jobs.Kind
	jobID     string
	projectID string
	view      governance.SourceView
	// unit names, within the saga, what one waiting Job of the kind lives by, as its lockQueued and its unique index find
	// it: the text version for the cards Job, "sample" for the sample (one live in a saga), none for the layer Job (one
	// waiting in a saga).
	unit       string
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
	// The queues of one unit go one after another, each to the end of the transaction of its caller: the second finds the Job
	// the first queued and reuses it — «such a Job waits already» is the success of a queue, not a refusal of the unique
	// index; the layer Job, without one, gets no second Job. The lock is the unit's and no wider: the ends of the text
	// indexes of both versions of a saga hold its books for share and queue their cards Jobs at once, and a lock of the
	// whole saga made them wait for each other (specs/2026-11-15-desk-reading-queue.md RQ1.1, RQ1.3).
	lock := "canonkeeper automatic job " + string(intent.kind) + " " + storedb.UUIDString(projectID)
	if intent.unit != "" {
		lock += " " + intent.unit
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lock); err != nil {
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
