package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
)

type textIndexIntentWriteMode bool

const (
	refreshTextIndexIntent textIndexIntentWriteMode = false
	ensureTextIndexIntent  textIndexIntentWriteMode = true
)

func (s *Store) upsertCanonTextIndexIntent(
	ctx context.Context,
	tx pgx.Tx,
	revision library.SourceCatalogRevision,
	mode textIndexIntentWriteMode,
) error {
	if tx == nil || revision.Validate() != nil {
		return library.ErrValidation
	}
	view, err := governance.PresentSourceView(revision.SourceView)
	if err != nil {
		return library.ErrValidation
	}
	projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID:         revision.ProjectID,
		ProjectGeneration: governance.LifecycleGeneration(revision.ProjectLifecycleGeneration),
	}, view, governance.AbsentChatArm())
	if err != nil {
		return library.ErrValidation
	}
	createIfMissing := mode == ensureTextIndexIntent
	_, present, err := s.jobs.UpsertCanonTextIndexIntent(ctx, tx, jobs.CanonTextIndexIntentInput{
		CreateIfMissing: createIfMissing,
		Subject: jobs.CanonTextIndexSubject{
			Type:            jobs.KindCanonTextIndex,
			ProjectID:       revision.ProjectID,
			SourceView:      revision.SourceView,
			CatalogRevision: revision.CatalogRevision,
		},
		Source: jobs.JobSourceManifest{
			SourceView: view,
			Refs:       []governance.ArtifactSourceRef{projectRef},
		},
	})
	if errors.Is(err, jobs.ErrValidation) {
		return library.ErrValidation
	}
	if errors.Is(err, jobs.ErrConflict) || (err == nil && createIfMissing && !present) {
		return library.ErrConflict
	}
	return err
}
