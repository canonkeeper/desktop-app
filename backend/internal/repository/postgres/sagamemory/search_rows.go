package sagamemory

import (
	"context"
	"encoding/hex"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// The reads of one saga call over the saga search rows (specs/2026-10-28-saga-search-in-postgres.md,
// UC2, UC3). Every lookup is bounded by the project and the stored texts of the call's version.

// searchTexts returns the stored search texts of the selected chapters by chapter text.
func searchTexts(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, chapters []service.SelectedChapter) (map[service.CardKey]domain.SearchText, error) {
	ids := make([]pgtype.UUID, 0, len(chapters))
	for _, chapter := range chapters {
		id, err := storedb.ParseUUID(chapter.ID)
		if err != nil {
			return nil, service.ErrInvalid
		}
		ids = append(ids, id)
	}
	texts := make(map[service.CardKey]domain.SearchText, len(chapters))
	if len(ids) == 0 {
		return texts, nil
	}
	rows, err := queries.ListChapterSearchTexts(ctx, sqlc.ListChapterSearchTextsParams{ProjectID: project, ChapterIds: ids})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		key := service.CardKey{ChapterID: storedb.UUIDString(row.ChapterID), ContentHash: hex.EncodeToString(row.ContentHash)}
		texts[key] = domain.SearchText{
			ID: storedb.UUIDString(row.ID), Orders: row.ParagraphOrders, Tokens: row.ParagraphTokens, Runes: int(row.TextRunes),
		}
	}
	return texts, nil
}

// Vocabulary returns the looked-up words of the texts and every word of them with a prefix.
func (repository *Repository) Vocabulary(ctx context.Context, projectID string, textIDs []string, lookup domain.SearchLookup) ([]domain.VocabularyWord, error) {
	project, texts, err := searchArgs(projectID, textIDs)
	if err != nil || len(lookup.Words) == 0 && len(lookup.Prefixes) == 0 {
		return nil, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaSearchVocabulary(ctx, sqlc.ListSagaSearchVocabularyParams{
		ProjectID: project, TextIds: texts, Words: nonNil(lookup.Words), Prefixes: nonNil(lookup.Prefixes),
	})
	if err != nil {
		return nil, err
	}
	words := make([]domain.VocabularyWord, len(rows))
	for i, row := range rows {
		words[i] = domain.VocabularyWord{Word: row.Word, Stem: row.Stem}
	}
	return words, nil
}

// StemWords returns every word row of the texts with one of the stems.
func (repository *Repository) StemWords(ctx context.Context, projectID string, textIDs, stems []string) ([]service.StoredWord, error) {
	project, texts, err := searchArgs(projectID, textIDs)
	if err != nil || len(stems) == 0 {
		return nil, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaSearchStemWords(ctx, sqlc.ListSagaSearchStemWordsParams{
		ProjectID: project, TextIds: texts, Stems: stems,
	})
	if err != nil {
		return nil, err
	}
	words := make([]service.StoredWord, len(rows))
	for i, row := range rows {
		words[i] = service.StoredWord{TextID: storedb.UUIDString(row.TextID), Word: row.Word, Stem: row.Stem, Postings: row.Postings}
	}
	return words, nil
}

// RareStemWords returns the word rows of the stems whose postings in the texts fit in maxBytes.
func (repository *Repository) RareStemWords(ctx context.Context, projectID string, textIDs, stems []string, maxBytes int64) ([]service.StoredWord, error) {
	project, texts, err := searchArgs(projectID, textIDs)
	if err != nil || len(stems) == 0 {
		return nil, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaSearchRareStemWords(ctx, sqlc.ListSagaSearchRareStemWordsParams{
		ProjectID: project, TextIds: texts, Stems: stems, MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	words := make([]service.StoredWord, len(rows))
	for i, row := range rows {
		words[i] = service.StoredWord{TextID: storedb.UUIDString(row.TextID), Word: row.Word, Stem: row.Stem, Postings: row.Postings}
	}
	return words, nil
}

// WordsContaining returns every word row of the texts whose word contains part.
func (repository *Repository) WordsContaining(ctx context.Context, projectID string, textIDs []string, part string) ([]service.StoredWord, error) {
	project, texts, err := searchArgs(projectID, textIDs)
	if err != nil || part == "" {
		return nil, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaSearchWordsContaining(ctx, sqlc.ListSagaSearchWordsContainingParams{
		ProjectID: project, TextIds: texts, Part: part,
	})
	if err != nil {
		return nil, err
	}
	words := make([]service.StoredWord, len(rows))
	for i, row := range rows {
		words[i] = service.StoredWord{TextID: storedb.UUIDString(row.TextID), Word: row.Word, Stem: row.Stem, Postings: row.Postings}
	}
	return words, nil
}

// Paragraphs reads paragraphs of live chapter revisions of the project by their stored order.
func (repository *Repository) Paragraphs(ctx context.Context, projectID, view string, keys []service.ParagraphKey) ([]service.ParagraphText, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	params := sqlc.ListSagaParagraphsAtParams{ProjectID: project, SourceView: view}
	for _, key := range keys {
		chapter, err := storedb.ParseUUID(key.ChapterID)
		if err != nil {
			return nil, service.ErrInvalid
		}
		params.ChapterIds = append(params.ChapterIds, chapter)
		params.Revisions = append(params.Revisions, key.Revision)
		params.Orders = append(params.Orders, key.Order)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaParagraphsAt(ctx, params)
	if err != nil {
		return nil, err
	}
	paragraphs := make([]service.ParagraphText, len(rows))
	for i, row := range rows {
		paragraphs[i] = service.ParagraphText{ChapterID: storedb.UUIDString(row.ChapterID), Order: row.Order, Text: row.PlainText}
	}
	return paragraphs, nil
}

func searchArgs(projectID string, textIDs []string) (pgtype.UUID, []pgtype.UUID, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, nil, service.ErrInvalid
	}
	texts := make([]pgtype.UUID, len(textIDs))
	for i, id := range textIDs {
		if texts[i], err = storedb.ParseUUID(id); err != nil {
			return pgtype.UUID{}, nil, service.ErrInvalid
		}
	}
	return project, texts, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
