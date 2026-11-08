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

// projectJobRow is what a Job owned by its project is queued with.
type projectJobRow struct {
	jobID, projectID, creatorID, ownerID pgtype.UUID
}

// createProjectJobInTx queues a Job whose source owner names only the project: the fence of a book or a chapter
// selects owners by their references, so a deleted book leaves it, and deleting the project removes it.
func (s *Store) createProjectJobInTx(
	ctx context.Context, tx pgx.Tx, project, job, creator string,
	insert func(*sqlc.Queries, projectJobRow) (sqlc.Job, error),
) (jobs.Job, error) {
	projectID, err := storedb.ParseUUID(project)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	creatorID, err := storedb.ParseUUID(creator)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(job)
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
		ProjectID: project, ProjectGeneration: governance.LifecycleGeneration(generation),
	}, governance.AbsentChatArm())
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	refs := []governance.ArtifactSourceRef{projectRef}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: project, ArtifactID: job,
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
		OwnerID: ownerID, ProjectID: project, ArtifactID: job,
		Kind: governance.ArtifactJob, SourceView: governance.AbsentSourceView(), Refs: refs, Manifest: manifest,
	}, func(ctx context.Context, tx pgx.Tx, artifactOwnerID pgtype.UUID) error {
		row, err := insert(sqlc.New(tx), projectJobRow{jobID: jobID, projectID: projectID, creatorID: creatorID, ownerID: artifactOwnerID})
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

// AuthorMaterialsImportJobInput is the Job of an import of the author.
type AuthorMaterialsImportJobInput struct {
	JobID         string
	Subject       jobs.AuthorMaterialsImportSubject
	CreatorUserID string
	CreatedAt     time.Time
}

// CreateAuthorMaterialsImportJobInTx queues the Job of an import in the caller's transaction
// (specs/2026-11-04-saga-map.md, R9.2): the author asked for it and may stop it.
func (s *Store) CreateAuthorMaterialsImportJobInTx(ctx context.Context, tx pgx.Tx, input AuthorMaterialsImportJobInput) (jobs.Job, error) {
	if s == nil || tx == nil || input.CreatedAt.IsZero() || input.Subject.Type != jobs.KindAuthorMaterialsImport {
		return jobs.Job{}, jobs.ErrValidation
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindAuthorMaterialsImport, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	return s.createProjectJobInTx(ctx, tx, input.Subject.ProjectID, input.JobID, input.CreatorUserID,
		func(queries *sqlc.Queries, row projectJobRow) (sqlc.Job, error) {
			return queries.CreateAuthorMaterialsImportJob(ctx, sqlc.CreateAuthorMaterialsImportJobParams{
				ID: row.jobID, SubjectJson: subjectJSON, CreatorUserID: row.creatorID,
				CreatedAt: storedb.Timestamptz(input.CreatedAt), ArtifactSourceOwnerID: row.ownerID, ProjectID: row.projectID,
			})
		})
}

// ClaimedJobEnd is the terminal write of a Job of its own repository, under its claim.
type ClaimedJobEnd struct {
	Kind             jobs.Kind
	ProjectID, JobID pgtype.UUID
	From, To         jobs.Status
	ClaimIncarnation int64
	ResultJSON       []byte
	ErrorJSON        []byte
}

// EndClaimedJobInTx ends a Job under its claim and seals its source owner in the caller's transaction; before writes what
// the Job made while both are locked. jobs.ErrConflict when the claim no longer holds the Job.
func EndClaimedJobInTx(ctx context.Context, tx pgx.Tx, end ClaimedJobEnd, before func() error) error {
	var owner pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT artifact_source_owner_id FROM jobs
		WHERE project_id = $1 AND id = $2 AND kind = $3 AND status = $4 AND claim_incarnation = $5
		  AND governance_payload_state = 'active'
		FOR UPDATE
	`, end.ProjectID, end.JobID, string(end.Kind), string(end.From), end.ClaimIncarnation).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	var seal sqlc.SealArtifactSourceOwnerParams
	err = tx.QueryRow(ctx, `
		SELECT source_view, manifest_version, manifest_hash, ref_count, source_identity_state
		FROM artifact_source_owners
		WHERE id = $1 AND project_id = $2 AND artifact_kind = 'job' AND artifact_id = $3
		  AND read_state = 'active' AND fenced_at IS NULL
		FOR UPDATE
	`, owner, end.ProjectID, end.JobID).Scan(&seal.SourceView, &seal.ManifestVersion, &seal.ManifestHash, &seal.RefCount, &seal.SourceIdentityState)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	if err := before(); err != nil {
		return err
	}
	ended, err := tx.Exec(ctx, `
		WITH terminal_clock AS (SELECT date_trunc('microseconds', clock_timestamp()) AS value)
		UPDATE jobs
		SET status = $1, result_json = $2::jsonb, error_json = $3::jsonb,
		    claimed_at = NULL, completed_at = terminal_clock.value, updated_at = terminal_clock.value
		FROM terminal_clock
		WHERE project_id = $4 AND id = $5 AND kind = $6
		  AND status = $7 AND claim_incarnation = $8 AND artifact_source_owner_id = $9
	`, string(end.To), end.ResultJSON, end.ErrorJSON, end.ProjectID, end.JobID, string(end.Kind), string(end.From), end.ClaimIncarnation, owner)
	if err != nil {
		return err
	}
	if ended.RowsAffected() != 1 {
		return jobs.ErrConflict
	}
	seal.OwnerID, seal.ProjectID, seal.ArtifactKind, seal.ArtifactID = owner, end.ProjectID, string(governance.ArtifactJob), end.JobID
	_, err = sqlc.New(tx).SealArtifactSourceOwner(ctx, seal)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	return err
}
