package sagamemory

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

// A search index for one tool call (specs/2026-10-28-saga-search-in-postgres.md, UC2) holds
// only what its queries touch: the paragraph token counts of the version, the vocabulary
// entries the parser consults and the postings of the stems the queries match. Run, Snippet and
// the substring steps then work on it unchanged, so ranking stays that of the whole index.

// ErrSearchNotReady reports a text version with a chapter whose saga search rows are not built
// yet, as after a restore before they are rebuilt: the chat cannot read the saga until they are.
var ErrSearchNotReady = errors.New("saga memory: the search rows of the text version are not built")

// ErrTextChanged reports a paragraph of the call's text version that is gone when it is read, as
// when its chapter is deleted or rewritten after the outline: the call is retried on the new version.
var ErrTextChanged = errors.New("saga memory: the text version changed during the call")

// SearchText is the stored search text of one chapter (chapter_search_texts): its row id, the
// stored order and the token count of every paragraph in reading order and the rune size.
type SearchText struct {
	ID     string
	Orders []int32
	Tokens []int32
	Runes  int
}

// Outline is the text version of one call: its chapters without text and, by reading-order
// chapter index, their stored search texts; ID is "" for a chapter without one.
type Outline struct {
	Version *Version
	Texts   []SearchText
}

// NewOutline sizes the chapters of a version from their stored search texts.
func NewOutline(version *Version, texts []SearchText) *Outline {
	for i := range version.Chapters {
		version.Chapters[i].ParagraphCount = len(texts[i].Tokens)
		version.Chapters[i].Runes = texts[i].Runes
	}
	return &Outline{Version: version, Texts: texts}
}

// Ready reports whether every chapter of the version has its stored search text.
func (outline *Outline) Ready() bool {
	for _, text := range outline.Texts {
		if text.ID == "" {
			return false
		}
	}
	return true
}

// TextIDs lists the stored search texts of the version.
func (outline *Outline) TextIDs() []string {
	ids := make([]string, 0, len(outline.Texts))
	for _, text := range outline.Texts {
		if text.ID != "" {
			ids = append(ids, text.ID)
		}
	}
	return ids
}

// Chapters maps the stored search texts to reading-order chapter indexes.
func (outline *Outline) Chapters() map[string]int {
	chapters := make(map[string]int, len(outline.Texts))
	for i, text := range outline.Texts {
		chapters[text.ID] = i
	}
	return chapters
}

// SearchLookup is the vocabulary a parse consults: whole word forms and word prefixes.
type SearchLookup struct {
	Words    []string
	Prefixes []string
}

// VocabularyWord is one word of the version with its russian_stem lexeme ("" for a stop word).
type VocabularyWord struct {
	Word string
	Stem string
}

// WordPostings is one stored word row of a chapter in reading order.
type WordPostings struct {
	Chapter  int
	Word     string
	Stem     string
	Postings []byte
}

// NewSearchIndex builds the call's index of a text version from the stored token counts of its
// chapters (UC2: «индекс вызова»); it has no vocabulary or postings yet.
func NewSearchIndex(outline *Outline) (*Index, error) {
	if len(outline.Texts) != len(outline.Version.Chapters) {
		return nil, fmt.Errorf("saga memory: %d search texts for %d chapters", len(outline.Texts), len(outline.Version.Chapters))
	}
	if !outline.Ready() {
		return nil, ErrSearchNotReady
	}
	index := &Index{
		Version: outline.Version, stems: map[string]string{},
		postings: map[string][]posting{}, surface: map[string]map[string]struct{}{},
	}
	total := 0
	for chapter, text := range outline.Texts {
		index.chapterStart = append(index.chapterStart, int32(len(index.paras)))
		for paragraph, count := range text.Tokens {
			index.paras = append(index.paras, paraRef{chapter: chapter, para: paragraph})
			index.docLen = append(index.docLen, int(count))
			total += int(count)
		}
	}
	index.chapterStart = append(index.chapterStart, int32(len(index.paras)))
	if len(index.paras) > 0 {
		index.avgLen = float64(total) / float64(len(index.paras))
	}
	return index, nil
}

// Lookup returns the word forms and prefixes the parser of these queries may consult; it is a
// superset, since the recording parse never stops early.
func (index *Index) Lookup(texts []string, mode Mode) SearchLookup {
	if mode == ModeSubstring {
		return SearchLookup{}
	}
	if mode == "" {
		mode = ModeAny
	}
	lookup := SearchLookup{}
	index.lookup = &lookup
	for _, text := range texts {
		index.parse(text, mode)
	}
	index.lookup = nil
	slices.Sort(lookup.Words)
	slices.Sort(lookup.Prefixes)
	lookup.Words = slices.Compact(lookup.Words)
	lookup.Prefixes = slices.Compact(lookup.Prefixes)
	return lookup
}

