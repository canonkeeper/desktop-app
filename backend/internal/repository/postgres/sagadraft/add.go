package sagadraft

import (
	"context"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/sagadraft"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// LockProjectInTx is the first lock of a write that reaches the library or a Job: the project before the
// draft, as the end of a Job takes them, so that they cannot wait for each other.
func LockProjectInTx(ctx context.Context, tx pgx.Tx, project pgtype.UUID, forUpdate bool) error {
	query := `SELECT id FROM projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE`
	if forUpdate {
		query = `SELECT id FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`
	}
	var locked pgtype.UUID
	err := tx.QueryRow(ctx, query, project).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// AddChapterDraftToBook adds the chapter of a draft to its book and marks the draft in one transaction: a
// repeat answers the draft as it stands and adds nothing (R7.5).
func (repository *Repository) AddChapterDraftToBook(ctx context.Context, write service.Write, draftID string, expected int64) (domain.Draft, error) {
	var added domain.Draft
	err := repository.WriteTx(ctx, write, true, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, true); err != nil {
			return err
		}
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		if draft.AddedChapterID != "" {
			added, err = GetInTx(ctx, queries, project, id)
			return err
		}
		switch {
		case draft.Job.Active():
			return domain.ErrBusy
		case draft.Revision != expected:
			return domain.ErrConflict
		case draft.Place.BookID == "":
			return domain.ErrNoDestination
		}
		// An edit of a chapter still in the book replaces it, it is not added as a second copy (A8.7).
		if draft.Edits != nil {
			if _, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, draft.Edits.ChapterID, 0); !errors.Is(err, domain.ErrChapterGone) {
				if err == nil {
					return domain.ErrInvalid
				}
				return err
			}
		}
		// The book takes the working text, written or not (specs/2026-11-04-writing-plain-path.md P2.3), without the marks
		// of what the model wrote.
		paragraphs := domain.Paragraphs(domain.WithoutMarks(draft.Text))
		if draft.Title == "" || len(paragraphs) == 0 {
			return domain.ErrNoText
		}
		chapterID, err := repository.library.AddChapterInTx(ctx, tx, library.AddChapterInput{
			ProjectID: write.ProjectID, BookID: draft.Place.BookID, AfterChapterID: draft.Place.AfterChapterID,
			Title: draft.Title, Paragraphs: paragraphs, At: time.Now().UTC(),
		})
		if errors.Is(err, library.ErrNotFound) {
			return domain.ErrNoDestination
		}
		if err != nil {
			return err
		}
		chapter, err := pgUUID(chapterID)
		if err != nil {
			return err
		}
		if err := queries.MarkChapterDraftAdded(ctx, sqlc.MarkChapterDraftAddedParams{ProjectID: project, ID: id, ChapterID: chapter, Actor: actor}); err != nil {
			return err
		}
		added, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return added, err
}
