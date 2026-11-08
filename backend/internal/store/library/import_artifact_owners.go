package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/library"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func lockImportArtifactOwners(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	ownerIDs ...pgtype.UUID,
) error {
	err := governancestore.LockImportArtifactOwnerSet(ctx, tx, projectID, ownerIDs...)
	if errors.Is(err, governancestore.ErrArtifactSourcesConflict) ||
		errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
		return library.ErrConflict
	}
	return err
}
