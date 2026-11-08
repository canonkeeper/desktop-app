package jobstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) createJobForTest(ctx context.Context, input jobs.CreateJobStoreInput) (jobs.Job, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	var created jobs.Job
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
			return err
		}
		projectGeneration, err := currentJobsProjectGeneration(ctx, tx, projectID)
		if err != nil {
			return err
		}
		err = s.createCurrentProjectJobArtifactForTest(
			ctx, tx, input.ProjectID, governance.LifecycleGeneration(projectGeneration), input.ID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				queries := sqlc.New(tx)
				// Тестовый импорт повторяет отдельный рабочий контракт прогресса, а не старое числовое поле.
				if input.Kind == jobs.KindImport {
					jobID, parseErr := storedb.ParseUUID(input.ID)
					if parseErr != nil {
						return parseErr
					}
					creatorID, parseErr := storedb.ParseUUID(input.CreatorUserID)
					if parseErr != nil {
						return parseErr
					}
					progress := jobs.ImportProgress{
						Phase: jobs.ImportPhaseQueued, BytesTransferred: 42, BytesTotal: 42,
						QueuedAt: input.CreatedAt, TransferredAt: input.CreatedAt,
					}
					if input.Status == jobs.StatusSucceeded {
						parsedAt := input.CreatedAt
						savedChapters := 1
						progress.Phase = jobs.ImportPhaseSaved
						progress.ParsingStartedAt = &parsedAt
						progress.ParsedChapters = &savedChapters
						progress.TotalChapters = &savedChapters
						progress.ParsedAt = &parsedAt
						progress.SavingStartedAt = &parsedAt
						progress.SavedChapters = &savedChapters
						progress.SavedAt = &parsedAt
					}
					progressJSON, marshalErr := MarshalImportProgress(progress, input.Status)
					if marshalErr != nil {
						return marshalErr
					}
					row, createErr := queries.CreateImportJobRecord(ctx, sqlc.CreateImportJobRecordParams{
						ID: jobID, Kind: string(input.Kind), Status: string(input.Status),
						ProgressJson: progressJSON, CreatorUserID: creatorID, CanCancel: input.CanCancel,
						CreatedAt: storedb.Timestamptz(input.CreatedAt), UpdatedAt: storedb.Timestamptz(input.UpdatedAt),
						ArtifactSourceOwnerID: ownerID, ProjectID: projectID,
					})
					if createErr != nil {
						return createErr
					}
					created, err = JobFromSQLJob(row)
					return err
				}
				created, err = createJobWithQueries(ctx, queries, input, ownerID)
				return err
			},
		)
		if err != nil {
			if errors.Is(err, governancestore.ErrArtifactSourcesConflict) {
				return jobs.ErrConflict
			}
			return err
		}
		return nil
	})
	return created, err
}

// createCurrentProjectJobArtifactForTest закрепляет Job за текущим поколением
// проекта через штатный CreateArtifactWithSources — тот же путь, что у задачи карточек глав.
func (s *Store) createCurrentProjectJobArtifactForTest(
	ctx context.Context,
	tx pgx.Tx,
	projectID string,
	projectGeneration governance.LifecycleGeneration,
	jobID string,
	mutate governancestore.ArtifactSourceCreateMutation,
) error {
	ref, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: projectGeneration,
	}, governance.AbsentChatArm())
	if err != nil {
		return jobs.ErrValidation
	}
	refs := []governance.ArtifactSourceRef{ref}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: projectID, ArtifactID: jobID,
		OwnerSourceView: governance.AbsentSourceView(), Refs: refs,
	})
	if err != nil {
		return jobs.ErrValidation
	}
	ownerID, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return err
	}
	return s.artifactSourcesStore(nil).CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: projectID, ArtifactID: jobID,
		Kind: governance.ArtifactJob, SourceView: governance.AbsentSourceView(),
		Refs: refs, Manifest: manifest,
	}, mutate)
}

func mustUUID(value string) pgtype.UUID {
	var result pgtype.UUID
	_ = result.Scan(value)
	return result
}

type canonTextIndexRetryFixture struct {
	db        *storedb.DB
	projectID string
	claim     jobs.WorkerClaim
}

func newCanonTextIndexRetryFixture(t *testing.T) canonTextIndexRetryFixture {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000007051"
	projectID := "00000000-0000-4000-8000-000000007052"
	jobID := "00000000-0000-4000-8000-000000007053"
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	insertProjectStoreUserForTest(t, db, actorID, "canon-text-retry@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Canon Text Retry", now)
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(
		governance.SourceGenerationVector{ProjectID: projectID, ProjectGeneration: 1},
		view,
		governance.AbsentChatArm(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, _, err := New(db).UpsertCanonTextIndexIntent(ctx, tx, jobs.CanonTextIndexIntentInput{
			JobID: jobID, CreateIfMissing: true,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID,
				SourceView: governance.SourceViewDraft, CatalogRevision: 1,
			},
			CreatorUserID: actorID,
			Source:        jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
			CreatedAt:     now,
		})
		return err
	}); err != nil {
		t.Fatalf("create canon text retry job: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status='running', claimed_at=$1, claim_incarnation=1, updated_at=$1
		WHERE project_id=$2 AND id=$3
	`, now, projectID, jobID); err != nil {
		t.Fatalf("claim canon text retry job: %v", err)
	}
	return canonTextIndexRetryFixture{
		db: db, projectID: projectID,
		claim: jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: 1},
	}
}

func currentJobsProjectGeneration(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (int64, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation
		FROM projects
		WHERE id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&generation); errors.Is(err, pgx.ErrNoRows) {
		return 0, jobs.ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if generation < 1 {
		return 0, jobs.ErrConflict
	}
	return generation, nil
}