// AddVocabulary adds the version's vocabulary entries found for a lookup: every looked-up word
// of the version and every word of the version starting with a looked-up prefix.
func (index *Index) AddVocabulary(words []VocabularyWord) {
	for _, word := range words {
		if _, ok := index.stems[word.Word]; !ok {
			index.vocab = append(index.vocab, word.Word)
		}
		index.stems[word.Word] = word.Stem
	}
	sort.Strings(index.vocab)
}

// QueryStems returns the stems the parsed queries match, once the vocabulary is added.
func (index *Index) QueryStems(texts []string, mode Mode) []string {
	if mode == ModeSubstring {
		return nil
	}
	if mode == "" {
		mode = ModeAny
	}
	var stems []string
	for _, text := range texts {
		for _, item := range index.parse(text, mode) {
			for _, element := range item.seq {
				stems = append(stems, element...)
			}
		}
	}
	slices.Sort(stems)
	return slices.Compact(stems)
}

// AddPostings adds the stored word rows of the matched stems: every word of the version with
// one of these stems, from every chapter. Postings of a stem are merged per paragraph in
// paragraph order, as the whole-version index laid them out.
func (index *Index) AddPostings(rows []WordPostings) error {
	byStem := map[string]map[int32][]int32{}
	for _, row := range rows {
		if row.Chapter < 0 || row.Chapter >= len(index.Version.Chapters) || row.Stem == "" {
			return ErrPostings
		}
		start, end := index.chapterStart[row.Chapter], index.chapterStart[row.Chapter+1]
		paragraphs := byStem[row.Stem]
		if paragraphs == nil {
			paragraphs = map[int32][]int32{}
			byStem[row.Stem] = paragraphs
		}
		failed := false
		err := DecodePostings(row.Postings, func(paragraph int, positions []int32) {
			id := start + int32(paragraph)
			if id >= end {
				failed = true
				return
			}
			paragraphs[id] = append(paragraphs[id], positions...)
		})
		if err != nil || failed {
			return ErrPostings
		}
		if index.surface[row.Stem] == nil {
			index.surface[row.Stem] = map[string]struct{}{}
		}
		index.surface[row.Stem][row.Word] = struct{}{}
	}
	for stem, paragraphs := range byStem {
		list := make([]posting, 0, len(paragraphs))
		for id, positions := range paragraphs {
			slices.Sort(positions)
			list = append(list, posting{para: id, positions: positions})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].para < list[j].para })
		index.postings[stem] = list
	}
	return nil
}

// ParagraphsOf returns the reading-order chapter and the 0-based paragraph of an indexed
// paragraph id.
func (index *Index) ParagraphsOf(id int) (int, int) {
	ref := index.paras[id]
	return ref.chapter, ref.para
}

// SubstringCandidates returns, by chapter, the paragraphs a substring query may match: those
// whose stored word rows (the words containing SubstringWordPart) list them, inside the query's
// range and books. Their texts must be loaded before SubstringHits.
func (index *Index) SubstringCandidates(query Query, rows []WordPostings) (map[int][]int, error) {
	inRange := index.rangeFilter(query)
	candidates := map[int]map[int]bool{}
	for _, row := range rows {
		if row.Chapter < 0 || row.Chapter >= len(index.Version.Chapters) {
			return nil, ErrPostings
		}
		start, end := index.chapterStart[row.Chapter], index.chapterStart[row.Chapter+1]
		failed := false
		err := DecodePostings(row.Postings, func(paragraph int, _ []int32) {
			id := start + int32(paragraph)
			if id >= end {
				failed = true
				return
			}
			if !inRange(int(id)) {
				return
			}
			if candidates[row.Chapter] == nil {
				candidates[row.Chapter] = map[int]bool{}
			}
			candidates[row.Chapter][paragraph] = true
		})
		if err != nil || failed {
			return nil, ErrPostings
		}
	}
	out := make(map[int][]int, len(candidates))
	for chapter, paragraphs := range candidates {
		for paragraph := range paragraphs {
			out[chapter] = append(out[chapter], paragraph)
		}
		slices.Sort(out[chapter])
	}
	return out, nil
}

// LoadParagraph puts one paragraph text (0-based) of a chapter into the version; the paragraphs
// not loaded stay empty.
func (version *Version) LoadParagraph(chapter, paragraph int, text string) {
	paragraphs := version.Chapters[chapter].Paragraphs
	if count := version.Chapters[chapter].ParagraphCount; len(paragraphs) != count {
		paragraphs = make([]Paragraph, count)
		for i := range paragraphs {
			paragraphs[i].N = i + 1
		}
		version.Chapters[chapter].Paragraphs = paragraphs
	}
	paragraphs[paragraph] = Paragraph{N: paragraph + 1, Text: text, Norm: Normalize(text)}
}
