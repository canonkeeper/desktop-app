package sagamemory

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

type paraRef struct {
	chapter int
	para    int
}

type posting struct {
	para      int32
	positions []int32
}

// Index is the paragraph index of one text version as one search call needs it (UC2 of
// specs/2026-10-28-saga-search-in-postgres.md): the paragraph lengths of the whole version (BM25
// statistics) and, for its queries only, the vocabulary entries and the postings of their stems.
type Index struct {
	Version *Version
	stems   map[string]string
	paras   []paraRef
	// chapterStart[c] is the id of the first paragraph of chapter c; the last entry is len(paras).
	chapterStart []int32
	postings     map[string][]posting
	surface      map[string]map[string]struct{}
	vocab        []string
	docLen       []int
	avgLen       float64
	// lookup records the vocabulary a parse consults instead of answering it (SearchLookup).
	lookup *SearchLookup
}

func (index *Index) stem(word string) string {
	if index.lookup != nil {
		index.lookup.Words = append(index.lookup.Words, word)
		return word
	}
	if stem, ok := index.stems[word]; ok {
		return stem
	}
	return word
}

// Locate returns the chapter and paragraph of an indexed paragraph id.
func (index *Index) Locate(id int) (*Chapter, *Paragraph) {
	ref := index.paras[id]
	chapter := &index.Version.Chapters[ref.chapter]
	return chapter, &chapter.Paragraphs[ref.para]
}

func (index *Index) bm25(stem string, item posting) float64 {
	const k1, b = 1.2, 0.75
	df := float64(len(index.postings[stem]))
	n := float64(len(index.paras))
	idf := math.Log(1 + (n-df+0.5)/(df+0.5))
	tf := float64(len(item.positions))
	length := float64(index.docLen[item.para])
	return idf * tf * (k1 + 1) / (tf + k1*(1-b+b*length/index.avgLen))
}

// caseEndings are the noun and adjective case endings a word gains or loses across its forms.
var caseEndings = []string{"а", "у", "е", "ы", "и", "ю", "я", "ом", "ым", "ой", "ою", "ем", "ей", "ах", "ам", "ых", "ами", "ыми"}

// wordStems returns the stems a query word matches: its own and those of its case forms in
// the text. russian_stem cuts the nominative of a surname apart from its other cases
// ("кутузов" → "кутуз", "кутузова" → "кутузов"), so "Кутузов" also finds "Кутузова" and back.
func (index *Index) wordStems(word string) []string {
	own := index.stem(word)
	if own == "" {
		return nil
	}
	stems := []string{own}
	add := func(form string) {
		if utf8.RuneCountInString(form) < 3 || !index.hasWord(form) {
			return
		}
		if stem := index.stem(form); stem != "" && !slices.Contains(stems, stem) {
			stems = append(stems, stem)
		}
	}
	for _, ending := range caseEndings {
		add(word + ending)
		if base, ok := strings.CutSuffix(word, ending); ok {
			add(base)
		}
	}
	return stems
}

func (index *Index) hasWord(word string) bool {
	if index.lookup != nil {
		index.lookup.Words = append(index.lookup.Words, word)
		return true
	}
	at := sort.SearchStrings(index.vocab, word)
	return at < len(index.vocab) && index.vocab[at] == word
}

func (index *Index) prefixStems(prefix string) []string {
	if index.lookup != nil {
		index.lookup.Prefixes = append(index.lookup.Prefixes, prefix)
		return nil
	}
	start := sort.SearchStrings(index.vocab, prefix)
	seen := map[string]struct{}{}
	var stems []string
	for i := start; i < len(index.vocab) && strings.HasPrefix(index.vocab[i], prefix); i++ {
		stem := index.stem(index.vocab[i])
		if stem == "" {
			continue
		}
		if _, ok := seen[stem]; !ok {
			seen[stem] = struct{}{}
			stems = append(stems, stem)
		}
	}
	return stems
}
