package sagadraft

import (
	"context"

	domain "canonkeeper/backend/internal/domain/sagadraft"
)

// The versions of the text of a draft (specs/2026-11-06-writing-agent.md, WA7.5, WA8): any member reads them, an editor
// or above takes one, or a piece of one, back into the working text.

// VersionStore reads the versions of the text of a draft for a member of its project.
type VersionStore interface {
	ChapterDraftVersions(ctx context.Context, actor Actor, draftID string) ([]domain.Version, error)
	ChapterDraftVersion(ctx context.Context, actor Actor, draftID string, number int) (domain.Version, error)
}

// ListVersions is listChapterDraftVersions: the versions without their text, the latest first.
type ListVersions struct{ Store VersionStore }

// Execute lists the versions.
func (useCase ListVersions) Execute(ctx context.Context, actor Actor, draftID string) ([]domain.Version, error) {
	return useCase.Store.ChapterDraftVersions(ctx, actor, draftID)
}

// GetVersion is getChapterDraftVersion: one version whole.
type GetVersion struct{ Store VersionStore }

// Execute reads the version.
func (useCase GetVersion) Execute(ctx context.Context, actor Actor, draftID string, number int) (domain.Version, error) {
	if number < 1 {
		return domain.Version{}, domain.ErrInvalid
	}
	return useCase.Store.ChapterDraftVersion(ctx, actor, draftID, number)
}

// TakeStore takes a version into the working text at the expected revision: domain.ErrConflict when the draft is not
// at it, domain.ErrBusy while a Job works, domain.ErrAdded once it is in the book, domain.ErrInvalid for a piece or a
// place that does not stand in its text once.
type TakeStore interface {
	TakeChapterDraftVersion(ctx context.Context, actor Write, draftID string, number int, expected int64, take domain.Take) (domain.Draft, error)
}

// TakeVersion is takeChapterDraftVersion (WA7.5): «Вернуть эту версию» whole, or «Взять в главу» a piece of it.
type TakeVersion struct{ Store TakeStore }

// Execute takes the version.
func (useCase TakeVersion) Execute(ctx context.Context, actor Write, draftID string, number int, expected int64, take domain.Take) (domain.Draft, error) {
	if !actor.Proof.Valid() || expected < 1 || number < 1 {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.TakeChapterDraftVersion(ctx, actor, draftID, number, expected, take)
}
