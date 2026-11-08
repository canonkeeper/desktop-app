package sagamemory_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// watchedText records what a ranking asks the database for.
type watchedText struct {
	*sagamemorytest.Text
	looked []string
	loaded map[string]bool
	texts  []string
}

func (text *watchedText) Vocabulary(ctx context.Context, projectID string, textIDs []string, lookup domain.SearchLookup) ([]domain.VocabularyWord, error) {
	text.looked = append(text.looked, lookup.Words...)
	return text.Text.Vocabulary(ctx, projectID, textIDs, lookup)
}

func (text *watchedText) RareStemWords(ctx context.Context, projectID string, textIDs, stems []string, maxBytes int64) ([]service.StoredWord, error) {
	text.texts = textIDs
	rows, err := text.Text.RareStemWords(ctx, projectID, textIDs, stems, maxBytes)
	for _, row := range rows {
		text.loaded[row.Stem] = true
	}
	return rows, err
}

// The ranking never looks up a function word or a word of two letters and reads no rows of a frequent
// stem or of the chapters before its range: the VPS has no memory to spare for them.
func TestNearestReadsNeitherFunctionWordsNorFrequentStems(t *testing.T) {
	const filler = "после долгого пути отряд остановился возле старой мельницы на закате"
	paragraphs := make([]domain.Paragraph, 2000)
	for i := range paragraphs {
		text := filler
		switch {
		case i%2 == 0:
			text = "дорога " + text
		case i == 1:
			text = "обсидиан " + text
		}
		paragraphs[i] = domain.Paragraph{N: i + 1, Text: text}
	}
	version := domain.NewVersion("project", "draft", "fp", []domain.Chapter{
		{ID: "before", BookID: "b", Paragraphs: []domain.Paragraph{{N: 1, Text: "обсидиан " + filler}}},
		{ID: "pool", BookID: "b", Paragraphs: paragraphs},
	})
	stems := map[string]string{"обсидиан": "обсидиан", "дорога": "дорог"}
	text := &watchedText{Text: sagamemorytest.NewText(version, stems, nil), loaded: map[string]bool{}}
	saga := service.NewSaga(text, text.Text)
	ctx := context.Background()
	outline, err := saga.Outline(ctx, "project", "draft")
	if err != nil {
		t.Fatal(err)
	}
	index, hits, err := saga.Nearest(ctx, outline, domain.NearestQuery{
		Query: domain.Query{Text: "и она не ел обсидиан дорога", From: 1, To: -1}, MinTokens: 8, Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if _, paragraph := index.Locate(hits[0].Para); !strings.HasPrefix(paragraph.Text, "обсидиан") {
		t.Fatalf("paragraph = %q, want its text loaded", paragraph.Text)
	}
	for _, word := range []string{"и", "она", "не", "ел"} {
		if slices.Contains(text.looked, word) {
			t.Fatalf("%q was looked up: %v", word, text.looked)
		}
	}
	if !slices.Contains(text.looked, "обсидиан") {
		t.Fatalf("looked up: %v", text.looked)
	}
	if text.loaded["дорог"] || !text.loaded["обсидиан"] {
		t.Fatalf("loaded = %v, want the rare stem only", text.loaded)
	}
	if !slices.Equal(text.texts, []string{outline.Texts[1].ID}) {
		t.Fatalf("texts = %v, want those of the range", text.texts)
	}
}
