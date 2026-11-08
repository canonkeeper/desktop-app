package indexingstore

import (
	"context"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

const lexizeBatch = 5000

// ensureChapterSearchRows makes the saga search rows of the text of a text index state exist and
// drops the rows of texts no state of the chapter names any more
// (specs/2026-10-28-saga-search-in-postgres.md, UC1, R1.3, R5.1). It runs in the transaction of
// the state under LockSagaSearchChapter, taken first in that transaction: the check that the
// rows exist, their build and the drop of stale rows of another version never interleave.
func ensureChapterSearchRows(
	ctx context.Context, queries *sqlc.Queries, projectID, chapterID pgtype.UUID, snapshot CanonTextChapterSnapshot,
) error {
	present, err := queries.ChapterSearchTextExists(ctx, sqlc.ChapterSearchTextExistsParams{
		ProjectID: projectID, ChapterID: chapterID, ContentHash: snapshot.ChapterContentHash[:],
	})
	if err != nil {
		return err
	}
	if !present {
		if err := insertChapterSearchRows(ctx, queries, projectID, chapterID, snapshot); err != nil {
			return err
		}
	}
	_, err = queries.DeleteStaleChapterSearchTexts(ctx, sqlc.DeleteStaleChapterSearchTextsParams{ProjectID: projectID, ChapterID: chapterID})
	return err
}

// insertChapterSearchRows stems the words of the snapshot paragraphs and stores the rows.
func insertChapterSearchRows(
	ctx context.Context, queries *sqlc.Queries, projectID, chapterID pgtype.UUID, snapshot CanonTextChapterSnapshot,
) error {
	orders := make([]int32, len(snapshot.Paragraphs))
	texts := make([]string, len(snapshot.Paragraphs))
	for i, paragraph := range snapshot.Paragraphs {
		orders[i] = int32(paragraph.Order)
		texts[i] = paragraph.Content
	}
	words := domain.ChapterSearchWords(texts)
	stems := make(map[string]string, len(words))
	for start := 0; start < len(words); start += lexizeBatch {
		lexemes, err := queries.LexizeSagaMemoryWords(ctx, words[start:min(start+lexizeBatch, len(words))])
		if err != nil {
			return err
		}
		for _, lexeme := range lexemes {
			stems[lexeme.Word] = lexeme.Stem
		}
	}
	built := domain.BuildChapterSearchText(texts, stems)
	tokens := built.ParagraphTokens
	if tokens == nil {
		tokens = []int32{}
	}
	ids, err := queries.InsertChapterSearchText(ctx, sqlc.InsertChapterSearchTextParams{
		ProjectID: projectID, ChapterID: chapterID, ContentHash: snapshot.ChapterContentHash[:],
		ParagraphOrders: orders, ParagraphTokens: tokens, TextRunes: int32(built.Runes),
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	rows := make([]sqlc.InsertChapterSearchWordsParams, len(built.Words))
	for i, word := range built.Words {
		rows[i] = sqlc.InsertChapterSearchWordsParams{TextID: ids[0], ProjectID: projectID, Word: word.Word, Stem: word.Stem, Postings: word.Postings}
	}
	_, err = queries.InsertChapterSearchWords(ctx, rows)
	return err
}
