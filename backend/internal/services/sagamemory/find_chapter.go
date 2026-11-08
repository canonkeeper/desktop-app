package sagamemory

import (
	"context"

	domain "canonkeeper/backend/internal/domain/sagamemory"
)

// FindChapter resolves an agent-facing chapter key («481», «b3c51») in the current text
// version of a project. Keys depend only on the chapter selection, so no text is loaded.
func (saga *Saga) FindChapter(ctx context.Context, projectID, view, key string) (domain.Chapter, bool, error) {
	if ctx == nil || projectID == "" || view == "" {
		return domain.Chapter{}, false, ErrInvalid
	}
	selection, err := saga.text.Selection(ctx, projectID, view)
	if err != nil {
		return domain.Chapter{}, false, err
	}
	chapter, ok := outlineVersion(selection).Find(key)
	if !ok {
		return domain.Chapter{}, false, nil
	}
	return *chapter, true, nil
}

// ChapterVersion reads the chapter order and keys of a text version without its text, whether or not
// its search rows are built yet.
func (saga *Saga) ChapterVersion(ctx context.Context, projectID, view string) (*domain.Version, error) {
	if ctx == nil || projectID == "" || view == "" {
		return nil, ErrInvalid
	}
	selection, err := saga.text.Selection(ctx, projectID, view)
	if err != nil {
		return nil, err
	}
	return outlineVersion(selection), nil
}

// outlineVersion is the version of a selection without text: chapter order and keys only.
func outlineVersion(selection Selection) *domain.Version {
	chapters := make([]domain.Chapter, len(selection.Chapters))
	for i, selected := range selection.Chapters {
		chapters[i] = domain.Chapter{
			ID: selected.ID, Revision: selected.Revision, ContentHash: selected.ContentHash, BookID: selected.BookID,
			BookOrder: selected.BookOrder, BookTitle: selected.BookTitle, Order: selected.Order, Title: selected.Title,
		}
	}
	return domain.NewVersion(selection.ProjectID, selection.View, selection.Fingerprint, chapters)
}
