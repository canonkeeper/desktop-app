package testutil

import (
	"context"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// SeedSagaSearchRows writes the saga search rows a text index Job would write for every text
// index state of a project that has none yet (specs/2026-10-28-saga-search-in-postgres.md, UC1):
// for fixtures that insert chapter_text_index_state directly.
func SeedSagaSearchRows(t *testing.T, db *storedb.DB, projectID string) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Pool.Query(ctx, `
		SELECT DISTINCT ON (state.chapter_id, state.chapter_content_hash)
		  state.chapter_id::text, state.chapter_content_hash, state.source_view, state.source_revision
		FROM chapter_text_index_state AS state
		WHERE state.project_id = $1
		  AND NOT EXISTS (
		    SELECT 1 FROM chapter_search_texts AS text
		    WHERE text.project_id = state.project_id AND text.chapter_id = state.chapter_id
		      AND text.content_hash = state.chapter_content_hash
		  )
		ORDER BY state.chapter_id, state.chapter_content_hash, state.source_view`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	type missing struct {
		chapterID string
		hash      []byte
		view      string
		revision  int64
	}
	var texts []missing
	for rows.Next() {
		var text missing
		if err := rows.Scan(&text.chapterID, &text.hash, &text.view, &text.revision); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, text := range texts {
		var orders []int32
		var paragraphs []string
		if err := db.Pool.QueryRow(ctx, `
			SELECT coalesce(array_agg("order" ORDER BY "order", id), '{}'), coalesce(array_agg(plain_text ORDER BY "order", id), '{}')
			FROM chapter_paragraphs WHERE chapter_id = $1 AND revision = $2 AND variant = $3`,
			text.chapterID, text.revision, text.view).Scan(&orders, &paragraphs); err != nil {
			t.Fatal(err)
		}
		stems := map[string]string{}
		lexemes, err := db.Pool.Query(ctx, `
			SELECT word, coalesce(array_to_string(ts_lexize('russian_stem', word), ' '), word)
			FROM unnest($1::text[]) AS word`, domain.ChapterSearchWords(paragraphs))
		if err != nil {
			t.Fatal(err)
		}
		for lexemes.Next() {
			var word, stem string
			if err := lexemes.Scan(&word, &stem); err != nil {
				t.Fatal(err)
			}
			stems[word] = stem
		}
		lexemes.Close()
		built := domain.BuildChapterSearchText(paragraphs, stems)
		var id string
		if err := db.Pool.QueryRow(ctx, `
			INSERT INTO chapter_search_texts (project_id, chapter_id, content_hash, paragraph_orders, paragraph_tokens, text_runes)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`,
			projectID, text.chapterID, text.hash, orders, nonNilTokens(built.ParagraphTokens), built.Runes).Scan(&id); err != nil {
			t.Fatal(err)
		}
		words := make([][]any, len(built.Words))
		for i, word := range built.Words {
			words[i] = []any{id, projectID, word.Word, word.Stem, word.Postings}
		}
		if _, err := db.Pool.CopyFrom(ctx, pgx.Identifier{"chapter_search_words"},
			[]string{"text_id", "project_id", "word", "stem", "postings"}, pgx.CopyFromRows(words)); err != nil {
			t.Fatal(err)
		}
	}
}

func nonNilTokens(tokens []int32) []int32 {
	if tokens == nil {
		return []int32{}
	}
	return tokens
}
