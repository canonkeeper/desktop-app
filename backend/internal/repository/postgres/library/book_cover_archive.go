package library

import (
	"context"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// InsertArchiveCoverInTx makes the cover of a new book of a saga file its first active version, on the transaction of the
// open; its file is among the files of the build already (specs/2026-11-13-desk-sagafile.md SF3.9). The rules of the row
// are those of an upload.
func InsertArchiveCoverInTx(ctx context.Context, tx pgx.Tx, projectID string, bookGeneration int64, input domainlibrary.ActivateCoverInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return domainlibrary.ErrValidation
	}
	_, bookID, artifactID, err := coverInputIDs(input.Actor.UserID, input.BookID, input.ArtifactID)
	if err != nil {
		return err
	}
	_, err = sqlc.New(tx).InsertActiveBookCover(ctx, sqlc.InsertActiveBookCoverParams{
		ID: artifactID, ProjectID: project, BookID: bookID, BookLifecycleGeneration: bookGeneration, Version: 1,
		StorageKey: input.StorageKey, MimeType: input.MIMEType, ByteSize: input.ByteSize,
		Width: int32(input.Width), Height: int32(input.Height), ContentSha256: input.ContentSHA256[:],
		CreatedAt: storedb.Timestamptz(input.At),
	})
	return mapCoverError(err)
}
