package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Repository {
	return &Repository{db: db}
}

func (repository *Repository) ActivateCover(ctx context.Context, input domainlibrary.ActivateCoverInput) (domainlibrary.CoverActivation, error) {
	if err := input.Validate(); err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	actorID, bookID, artifactID, err := coverInputIDs(input.Actor.UserID, input.BookID, input.ArtifactID)
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	var activation domainlibrary.CoverActivation
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		locked, err := queries.LockBookCoverForActor(ctx, sqlc.LockBookCoverForActorParams{
			ActorUserID: actorID,
			BookID:      bookID,
		})
		if err != nil {
			return mapCoverError(err)
		}
		if !canEdit(locked.Role) {
			return domainlibrary.ErrForbidden
		}
		version, err := queries.NextBookCoverVersion(ctx, sqlc.NextBookCoverVersionParams{
			BookID:                  bookID,
			BookLifecycleGeneration: locked.LifecycleGeneration,
		})
		if err != nil {
			return err
		}
		supersededRows, err := queries.SupersedeActiveBookCover(ctx, sqlc.SupersedeActiveBookCoverParams{
			SupersededAt:            storedb.Timestamptz(input.At),
			BookID:                  bookID,
			BookLifecycleGeneration: locked.LifecycleGeneration,
		})
		if err != nil {
			return err
		}
		if len(supersededRows) > 1 {
			return domainlibrary.ErrConflict
		}
		if len(supersededRows) == 1 {
			superseded, err := coverArtifactFromSQL(supersededRows[0])
			if err != nil {
				return err
			}
			activation.Superseded = &superseded
		}
		currentRow, err := queries.InsertActiveBookCover(ctx, sqlc.InsertActiveBookCoverParams{
			ID:                      artifactID,
			ProjectID:               locked.ProjectID,
			BookID:                  bookID,
			BookLifecycleGeneration: locked.LifecycleGeneration,
			Version:                 version,
			StorageKey:              input.StorageKey,
			MimeType:                input.MIMEType,
			ByteSize:                input.ByteSize,
			Width:                   int32(input.Width),
			Height:                  int32(input.Height),
			ContentSha256:           input.ContentSHA256[:],
			CreatedAt:               storedb.Timestamptz(input.At),
		})
		if err != nil {
			return mapCoverError(err)
		}
		activation.Current, err = coverArtifactFromSQL(currentRow)
		return err
	})
	return activation, err
}

func (repository *Repository) FindCover(ctx context.Context, input domainlibrary.CoverLookup) (domainlibrary.CoverArtifact, error) {
	if input.Actor.UserID == "" || input.BookID == "" || input.Version < 1 {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	actorID, err := storedb.ParseUUID(input.Actor.UserID)
	if err != nil {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	row, err := sqlc.New(repository.db.Pool).FindActiveBookCoverForActor(ctx, sqlc.FindActiveBookCoverForActorParams{
		ActorUserID: actorID,
		BookID:      bookID,
		Version:     input.Version,
	})
	if err != nil {
		return domainlibrary.CoverArtifact{}, mapCoverError(err)
	}
	return coverArtifactFromSQL(row.BookCoverArtifact)
}

func (repository *Repository) RemoveCover(ctx context.Context, input domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
	if input.Actor.UserID == "" || input.BookID == "" || input.At.IsZero() {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrValidation
	}
	actorID, err := storedb.ParseUUID(input.Actor.UserID)
	if err != nil {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	var removed domainlibrary.CoverArtifact
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		locked, err := queries.LockBookCoverForActor(ctx, sqlc.LockBookCoverForActorParams{
			ActorUserID: actorID,
			BookID:      bookID,
		})
		if err != nil {
			return mapCoverError(err)
		}
		if !canEdit(locked.Role) {
			return domainlibrary.ErrForbidden
		}
		row, err := queries.RemoveActiveBookCover(ctx, sqlc.RemoveActiveBookCoverParams{
			RemovedAt:               storedb.Timestamptz(input.At),
			BookID:                  bookID,
			BookLifecycleGeneration: locked.LifecycleGeneration,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return domainlibrary.ErrConflict
		}
		if err != nil {
			return mapCoverError(err)
		}
		removed, err = coverArtifactFromSQL(row)
		return err
	})
	return removed, err
}

func (repository *Repository) NextCoverDeletion(ctx context.Context) (domainlibrary.CoverArtifact, error) {
	row, err := sqlc.New(repository.db.Pool).NextBookCoverDeletion(ctx)
	if err != nil {
		return domainlibrary.CoverArtifact{}, mapCoverError(err)
	}
	return coverArtifactFromSQL(row)
}

func (repository *Repository) CompleteCoverDeletion(
	ctx context.Context,
	artifact domainlibrary.CoverArtifact,
	deletedAt time.Time,
) error {
	if deletedAt.IsZero() || artifact.State != domainlibrary.CoverSuperseded && artifact.State != domainlibrary.CoverRemoved {
		return domainlibrary.ErrValidation
	}
	id, err := storedb.ParseUUID(artifact.ID)
	if err != nil {
		return domainlibrary.ErrValidation
	}
	projectID, err := storedb.ParseUUID(artifact.ProjectID)
	if err != nil {
		return domainlibrary.ErrValidation
	}
	bookID, err := storedb.ParseUUID(artifact.BookID)
	if err != nil {
		return domainlibrary.ErrValidation
	}
	_, err = sqlc.New(repository.db.Pool).CompleteBookCoverDeletion(ctx, sqlc.CompleteBookCoverDeletionParams{
		ObjectDeletedAt:         storedb.Timestamptz(deletedAt),
		ID:                      id,
		ProjectID:               projectID,
		BookID:                  bookID,
		BookLifecycleGeneration: artifact.BookLifecycleGeneration,
		Version:                 artifact.Version,
		StorageKey:              artifact.StorageKey,
		State:                   string(artifact.State),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domainlibrary.ErrNotFound
	}
	return mapCoverError(err)
}

func coverInputIDs(actor, book, artifact string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	actorID, err := storedb.ParseUUID(actor)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, domainlibrary.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(book)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, domainlibrary.ErrNotFound
	}
	artifactID, err := storedb.ParseUUID(artifact)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, domainlibrary.ErrValidation
	}
	return actorID, bookID, artifactID, nil
}

func canEdit(role string) bool {
	return role == "editor" || role == "admin" || role == "owner"
}

func coverArtifactFromSQL(row sqlc.BookCoverArtifact) (domainlibrary.CoverArtifact, error) {
	if len(row.ContentSha256) != sha256.Size {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrConflict
	}
	artifact := domainlibrary.CoverArtifact{
		ID: row.ID.String(), ProjectID: row.ProjectID.String(), BookID: row.BookID.String(),
		BookLifecycleGeneration: row.BookLifecycleGeneration, Version: row.Version,
		StorageKey: row.StorageKey, MIMEType: row.MimeType, ByteSize: row.ByteSize,
		Width: int(row.Width), Height: int(row.Height), State: domainlibrary.CoverState(row.State),
		CreatedAt: row.CreatedAt.Time,
	}
	copy(artifact.ContentSHA256[:], row.ContentSha256)
	artifact.ActivatedAt = timePointer(row.ActivatedAt)
	artifact.SupersededAt = timePointer(row.SupersededAt)
	artifact.RemovedAt = timePointer(row.RemovedAt)
	artifact.ObjectDeletedAt = timePointer(row.ObjectDeletedAt)
	return artifact, nil
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func mapCoverError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domainlibrary.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return domainlibrary.ErrConflict
		case "23514", "23503":
			return domainlibrary.ErrValidation
		}
	}
	return err
}
