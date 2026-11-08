package sagadraft

import (
	"context"
	"strings"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/sagadraft"
)

// ChapterEditStore opens the edit of a chapter of the book in a draft and puts the draft back in its place
// (specs/2026-11-05-voice-full-access.md A8.7). Replace and revert go at the expected revision of the draft:
// domain.ErrChapterChanged when the chapter changed in the book since, domain.ErrChapterGone when it is deleted.
type ChapterEditStore interface {
	OpenChapterEdit(ctx context.Context, actor Write, chapterID string) (domain.Draft, error)
	ReplaceChapterFromDraft(ctx context.Context, actor Write, draftID string, expected int64, title string) (domain.Draft, error)
	RevertChapterFromDraft(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error)
	ReopenChapterFromBook(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error)
}

// OpenChapterEdit is openChapterEdit: «Править главу» in the reader.
type OpenChapterEdit struct{ Store ChapterEditStore }

func (useCase OpenChapterEdit) Execute(ctx context.Context, actor Write, chapterID string) (domain.Draft, error) {
	if !actor.Proof.Valid() || strings.TrimSpace(chapterID) == "" {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.OpenChapterEdit(ctx, actor, strings.TrimSpace(chapterID))
}

// maxChapterTitle is the limit of the title of a chapter of the book.
const maxChapterTitle = 180

// ReplaceChapter is replaceChapterFromDraft: «Заменить главу в книге», under the title the author confirmed.
type ReplaceChapter struct{ Store ChapterEditStore }

func (useCase ReplaceChapter) Execute(ctx context.Context, actor Write, draftID string, expected int64, title string) (domain.Draft, error) {
	title = strings.TrimSpace(title)
	if !actor.Proof.Valid() || expected < 1 || title == "" || utf8.RuneCountInString(title) > maxChapterTitle {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.ReplaceChapterFromDraft(ctx, actor, draftID, expected, title)
}

// RevertChapter is revertChapterFromDraft: «Вернуть прежний текст» after a replacement.
type RevertChapter struct{ Store ChapterEditStore }

func (useCase RevertChapter) Execute(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error) {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.RevertChapterFromDraft(ctx, actor, draftID, expected)
}

// ReopenChapter is reopenChapterFromBook: «Открыть главу заново», after the question of the page: the text of the field goes.
type ReopenChapter struct{ Store ChapterEditStore }

func (useCase ReopenChapter) Execute(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error) {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.ReopenChapterFromBook(ctx, actor, draftID, expected)
}
