package canonstore

import (
	"context"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

type CanonicalTextBaseline struct {
	Content    string
	Locator    governance.CanonicalChapterSourceLocatorV1
	SourceRefs []governance.ArtifactSourceRef
}

// LoadCurrentCanonicalTargetTextInTx проверяет выбранную книгу или главу в transaction вызывающего кода.
func (db *Store) LoadCurrentCanonicalTargetTextInTx(
	ctx context.Context,
	tx pgx.Tx,
	input CanonicalTextSelectionInput,
) (CanonicalTextSelection, error) {
	if db == nil || tx == nil || input.BookID == "" && input.ChapterID == "" {
		return CanonicalTextSelection{}, ErrCanonicalSourceValidation
	}
	return loadCanonicalTextSelectionInTx(ctx, tx, input)
}

// LoadCurrentCanonicalProjectTextInTx не допускает молчаливого исключения неактуальных книг.
func (db *Store) LoadCurrentCanonicalProjectTextInTx(
	ctx context.Context,
	tx pgx.Tx,
	input CanonicalTextSelectionInput,
) (CanonicalTextSelection, error) {
	if db == nil || tx == nil || input.BookID != "" || input.ChapterID != "" {
		return CanonicalTextSelection{}, ErrCanonicalSourceValidation
	}
	selection, err := loadCanonicalTextSelectionInTx(ctx, tx, input)
	if err != nil {
		return CanonicalTextSelection{}, err
	}
	coverage := selection.TextCoverage
	if coverage.TotalBooks == 0 {
		return CanonicalTextSelection{}, canonicalSourceViewUnavailable(input.SourceView)
	}
	if coverage.CurrentBooks != coverage.TotalBooks {
		return CanonicalTextSelection{}, &searchError{
			kind: search.ErrBookTextNotCurrent, action: search.ActionStartIndexing,
		}
	}
	return selection, nil
}

// LoadCurrentCanonicalTextBaselineInTx выбирает детерминированный current-фрагмент,
// чтобы пустой lexical search не оставлял модель без точного source context.
func (db *Store) LoadCurrentCanonicalTextBaselineInTx(
	ctx context.Context,
	tx pgx.Tx,
	input CanonicalTextSelectionInput,
) (CanonicalTextBaseline, error) {
	if db == nil || tx == nil || input.ChapterID != "" {
		return CanonicalTextBaseline{}, ErrCanonicalSourceValidation
	}
	var (
		selection CanonicalTextSelection
		err       error
	)
	if input.BookID == "" {
		selection, err = db.LoadCurrentCanonicalProjectTextInTx(ctx, tx, input)
	} else {
		selection, err = db.LoadCurrentCanonicalTargetTextInTx(ctx, tx, input)
	}
	if err != nil || len(selection.CurrentBookIDs) == 0 {
		if err != nil {
			return CanonicalTextBaseline{}, err
		}
		return CanonicalTextBaseline{}, ErrCanonicalSourceNotCurrent
	}
	bookID := selection.CurrentBookIDs[0]
	for _, candidate := range selection.CurrentBookIDs[1:] {
		if candidate < bookID {
			bookID = candidate
		}
	}
	snapshot, err := loadCurrentBookSourceSnapshot(ctx, sqlc.New(tx), input.ProjectID, input.SourceView, bookID)
	if err != nil {
		return CanonicalTextBaseline{}, err
	}
	manifest, err := bookSourceSnapshotManifest(snapshot)
	if err != nil || manifest != selection.currentBookManifests[bookID] {
		return CanonicalTextBaseline{}, ErrCanonicalSourceNotCurrent
	}
	if len(snapshot.Chapters) == 0 {
		return CanonicalTextBaseline{}, ErrCanonicalSourceNotCurrent
	}
	chapter := snapshot.Chapters[0]
	for _, candidate := range snapshot.Chapters[1:] {
		if candidate.Order < chapter.Order || candidate.Order == chapter.Order && candidate.ChapterID < chapter.ChapterID {
			chapter = candidate
		}
	}
	resolved, err := db.ResolveCanonicalChapterSourceInTx(ctx, tx, snapshot.ProjectID, snapshot.SourceView, snapshot.BookID, chapter.ChapterID)
	if err != nil {
		return CanonicalTextBaseline{}, err
	}
	if resolved.SourceRevision != chapter.SourceRevision ||
		resolved.SourceGeneration != chapter.SourceGeneration || resolved.ContentHash != chapter.ChapterContentHash {
		return CanonicalTextBaseline{}, ErrCanonicalSourceNotCurrent
	}
	locator := governance.CanonicalChapterSourceLocatorV1{
		ProjectID: snapshot.ProjectID, BookID: snapshot.BookID, ChapterID: chapter.ChapterID, SourceRef: chapter.ChapterID,
		SourceView: snapshot.SourceView, SourceRevision: chapter.SourceRevision,
		SourceGeneration: chapter.SourceGeneration, ContentHash: chapter.ChapterContentHash,
	}
	return CanonicalTextBaseline{Content: resolved.Content, Locator: locator, SourceRefs: []governance.ArtifactSourceRef{resolved.ExactRef}}, nil
}
