package sagamemory

import (
	"encoding/binary"
	"errors"
	"sort"
	"unicode/utf8"
)

// The saga search keeps every chapter text in PostgreSQL as the rows built here
// (specs/2026-10-28-saga-search-in-postgres.md, UC1): one row per distinct normalized word
// with its russian_stem lexeme and postings, and the token count of every paragraph.

var ErrPostings = errors.New("saga memory: invalid postings")

// ChapterSearchText is the search data of one chapter text: the token count of each paragraph
// in reading order (BM25 lengths), the rune size of the text (scan chunks) and its words.
type ChapterSearchText struct {
	ParagraphTokens []int32
	Runes           int
	Words           []SearchWord
}

// SearchWord is one distinct normalized word of a chapter text. Stem is its russian_stem lexeme
// ("" for a stop word, which no query matches by stem); Postings encode the paragraphs and word
// positions of the word, so a substring query reads only paragraphs having a word that contains
// its letters (SubstringWordPart), stop words included.
type SearchWord struct {
	Word     string
	Stem     string
	Postings []byte
}

// ChapterSearchWords lists the distinct normalized words of chapter paragraphs, sorted, for
// stemming before BuildChapterSearchText.
func ChapterSearchWords(paragraphs []string) []string {
	seen := map[string]struct{}{}
	for _, text := range paragraphs {
		for _, token := range Tokenize(Normalize(text)) {
			seen[token.Word] = struct{}{}
		}
	}
	words := make([]string, 0, len(seen))
	for word := range seen {
		words = append(words, word)
	}
	sort.Strings(words)
	return words
}

type wordOccurrence struct {
	paragraph int
	positions []int32
}

// BuildChapterSearchText tokenizes the paragraphs of one chapter text exactly as the paragraph
// index does and returns its search rows. stems maps words to russian_stem lexemes; an empty
// lexeme marks a stop word and a missing word stems to itself, as in the whole-version index.
func BuildChapterSearchText(paragraphs []string, stems map[string]string) ChapterSearchText {
	byWord := map[string][]wordOccurrence{}
	text := ChapterSearchText{ParagraphTokens: make([]int32, len(paragraphs))}
	for paragraph, raw := range paragraphs {
		text.Runes += utf8.RuneCountInString(raw)
		tokens := Tokenize(Normalize(raw))
		text.ParagraphTokens[paragraph] = int32(len(tokens))
		for _, token := range tokens {
			list := byWord[token.Word]
			if n := len(list); n == 0 || list[n-1].paragraph != paragraph {
				list = append(list, wordOccurrence{paragraph: paragraph})
			}
			list[len(list)-1].positions = append(list[len(list)-1].positions, int32(token.Pos))
			byWord[token.Word] = list
		}
	}
	text.Words = make([]SearchWord, 0, len(byWord))
	for word, list := range byWord {
		stem, ok := stems[word]
		if !ok {
			stem = word
		}
		text.Words = append(text.Words, SearchWord{Word: word, Stem: stem, Postings: encodePostings(list)})
	}
	sort.Slice(text.Words, func(i, j int) bool { return text.Words[i].Word < text.Words[j].Word })
	return text
}

// Check verifies search rows that come from outside the text index Job (the operator load,
// UC4): words are distinct and not empty, postings decode inside the paragraphs, and the
// positions of every paragraph add up to its token count, as they do when the rows come from
// its text.
func (text ChapterSearchText) Check() error {
	counted := make([]int32, len(text.ParagraphTokens))
	seen := make(map[string]bool, len(text.Words))
	for _, word := range text.Words {
		if word.Word == "" || seen[word.Word] || len(word.Postings) == 0 {
			return ErrPostings
		}
		seen[word.Word] = true
		outside := false
		if err := DecodePostings(word.Postings, func(paragraph int, positions []int32) {
			if paragraph >= len(counted) {
				outside = true
				return
			}
			counted[paragraph] += int32(len(positions))
		}); err != nil || outside {
			return ErrPostings
		}
	}
	for paragraph, tokens := range text.ParagraphTokens {
		if counted[paragraph] != tokens {
			return ErrPostings
		}
	}
	return nil
}

// encodePostings writes, per paragraph in ascending order, the paragraph delta from the
// previous one (the first from -1), the number of positions and the position deltas.
func encodePostings(list []wordOccurrence) []byte {
	var data []byte
	previous := -1
	for _, item := range list {
		data = binary.AppendUvarint(data, uint64(item.paragraph-previous))
		data = binary.AppendUvarint(data, uint64(len(item.positions)))
		last := int32(0)
		for _, position := range item.positions {
			data = binary.AppendUvarint(data, uint64(position-last))
			last = position
		}
		previous = item.paragraph
	}
	return data
}

// DecodePostings reads postings written by BuildChapterSearchText in paragraph order; the
// positions slice passed to visit is reused between calls.
func DecodePostings(data []byte, visit func(paragraph int, positions []int32)) error {
	var positions []int32
	paragraph := -1
	read := func() (uint64, bool) {
		value, size := binary.Uvarint(data)
		if size <= 0 {
			return 0, false
		}
		data = data[size:]
		return value, true
	}
	for len(data) > 0 {
		delta, ok := read()
		if !ok || delta == 0 || delta > 1<<31 {
			return ErrPostings
		}
		paragraph += int(delta)
		count, ok := read()
		if !ok || count == 0 || count > uint64(len(data)) {
			return ErrPostings
		}
		positions = positions[:0]
		last := uint64(0)
		for range count {
			step, ok := read()
			if !ok || step > 1<<31 {
				return ErrPostings
			}
			last += step
			if last > 1<<31-1 {
				return ErrPostings
			}
			positions = append(positions, int32(last))
		}
		visit(paragraph, positions)
	}
	return nil
}
