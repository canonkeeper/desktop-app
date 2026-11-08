package indexingstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ReplaceCanonTextChapterForClaim(
	ctx context.Context,
	input ReplaceCanonTextChapterInput,
) (CanonTextChapterReplaceDisposition, error) {
	if s == nil || s.DB == nil || input.IndexedAt.IsZero() {
		return "", ErrTextIndexValidation
	}
	prepared, err := prepareCanonTextReplacement(input)
	if err != nil {
		return "", err
	}
	readQueries := sqlc.New(s.Pool)
	if _, err := validateClaimedCanonTextJob(ctx, readQueries, input.Snapshot.Claim, input.Snapshot.SourceView, input.Snapshot.CatalogRevision); err != nil {
		return "", err
	}

	// PostgreSQL evaluates the whole chapter here, before a write transaction is
	// opened. The generated search column repeats the same bounded computation.
	document, err := buildCanonChapterDocument(input.Snapshot, "chapter")
	if err != nil {
		return "", ErrTextIndexValidation
	}
	sourceTuple, err := readQueries.GetClaimedCanonTextChapterSourceTuple(ctx, sqlc.GetClaimedCanonTextChapterSourceTupleParams{
		JobID: prepared.jobID, ClaimIncarnation: input.Snapshot.Claim.ClaimIncarnation,
		SourceView: string(input.Snapshot.SourceView), CatalogRevision: int64(input.Snapshot.CatalogRevision),
		BookID: prepared.bookID, ChapterID: prepared.chapterID, SourceRevision: int64(input.Snapshot.SourceRevision),
		ProjectID:                  prepared.projectID,
		ProjectLifecycleGeneration: input.Snapshot.ProjectLifecycleGeneration,
		SourceGeneration:           input.Snapshot.SourceGeneration[:],
		Content:                    document.Content,
	})
	if err != nil {
		return "", mapCanonTextChapterSearchVectorError(mapTextIndexError(err))
	}
	document.Title = sourceTuple.Title
	if err := preflightCanonChapterDocument(document); err != nil {
		return "", err
	}

	disposition := CanonTextChapterReplaced
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		// First: the saga search rows of this chapter are written by one transaction at a time.
		if err := queries.LockSagaSearchChapter(ctx, sqlc.LockSagaSearchChapterParams{
			ProjectID: prepared.projectID, ChapterID: prepared.chapterID,
		}); err != nil {
			return err
		}
		if _, err := queries.LockCurrentSourceProject(ctx, sqlc.LockCurrentSourceProjectParams{
			ProjectID:                  prepared.projectID,
			ProjectLifecycleGeneration: input.Snapshot.ProjectLifecycleGeneration,
		}); err != nil {
			return err
		}
		if _, err := queries.LockCurrentSourceBook(ctx, sqlc.LockCurrentSourceBookParams{
			ProjectID: prepared.projectID, BookID: prepared.bookID,
			BookLifecycleGeneration: sourceTuple.BookLifecycleGeneration,
		}); err != nil {
			return err
		}
		if _, err := queries.LockCurrentSourceChapter(ctx, sqlc.LockCurrentSourceChapterParams{
			ProjectID: prepared.projectID, BookID: prepared.bookID, ChapterID: prepared.chapterID,
			ChapterLifecycleGeneration: sourceTuple.ChapterLifecycleGeneration,
		}); err != nil {
			return err
		}
		if _, err := queries.LockExactCanonTextChapterCatalogMember(ctx, sqlc.LockExactCanonTextChapterCatalogMemberParams{
			SourceView: string(input.Snapshot.SourceView), ProjectLifecycleGeneration: input.Snapshot.ProjectLifecycleGeneration,
			CatalogRevision: int64(input.Snapshot.CatalogRevision), BookID: prepared.bookID,
			BookLifecycleGeneration: sourceTuple.BookLifecycleGeneration, ChapterID: prepared.chapterID,
			ChapterLifecycleGeneration: sourceTuple.ChapterLifecycleGeneration, SourceRevision: int64(input.Snapshot.SourceRevision),
			ProjectID: prepared.projectID, SourceGeneration: input.Snapshot.SourceGeneration[:],
		}); err != nil {
			return err
		}
		if _, err := queries.LockClaimedCanonTextIndexJob(ctx, sqlc.LockClaimedCanonTextIndexJobParams{
			ProjectID: prepared.projectID, JobID: prepared.jobID,
			ClaimIncarnation: input.Snapshot.Claim.ClaimIncarnation,
			SourceView:       string(input.Snapshot.SourceView), CatalogRevision: int64(input.Snapshot.CatalogRevision),
		}); err != nil {
			return err
		}

		if _, err := queries.LockMatchingCanonTextChapterState(ctx, canonicalStateParams(input.Snapshot)); err == nil {
			disposition = CanonTextChapterUnchanged
			// An unchanged text still gets the saga search rows it lacks.
			return ensureChapterSearchRows(ctx, queries, prepared.projectID, prepared.chapterID, input.Snapshot)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if err := queries.UpsertCanonTextChapterSearchRow(ctx, sqlc.UpsertCanonTextChapterSearchRowParams{
			ProjectID: prepared.projectID, SourceView: string(document.SourceView), BookID: prepared.bookID,
			ChapterID: prepared.chapterID, SourceRevision: int64(document.SourceRevision),
			SourceGeneration: document.SourceGeneration[:], Title: document.Title, Content: document.Content,
			ChapterContentHash: document.ContentHash[:], IndexedAt: pgtype.Timestamptz{Time: input.IndexedAt, Valid: true},
		}); err != nil {
			return err
		}
		if err := queries.UpsertCanonTextChapterState(ctx, sqlc.UpsertCanonTextChapterStateParams{
			ProjectID: prepared.projectID, SourceView: string(input.Snapshot.SourceView), ChapterID: prepared.chapterID,
			SourceRevision: int64(input.Snapshot.SourceRevision), SourceGeneration: input.Snapshot.SourceGeneration[:],
			ChapterContentHash: input.Snapshot.ChapterContentHash[:],
			IndexedAt:          pgtype.Timestamptz{Time: input.IndexedAt, Valid: true},
		}); err != nil {
			return err
		}
		if err := ensureChapterSearchRows(ctx, queries, prepared.projectID, prepared.chapterID, input.Snapshot); err != nil {
			return err
		}
		return s.hit("after_state_upsert")
	})
	if err != nil {
		return "", mapCanonTextChapterSearchVectorError(mapTextIndexError(err))
	}
	return disposition, nil
}

func mapCanonTextChapterSearchVectorError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "54000" {
		return ErrTextIndexChapterOversize
	}
	return err
}
