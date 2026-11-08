package sagamemory

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// storedSearch keeps the rows BuildChapterSearchText writes for every chapter of a version, the
// way PostgreSQL holds them, and answers the two lookups of a search call.
type storedSearch struct {
	tokens [][]int32
	texts  []ChapterSearchText
}

func storeVersion(version *Version, stems map[string]string) storedSearch {
	var stored storedSearch
	for _, chapter := range version.Chapters {
		texts := make([]string, len(chapter.Paragraphs))
		for i, paragraph := range chapter.Paragraphs {
			texts[i] = paragraph.Text
		}
		text := BuildChapterSearchText(texts, stems)
		stored.tokens = append(stored.tokens, text.ParagraphTokens)
		stored.texts = append(stored.texts, text)
	}
	return stored
}

func (stored storedSearch) vocabulary(lookup SearchLookup) []VocabularyWord {
	var found []VocabularyWord
	seen := map[string]bool{}
	for _, text := range stored.texts {
		for _, word := range text.Words {
			match := false
			for _, wanted := range lookup.Words {
				match = match || word.Word == wanted
			}
			for _, prefix := range lookup.Prefixes {
				match = match || strings.HasPrefix(word.Word, prefix)
			}
			if match && !seen[word.Word] {
				seen[word.Word] = true
				found = append(found, VocabularyWord{Word: word.Word, Stem: word.Stem})
			}
		}
	}
	return found
}

func (stored storedSearch) postings(stems []string) []WordPostings {
	var rows []WordPostings
	for chapter, text := range stored.texts {
		for _, word := range text.Words {
			for _, stem := range stems {
				if word.Stem == stem {
					rows = append(rows, WordPostings{Chapter: chapter, Word: word.Word, Stem: word.Stem, Postings: word.Postings})
				}
			}
		}
	}
	return rows
}

// outlineOf is the version of a call: the chapters without their text and the stored texts.
func outlineOf(full *Version, stored storedSearch) *Outline {
	chapters := make([]Chapter, len(full.Chapters))
	texts := make([]SearchText, len(full.Chapters))
	for i, chapter := range full.Chapters {
		chapter.Paragraphs = nil
		chapters[i] = chapter
		texts[i] = SearchText{ID: fmt.Sprintf("text-%d", i), Tokens: stored.tokens[i], Runes: stored.texts[i].Runes}
	}
	version := &Version{ProjectID: full.ProjectID, View: full.View, Fingerprint: full.Fingerprint, Chapters: chapters, byKey: full.byKey}
	return NewOutline(version, texts)
}

// wholeIndex is the index of every stored row of the version, with its paragraphs loaded: what
// the tests of ranking, snippets and links read.
func wholeIndex(t *testing.T, full *Version, stems map[string]string) *Index {
	t.Helper()
	stored := storeVersion(full, stems)
	outline := outlineOf(full, stored)
	index, err := NewSearchIndex(outline)
	if err != nil {
		t.Fatal(err)
	}
	var words []VocabularyWord
	var rows []WordPostings
	seen := map[string]bool{}
	for chapter, text := range stored.texts {
		for _, word := range text.Words {
			if !seen[word.Word] {
				seen[word.Word] = true
				words = append(words, VocabularyWord{Word: word.Word, Stem: word.Stem})
			}
			if word.Stem != "" {
				rows = append(rows, WordPostings{Chapter: chapter, Word: word.Word, Stem: word.Stem, Postings: word.Postings})
			}
		}
	}
	index.AddVocabulary(words)
	if err := index.AddPostings(rows); err != nil {
		t.Fatal(err)
	}
	// Like the reference index, words of the stems map outside the text (card words) stem by
	// the map; in a call they come from the vocabulary of the saga.
	for word, stem := range stems {
		if _, ok := index.stems[word]; !ok {
			index.stems[word] = stem
		}
	}
	for i := range full.Chapters {
		outline.Version.Chapters[i].Paragraphs = full.Chapters[i].Paragraphs
	}
	return index
}

