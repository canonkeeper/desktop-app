package sagamemory

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

const nearestFiller = "после долгого пути отряд остановился возле старой мельницы на закате"

// nearestVersion is one chapter of paragraphs, and a first chapter outside the pool of nearestQuery.
func nearestVersion(paragraphs ...string) (*Index, storedSearch) {
	chapters := []Chapter{
		{ID: "before", BookID: "b", Paragraphs: []Paragraph{{N: 1, Text: "обсидиан " + nearestFiller}, {N: 2, Text: "обсидиан " + nearestFiller}}},
		{ID: "pool", BookID: "b"},
	}
	for i, text := range paragraphs {
		chapters[1].Paragraphs = append(chapters[1].Paragraphs, Paragraph{N: i + 1, Text: text})
	}
	version := NewVersion("project", "draft", "fp", chapters)
	stored := storeVersion(version, nil)
	texts := make([]SearchText, len(stored.tokens))
	for i, tokens := range stored.tokens {
		texts[i] = SearchText{ID: fmt.Sprintf("text-%d", i), Orders: make([]int32, len(tokens)), Tokens: tokens}
	}
	index, err := NewSearchIndex(NewOutline(version, texts))
	if err != nil {
		panic(err)
	}
	return index, stored
}

func nearestQuery(text string, limit int) NearestQuery {
	return NearestQuery{Query: Query{Text: text, From: 1, To: -1}, MinTokens: 8, Limit: limit}
}

func rankNearest(t *testing.T, index *Index, stored storedSearch, query NearestQuery) []Hit {
	t.Helper()
	text := NearestText(query.Text)
	index.AddVocabulary(stored.vocabulary(index.Lookup([]string{text}, ModeAny)))
	hits, err := index.RankNearest(index.NearestPool(query), query.Limit, stored.postings(index.QueryStems([]string{text}, ModeAny)))
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestNearestTextKeepsWordsOnceWithoutFunctionAndShortWords(t *testing.T) {
	got := NearestText(`¶3 Мирон спросил, «где меч» и* ещё раз спросил — мирон не ел`)
	if got != "мирон спросил где меч раз" {
		t.Fatalf("NearestText() = %q", got)
	}
}

// The score is the one the blind exam measured, over the pool alone: its paragraphs of at least 8 words
// in the range, whatever lies before it or is too short.
func TestRankNearestScoresBM25OverThePool(t *testing.T) {
	paragraphs := []string{"обсидиан обсидиан " + nearestFiller, nearestFiller, "обсидиан коротко"}
	for range 5 {
		paragraphs = append(paragraphs, nearestFiller+" и снова в путь")
	}
	index, stored := nearestVersion(paragraphs...)
	hits := rankNearest(t, index, stored, nearestQuery("обсидиан", 5))
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want the one paragraph of the pool", hits)
	}
	if hits[0].Tokens != 12 {
		t.Fatalf("tokens = %d", hits[0].Tokens)
	}
	const pool = 7.0
	average := (12 + 10 + 5*14) / pool
	want := math.Log(1+pool/1) * 2 * 2.2 / (2 + 1.2*(.25+.75*12/average))
	if math.Abs(hits[0].Score-want) > 1e-9 {
		t.Fatalf("score = %v, want %v", hits[0].Score, want)
	}
}

func TestRankNearestPutsOneRareWordAboveTwoFrequentOnes(t *testing.T) {
	var paragraphs []string
	for i := range 200 {
		switch {
		case i == 0:
			paragraphs = append(paragraphs, "меч пепел "+nearestFiller)
		case i == 1:
			paragraphs = append(paragraphs, "обсидиан "+nearestFiller)
		case i < 21:
			paragraphs = append(paragraphs, "меч "+nearestFiller)
		case i < 41:
			paragraphs = append(paragraphs, "пепел "+nearestFiller)
		default:
			paragraphs = append(paragraphs, nearestFiller)
		}
	}
	index, stored := nearestVersion(paragraphs...)
	hits := rankNearest(t, index, stored, nearestQuery("меч пепел обсидиан", 1))
	if _, paragraph := index.Locate(hits[0].Para); !strings.HasPrefix(paragraph.Text, "обсидиан") {
		t.Fatalf("best = %q", paragraph.Text)
	}
}

// A stem in more than an eighth of the paragraphs of the pool does not rank at all.
func TestRankNearestDropsAStemInMoreThanAnEighthOfThePool(t *testing.T) {
	var paragraphs []string
	for i := range 80 {
		text := nearestFiller
		if i < 11 {
			text = "кузнец " + text
		}
		paragraphs = append(paragraphs, text)
	}
	index, stored := nearestVersion(paragraphs...)
	if hits := rankNearest(t, index, stored, nearestQuery("кузнец", 3)); len(hits) != 0 {
		t.Fatalf("hits = %+v, want none of a stem in 11 of 80", hits)
	}
	paragraphs[10] = nearestFiller
	index, stored = nearestVersion(paragraphs...)
	if hits := rankNearest(t, index, stored, nearestQuery("кузнец", 3)); len(hits) != 3 {
		t.Fatalf("hits = %+v, want 3 of a stem in 10 of 80", hits)
	}
}

// A saga too small to have an eighth keeps a word found once.
func TestRankNearestKeepsARareWordOfASmallSaga(t *testing.T) {
	index, stored := nearestVersion("кузнец "+nearestFiller, nearestFiller)
	if hits := rankNearest(t, index, stored, nearestQuery("кузнец", 3)); len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestMaxPostingBytesIsOneBytePerParagraphOfThePool(t *testing.T) {
	var paragraphs []string
	for range 2000 {
		paragraphs = append(paragraphs, nearestFiller)
	}
	index, _ := nearestVersion(paragraphs...)
	if got := index.NearestPool(nearestQuery("кузнец", 3)).MaxPostingBytes(); got != 2000 {
		t.Fatalf("MaxPostingBytes() = %d", got)
	}
	index, _ = nearestVersion(nearestFiller)
	if got := index.NearestPool(nearestQuery("кузнец", 3)).MaxPostingBytes(); got != minPostingBytesCap {
		t.Fatalf("MaxPostingBytes() of a small saga = %d", got)
	}
}
