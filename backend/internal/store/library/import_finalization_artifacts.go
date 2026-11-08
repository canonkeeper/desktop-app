package librarystore

import (
	"context"
	"errors"
	"fmt"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
)

type importArtifactOwnerSet struct {
	jobOwnerID       string
	requestOwnerID   string
	uploadOwnerID    string
	uploadArtifactID string
}

func (s *Store) replaceImportArtifactSources(
	ctx context.Context,
	tx pgx.Tx,
	projectID string,
	projectGeneration governance.LifecycleGeneration,
	jobID string,
	claimIncarnation int64,
	storageKey string,
	owners importArtifactOwnerSet,
	producedRefs []governance.ArtifactSourceRef,
	view governance.SourceViewArm,
	hit func(string) error,
) error {
	if tx == nil || projectGeneration == 0 || claimIncarnation < 1 || len(producedRefs) == 0 {
		return library.ErrValidation
	}
	projectRef, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: projectGeneration,
	}, governance.AbsentChatArm())
	if err != nil {
		return library.ErrValidation
	}
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return library.ErrNotFound
	}
	jobUUID, err := storedb.ParseUUID(jobID)
	if err != nil {
		return library.ErrConflict
	}
	uploadArtifactUUID, err := storedb.ParseUUID(owners.uploadArtifactID)
	if err != nil {
		return library.ErrConflict
	}

	replacements := []struct {
		kind       governance.ArtifactKind
		artifactID string
		ownerID    string
		guard      governancestore.ArtifactSourceMutation
	}{
		{
			kind: governance.ArtifactJob, artifactID: jobID, ownerID: owners.jobOwnerID,
			guard: func(ctx context.Context, tx pgx.Tx, ownerID string) error {
				ownerUUID, err := storedb.ParseUUID(ownerID)
				if err != nil {
					return library.ErrConflict
				}
				var exists bool
				err = tx.QueryRow(ctx, `
					SELECT true
					FROM jobs
					WHERE id = $1 AND project_id = $2 AND kind = 'import'
					  AND status = 'running' AND claim_incarnation = $3
					  AND cancel_requested_at IS NULL
					  AND artifact_source_owner_id = $4
					FOR UPDATE
				`, jobUUID, projectUUID, claimIncarnation, ownerUUID).Scan(&exists)
				return mapImportArtifactGuardError(err)
			},
		},
		{
			kind: governance.ArtifactImportJobRequest, artifactID: jobID, ownerID: owners.requestOwnerID,
			guard: func(ctx context.Context, tx pgx.Tx, ownerID string) error {
				ownerUUID, err := storedb.ParseUUID(ownerID)
				if err != nil {
					return library.ErrConflict
				}
				var exists bool
				err = tx.QueryRow(ctx, `
					SELECT true
					FROM import_job_requests
					WHERE job_id = $1 AND project_id = $2 AND storage_key = $3
					  AND artifact_source_owner_id = $4
					FOR UPDATE
				`, jobUUID, projectUUID, storageKey, ownerUUID).Scan(&exists)
				return mapImportArtifactGuardError(err)
			},
		},
		{
			kind: governance.ArtifactImportUpload, artifactID: owners.uploadArtifactID, ownerID: owners.uploadOwnerID,
			guard: func(ctx context.Context, tx pgx.Tx, ownerID string) error {
				ownerUUID, err := storedb.ParseUUID(ownerID)
				if err != nil {
					return library.ErrConflict
				}
				var exists bool
				err = tx.QueryRow(ctx, `
					SELECT true
					FROM import_uploads
					WHERE artifact_id = $1 AND project_id = $2 AND storage_key = $3
					  AND artifact_source_owner_id = $4
					  AND content IS NOT NULL AND clock_timestamp() < expires_at
					FOR UPDATE
				`, uploadArtifactUUID, projectUUID, storageKey, ownerUUID).Scan(&exists)
				return mapImportArtifactGuardError(err)
			},
		},
	}

	for _, replacement := range replacements {
		writer := s.artifactSourcesStore(func(point string) error {
			return hitImportFinalizeStep(
				hit,
				fmt.Sprintf("after_import_source_replace_%s_%s", replacement.kind, point),
			)
		})
		expectedManifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
			Kind: replacement.kind, ProjectID: projectID, ArtifactID: replacement.artifactID,
			OwnerSourceView: governance.AbsentSourceView(), Refs: []governance.ArtifactSourceRef{projectRef},
		})
		if err != nil {
			return library.ErrValidation
		}
		manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
			Kind: replacement.kind, ProjectID: projectID, ArtifactID: replacement.artifactID,
			OwnerSourceView: view, Refs: producedRefs,
		})
		if err != nil {
			return library.ErrValidation
		}
		err = writer.ReplaceArtifactSources(ctx, tx, governancestore.ReplaceArtifactSourcesInput{
			SealArtifactSourcesInput: governancestore.SealArtifactSourcesInput{
				OwnerID: replacement.ownerID, ProjectID: projectID, ArtifactID: replacement.artifactID,
				Kind: replacement.kind, SourceView: view, Refs: producedRefs, Manifest: manifest,
			},
			ExpectedManifest: expectedManifest,
		}, replacement.guard)
		if errors.Is(err, governancestore.ErrArtifactSourcesConflict) || errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
			return library.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("replace import artifact sources: %w", err)
		}
	}
	return nil
}

func hitImportFinalizeStep(hit func(string) error, point string) error {
	if hit == nil {
		return nil
	}
	return hit(point)
}

func mapImportArtifactGuardError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ErrConflict
	}
	return err
}

func mustDraftSourceView() governance.SourceViewArm {
	view, err := governance.PresentSourceView(governance.SourceView("draft"))
	if err != nil {
		panic("store: draft source view is invalid")
	}
	return view
}
