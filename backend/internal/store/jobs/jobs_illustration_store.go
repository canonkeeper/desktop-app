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

// IllustrationJobInput is the Job drawing one image of the saga gallery.
type IllustrationJobInput struct {
	JobID         string
	Subject       jobs.IllustrationSubject
	CreatorUserID string
	CreatedAt     time.Time
	// StartsAt is when the Job may start (specs/2026-11-12-ux10-drawing-ops.md DO2.1): zero — at once; later — a drawing
	// put off until the service of drawings is free, which the claim does not take before it.
	StartsAt time.Time
}

// CreateIllustrationJobInTx queues the Job drawing one image in the caller's transaction
// (specs/2026-10-29-saga-illustrations.md R1.3): an interactive Job of the turn's author that
// nobody cancels. Its source owner names only the project, so deleting a book or a chat leaves
// the drawing alone; deleting the project removes it.
func (s *Store) CreateIllustrationJobInTx(ctx context.Context, tx pgx.Tx, input IllustrationJobInput) (jobs.Job, error) {
	if s == nil || tx == nil || input.CreatedAt.IsZero() || input.Subject.Type != jobs.KindIllustration || input.StartsAt.Before(input.CreatedAt) && !input.StartsAt.IsZero() {
		return jobs.Job{}, jobs.ErrValidation
	}
	startsAt := input.StartsAt
	if startsAt.IsZero() {
		startsAt = input.CreatedAt
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindIllustration, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.Subject.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	creatorID, err := storedb.ParseUUID(input.CreatorUserID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	var generation int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE
	`, projectID).Scan(&generation); errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	} else if err != nil {
		return jobs.Job{}, err
	}
	projectRef, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: input.Subject.ProjectID, ProjectGeneration: governance.LifecycleGeneration(generation),
	}, governance.AbsentChatArm())
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	refs := []governance.ArtifactSourceRef{projectRef}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: input.Subject.ProjectID, ArtifactID: input.JobID,
		OwnerSourceView: governance.AbsentSourceView(), Refs: refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	ownerID, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return jobs.Job{}, err
	}
	var created jobs.Job
	err = s.artifactSourcesStore(nil).CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: input.Subject.ProjectID, ArtifactID: input.JobID,
		Kind: governance.ArtifactJob, SourceView: governance.AbsentSourceView(), Refs: refs, Manifest: manifest,
	}, func(ctx context.Context, tx pgx.Tx, artifactOwnerID pgtype.UUID) error {
		row, err := sqlc.New(tx).CreateIllustrationJob(ctx, sqlc.CreateIllustrationJobParams{
			ID: jobID, SubjectJson: subjectJSON, CreatorUserID: creatorID, NextAttemptAt: storedb.Timestamptz(startsAt),
			CreatedAt: storedb.Timestamptz(input.CreatedAt), ArtifactSourceOwnerID: artifactOwnerID, ProjectID: projectID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrNotFound
		}
		if err != nil {
			return err
		}
		created, err = JobFromSQLJob(row)
		return err
	})
	if err != nil {
		return jobs.Job{}, err
	}
	return created, nil
}
