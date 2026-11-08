package sagadraft

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/sagadraft"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// maxChapterText is the limit of the working text of a draft (chapter_drafts_chapter_check).
const maxChapterText = 400000

// chapterText is the text of a chapter of the book as the working text of a draft: a paragraph a line, an empty line
// between them.
func chapterText(paragraphs []string) string { return strings.Join(paragraphs, "\n\n") }

// editedChapterInTx reads a chapter of the book for its edit; a deleted one is domain.ErrChapterGone.
func (repository *Repository) editedChapterInTx(ctx context.Context, tx pgx.Tx, projectID, chapterID string, revision int64) (library.EditedChapter, error) {
	chapter, err := repository.library.EditedChapterInTx(ctx, tx, projectID, chapterID, revision)
	if errors.Is(err, library.ErrNotFound) {
		return library.EditedChapter{}, domain.ErrChapterGone
	}
	return chapter, err
}

// OpenChapterEdit opens the draft of «Письмо» that edits a chapter of the book (specs/2026-11-05-voice-full-access.md
// A8.7): the one the chapter has, or a new one with its text, its title and its place after the chapter before it, so
// that the writing reads the book up to it. A draft stays as it stands, also when its chapter changed in the book since: the
// text of the book replaces the author's only by ReopenChapterFromBook, behind a question (A8.7.5).
func (repository *Repository) OpenChapterEdit(ctx context.Context, write service.Write, chapterID string) (domain.Draft, error) {
	var opened domain.Draft
	err := repository.WriteTx(ctx, write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, true); err != nil {
			return err
		}
		chapter, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, chapterID, 0)
		if errors.Is(err, domain.ErrChapterGone) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		text := chapterText(chapter.Paragraphs)
		if utf8.RuneCountInString(text) > maxChapterText {
			return domain.ErrInvalid
		}
		chapterUUID, bookUUID := mustUUID(chapter.ChapterID), mustUUID(chapter.BookID)
		existing, err := queries.ChapterDraftOfEditedChapter(ctx, sqlc.ChapterDraftOfEditedChapterParams{ProjectID: project, ChapterID: chapterUUID})
		switch {
		case err == nil:
			opened, err = GetInTx(ctx, queries, project, existing)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		after := pgtype.UUID{}
		if chapter.PreviousChapterID != "" {
			after = mustUUID(chapter.PreviousChapterID)
		}
		id, err := queries.CreateChapterEditDraft(ctx, sqlc.CreateChapterEditDraftParams{
			ProjectID: project, Title: chapter.Title, BookID: bookUUID, AfterChapterID: after, Body: text,
			ChapterID: chapterUUID, EditsRevision: int8Of(chapter.Revision), Actor: actor,
		})
		if err != nil {
			return err
		}
		opened, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return opened, err
}

// ReopenChapterFromBook is «Открыть главу заново» (A8.7.5): the draft of an edit takes the text and the title of its chapter
// as the book holds them now, in the place of the author's.
func (repository *Repository) ReopenChapterFromBook(ctx context.Context, write service.Write, draftID string, expected int64) (domain.Draft, error) {
	var reopened domain.Draft
	err := repository.WriteTx(ctx, write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, true); err != nil {
			return err
		}
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		if err := editable(draft, expected); err != nil {
			return err
		}
		chapter, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, draft.Edits.ChapterID, 0)
		if err != nil {
			return err
		}
		text := chapterText(chapter.Paragraphs)
		if utf8.RuneCountInString(text) > maxChapterText {
			return domain.ErrInvalid
		}
		if err := queries.ReopenChapterEditDraft(ctx, sqlc.ReopenChapterEditDraftParams{
			ProjectID: project, ID: id, Title: chapter.Title, Body: text, EditsRevision: int8Of(chapter.Revision), Actor: actor,
		}); err != nil {
			return err
		}
		reopened, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return reopened, err
}

