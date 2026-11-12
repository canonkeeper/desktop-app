package sagamemory

import (
	"context"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
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
			BookOrder: selected.BookOrder, BookKeyNumber: selected.BookKeyNumber, BookTitle: selected.BookTitle, Order: selected.Order, Title: selected.Title,
		}
	}
	return domain.NewVersion(selection.ProjectID, selection.View, selection.Fingerprint, chapters)
}

// pageView is the text version the pages of a saga show: the saga has one, the published.
const pageView = string(governance.SourceViewPublished)

// Places names the chapters of the keys as the author reads them on the pages of the saga («Книга 2, гл. 12 „Белый
// порт“»), by the key as written; a key the text has no chapter for is left out, and no keys read nothing
// (specs/2026-11-10-ux1-names.md N1.3).
func (saga *Saga) Places(ctx context.Context, projectID string, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	version, err := saga.ChapterVersion(ctx, projectID, pageView)
	if err != nil {
		return nil, err
	}
	return version.Places(keys), nil
}