// searchFromStore runs one query the way a search call does: lookup, vocabulary, stems,
// postings, then Run; paragraphs are loaded only for substring candidates and snippets.
func searchFromStore(t *testing.T, full *Version, stored storedSearch, query Query) (*Index, Result) {
	t.Helper()
	outline := outlineOf(full, stored)
	version := outline.Version
	index, err := NewSearchIndex(outline)
	if err != nil {
		t.Fatal(err)
	}
	load := func(chapter int) {
		for i, paragraph := range full.Chapters[chapter].Paragraphs {
			version.LoadParagraph(chapter, i, paragraph.Text)
		}
	}
	var result Result
	if query.Mode == ModeSubstring {
		chapters := index.SubstringChapters(query)
		if part := SubstringWordPart(query.Text); part != "" {
			var rows []WordPostings
			for chapter, text := range stored.texts {
				for _, word := range text.Words {
					if strings.Contains(word.Word, part) {
						rows = append(rows, WordPostings{Chapter: chapter, Word: word.Word, Stem: word.Stem, Postings: word.Postings})
					}
				}
			}
			candidates, err := index.SubstringCandidates(query, rows)
			if err != nil {
				t.Fatal(err)
			}
			chapters = nil
			for chapter, paragraphs := range candidates {
				for _, paragraph := range paragraphs {
					version.LoadParagraph(chapter, paragraph, full.Chapters[chapter].Paragraphs[paragraph].Text)
				}
				chapters = append(chapters, chapter)
			}
		} else {
			for _, chapter := range chapters {
				load(chapter)
			}
		}
		var hits []Hit
		for _, chapter := range chapters {
			hits = append(hits, index.SubstringHits(query, chapter)...)
		}
		result = index.SubstringResult(query, hits)
	} else {
		index.AddVocabulary(stored.vocabulary(index.Lookup([]string{query.Text}, query.Mode)))
		if err := index.AddPostings(stored.postings(index.QueryStems([]string{query.Text}, query.Mode))); err != nil {
			t.Fatal(err)
		}
		result = index.Run(query)
	}
	for _, hit := range result.Hits {
		chapter, _ := index.ParagraphsOf(hit.Para)
		load(chapter)
	}
	return index, result
}

func TestSearchFromStoredRowsMatchesTheWholeIndex(t *testing.T) {
	stems := map[string]string{}
	for word, stem := range testStems {
		stems[word] = stem
	}
	for word, stem := range map[string]string{"кутузов": "кутуз", "кутузова": "кутузов", "кутузову": "кутузов", "был": "", "это": "", "и": "", "с": "", "у": ""} {
		stems[word] = stem
	}
	version := testVersion()
	version.Chapters = append(version.Chapters, Chapter{ID: "c5", BookID: "b2", BookOrder: 2, Order: 3, Title: "Глава 90", Key: "90", Index: 4,
		Paragraphs: []Paragraph{{Text: "Брат — адъютант Кутузова, тихо-тихо."}, {Text: ""}, {Text: "Кутузов и Мирон. Письмо Кутузову, Мирона ждали."}}})
	version = NewVersion(version.ProjectID, version.View, version.Fingerprint, version.Chapters)
	whole := buildReferenceIndex(version, stems)
	stored := storeVersion(version, stems)
	queries := []Query{
		{Text: "Зимогорск", To: -1}, {Text: "Зимогорск", To: -1, Story: true}, {Text: "елки вьюжница", To: -1},
		{Text: `"друг по отряду"`, To: -1}, {Text: `"адъютант Кутузов"`, To: -1}, {Text: "друг отряду", Mode: ModePhrase, To: -1},
		{Text: "мирон капсулу", Mode: ModeAll, To: -1}, {Text: "мирон капсулу кутузов", To: -1}, {Text: "Кутузову", To: -1},
		{Text: "кутуз* зимо*", To: -1}, {Text: "был", To: -1}, {Text: "минибосс", To: -1}, {Text: "тихо", To: -1},
		{Text: "контроллер", Mode: ModeSubstring, To: -1}, {Text: "мини-босс", Mode: ModeSubstring, To: -1},
		{Text: "инибо", Mode: ModeSubstring, To: -1}, {Text: "ло и", Mode: ModeSubstring, To: -1}, {Text: "это", Mode: ModeSubstring, To: -1},
		{Text: "вьюжница", Mode: ModeSubstring, From: 3, To: 3},
		{Text: "вьюжница", From: 3, To: 3}, {Text: "мирон", To: -1, BookIDs: []string{"b2"}}, {Text: "...", Mode: ModeSubstring, To: -1},
		{Text: "нет такого слова", To: -1},
	}
	for _, query := range queries {
		want := whole.Run(query)
		index, got := searchFromStore(t, version, stored, query)
		if !sameResult(want, got) {
			t.Fatalf("%+v:\nwhole  %+v\nstored %+v", query, want, got)
		}
		for i := range want.Hits {
			if a, b := whole.Snippet(want.Hits[i], 12), index.Snippet(got.Hits[i], 12); a != b {
				t.Fatalf("%+v hit %d snippet %q, stored %q", query, i, a, b)
			}
		}
	}
}

// sameResult compares hits by paragraph, matched terms, exact score and snippet words.
func sameResult(a, b Result) bool {
	if !reflect.DeepEqual(a.Terms, b.Terms) || !reflect.DeepEqual(a.Chapters, b.Chapters) || len(a.Hits) != len(b.Hits) {
		return false
	}
	for i := range a.Hits {
		x, y := a.Hits[i], b.Hits[i]
		if x.Para != y.Para || x.Matched != y.Matched || math.Float64bits(x.Score) != math.Float64bits(y.Score) || !sameWords(x.words, y.words) {
			return false
		}
	}
	return true
}

func sameWords(a, b []string) bool {
	count := map[string]int{}
	for _, word := range a {
		count[word]++
	}
	for _, word := range b {
		count[word]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}