// ReplaceChapterFromDraft puts the working text of an edit, without the marks of what the helper wrote, in the place of
// its chapter in the book, at the next revision and under the title the author confirmed (A8.7). The chapter must still
// stand at the revision the draft took: one changed since is domain.ErrChapterChanged, one deleted domain.ErrChapterGone.
func (repository *Repository) ReplaceChapterFromDraft(ctx context.Context, write service.Write, draftID string, expected int64, title string) (domain.Draft, error) {
	var replaced domain.Draft
	err := repository.WriteTx(ctx, write, true, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, true); err != nil {
			return err
		}
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		if err := editable(draft, expected); err != nil {
			return err
		}
		text := domain.WithoutMarks(draft.Text)
		paragraphs := domain.Paragraphs(text)
		if len(paragraphs) == 0 {
			return domain.ErrNoText
		}
		chapter, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, draft.Edits.ChapterID, 0)
		if err != nil {
			return err
		}
		if chapter.Revision != draft.Edits.Revision {
			return domain.ErrChapterChanged
		}
		revision, err := repository.replaceInTx(ctx, tx, write.ProjectID, chapter, title, paragraphs)
		if err != nil {
			return err
		}
		if err := queries.MarkChapterDraftReplaced(ctx, sqlc.MarkChapterDraftReplacedParams{
			ProjectID: project, ID: id, Title: strings.TrimSpace(title), EditsRevision: int8Of(revision),
			EditsPreviousRevision: int8Of(chapter.Revision), Body: chapterText(paragraphs), Actor: actor,
		}); err != nil {
			return err
		}
		replaced, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return replaced, err
}

// RevertChapterFromDraft gives the chapter back the text it had before the last replacement from this draft, as a new
// revision, and the draft takes that text too (A8.7). It is one step back: there is no history of versions.
func (repository *Repository) RevertChapterFromDraft(ctx context.Context, write service.Write, draftID string, expected int64) (domain.Draft, error) {
	var reverted domain.Draft
	err := repository.WriteTx(ctx, write, true, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, true); err != nil {
			return err
		}
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		if err := editable(draft, expected); err != nil {
			return err
		}
		if draft.Edits.PreviousRevision < 1 {
			return domain.ErrInvalid
		}
		chapter, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, draft.Edits.ChapterID, 0)
		if err != nil {
			return err
		}
		if chapter.Revision != draft.Edits.Revision {
			return domain.ErrChapterChanged
		}
		before, err := repository.editedChapterInTx(ctx, tx, write.ProjectID, draft.Edits.ChapterID, draft.Edits.PreviousRevision)
		if err != nil {
			return err
		}
		revision, err := repository.replaceInTx(ctx, tx, write.ProjectID, chapter, chapter.Title, before.Paragraphs)
		if err != nil {
			return err
		}
		if err := queries.MarkChapterDraftReplaced(ctx, sqlc.MarkChapterDraftReplacedParams{
			ProjectID: project, ID: id, Title: chapter.Title, EditsRevision: int8Of(revision),
			Body: chapterText(before.Paragraphs), Actor: actor,
		}); err != nil {
			return err
		}
		reverted, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return reverted, err
}

// editable checks a locked draft is an edit of a chapter the author may put in the book now.
func editable(draft domain.Draft, expected int64) error {
	switch {
	case draft.Edits == nil:
		return domain.ErrInvalid
	case draft.Job != nil:
		return domain.ErrBusy
	case draft.Revision != expected:
		return domain.ErrConflict
	}
	return nil
}

func int8Of(value int64) pgtype.Int8 { return pgtype.Int8{Int64: value, Valid: true} }

func (repository *Repository) replaceInTx(ctx context.Context, tx pgx.Tx, projectID string, chapter library.EditedChapter, title string, paragraphs []string) (int64, error) {
	revision, err := repository.library.ReplaceChapterTextInTx(ctx, tx, library.ReplaceChapterTextInput{
		ProjectID: projectID, BookID: chapter.BookID, ChapterID: chapter.ChapterID, ExpectedRevision: chapter.Revision,
		Title: title, Paragraphs: paragraphs,
	})
	switch {
	case errors.Is(err, library.ErrNotFound):
		return 0, domain.ErrChapterGone
	case errors.Is(err, library.ErrConflict):
		return 0, domain.ErrChapterChanged
	case errors.Is(err, library.ErrValidation):
		return 0, domain.ErrInvalid
	}
	return revision, err
}

// mustUUID reads an id the library has just given.
func mustUUID(value string) pgtype.UUID {
	id, _ := storedb.ParseUUID(value)
	return id
}
