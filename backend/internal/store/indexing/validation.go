package indexingstore

import (
	"context"
	"errors"
	"math"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func textIndexClaimIDs(claim jobs.WorkerClaim) (pgtype.UUID, pgtype.UUID, error) {
	if claim.Validate() != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrTextIndexValidation
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrTextIndexValidation
	}
	jobID, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrTextIndexValidation
	}
	return projectID, jobID, nil
}

func validateClaimedCanonTextJob(
	ctx context.Context,
	queries *sqlc.Queries,
	claim jobs.WorkerClaim,
	wantView governance.SourceView,
	wantCatalog uint64,
) (sqlc.GetClaimedCanonTextIndexJobRow, error) {
	projectID, jobID, err := textIndexClaimIDs(claim)
	if err != nil {
		return sqlc.GetClaimedCanonTextIndexJobRow{}, err
	}
	row, err := queries.GetClaimedCanonTextIndexJob(ctx, sqlc.GetClaimedCanonTextIndexJobParams{
		ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
	})
	if err != nil {
		return sqlc.GetClaimedCanonTextIndexJobRow{}, mapTextIndexError(err)
	}
	if row.SourceView != string(governance.SourceViewDraft) && row.SourceView != string(governance.SourceViewPublished) || row.CatalogRevision < 1 ||
		(wantView != "" && row.SourceView != string(wantView)) || (wantCatalog != 0 && uint64(row.CatalogRevision) != wantCatalog) {
		return sqlc.GetClaimedCanonTextIndexJobRow{}, ErrTextIndexConflict
	}
	return row, nil
}

func validateCanonTextDescriptor(descriptor CanonTextChapterDescriptor) error {
	if descriptor.ProjectLifecycleGeneration < 1 || descriptor.CatalogRevision == 0 || descriptor.CatalogRevision > math.MaxInt64 ||
		descriptor.SourceRevision == 0 || descriptor.SourceRevision > math.MaxInt64 || descriptor.SourceGeneration == (governance.Digest{}) {
		return ErrTextIndexValidation
	}
	if descriptor.SourceView != governance.SourceViewDraft && descriptor.SourceView != governance.SourceViewPublished {
		return ErrTextIndexValidation
	}
	if _, err := storedb.ParseUUID(descriptor.ProjectID); err != nil {
		return ErrTextIndexValidation
	}
	if _, err := storedb.ParseUUID(descriptor.BookID); err != nil {
		return ErrTextIndexValidation
	}
	if _, err := storedb.ParseUUID(descriptor.ChapterID); err != nil {
		return ErrTextIndexValidation
	}
	return nil
}

func mapTextIndexError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, governancestore.ErrArtifactSourcesConflict) || errors.Is(err, jobs.ErrConflict) {
		return ErrTextIndexConflict
	}
	if errors.Is(err, governancestore.ErrArtifactSourcesValidation) || errors.Is(err, jobs.ErrValidation) {
		return ErrTextIndexValidation
	}
	return err
}
