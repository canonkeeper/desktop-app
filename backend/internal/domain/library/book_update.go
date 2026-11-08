package library

import (
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The plan of «Обновить из файла» (specs/2026-10-27-update-book-from-file.md, UC3–UC4): which chapter of the
// book each chapter of the new file is, and how its text changed. The update writes by it and the preview
// shows it, so both are the same plan.

// BookChapterText is a chapter of a book or of a file: its title and the plain text of its paragraphs.
type BookChapterText struct {
	Title      string
	Paragraphs []string
}

// TextChange is how the text of a chapter of the file meets the text of its chapter in the book.
type TextChange string

const (
	// TextSame is the same paragraphs up to spaces (R3.1).
	TextSame TextChange = "same"
	// TextPunctuation is the same words with other signs, letter case or «ё» (R3.2).
	TextPunctuation TextChange = "punctuation"
	// TextWords is another word or another number of paragraphs (R3.4).
	TextWords TextChange = "words"
)

// ChapterPairing is one chapter of the file: the index of its chapter in the book, -1 for a new one, how its
// text changed and whether its title did.
type ChapterPairing struct {
	Book    int
	Change  TextChange
	Renamed bool
}

// BookUpdatePlan pairs every chapter of the file in its order; Removed lists the chapters of the book the
// file no longer has.
type BookUpdatePlan struct {
	Chapters []ChapterPairing
	Removed  []int
}

// BookUpdateCounts counts a plan: by text the chapters left as they are, with other words, with other signs
// and new ones; renamed chapters are counted apart, whatever their text.
type BookUpdateCounts struct {
	Unchanged   int
	Words       int
	Punctuation int
	Renamed     int
	Added       int
	Removed     int
}

// RenamedTextSimilarity is the share of common word triples from which a chapter under another title is the
// same chapter (R4.1; the test TestBookUpdatePlanSimilarityThreshold measures it).
const RenamedTextSimilarity = 0.5

// renamedChapterReach is how far from its place in the file a renamed chapter is looked for, besides the
// difference of the numbers of chapters: «текст на том же месте почти тот же».
const renamedChapterReach = 5

// PlanBookUpdate pairs the chapters of a file with the chapters of a book (R4.1): the same words whatever the
// title, then the same title, then the most similar text near the same place; a chapter of the book is paired
// once.
func PlanBookUpdate(book, file []BookChapterText) BookUpdatePlan {
	bookTexts, fileTexts := chapterTexts(book), chapterTexts(file)
	plan := BookUpdatePlan{Chapters: make([]ChapterPairing, len(file))}
	used := make([]bool, len(book))
	for i := range plan.Chapters {
		plan.Chapters[i].Book = -1
	}
	pairBy := func(key func(chapterText) string) {
		free := map[string][]int{}
		for i := range book {
			if !used[i] {
				free[key(bookTexts[i])] = append(free[key(bookTexts[i])], i)
			}
		}
		for i := range file {
			if plan.Chapters[i].Book >= 0 {
				continue
			}
			candidates := free[key(fileTexts[i])]
			if len(candidates) == 0 {
				continue
			}
			plan.Chapters[i].Book, used[candidates[0]] = candidates[0], true
			free[key(fileTexts[i])] = candidates[1:]
		}
	}
	pairBy(func(text chapterText) string { return text.words })
	pairBy(func(text chapterText) string { return text.title })
	pairSimilar(plan.Chapters, used, bookTexts, fileTexts)
	for i, pairing := range plan.Chapters {
		if pairing.Book < 0 {
			continue
		}
		before, after := bookTexts[pairing.Book], fileTexts[i]
		plan.Chapters[i].Change = before.change(after)
		plan.Chapters[i].Renamed = before.title != after.title
	}
	for i := range book {
		if !used[i] {
			plan.Removed = append(plan.Removed, i)
		}
	}
	return plan
}

// Counts counts the plan.
func (plan BookUpdatePlan) Counts() BookUpdateCounts {
	counts := BookUpdateCounts{Removed: len(plan.Removed)}
	for _, pairing := range plan.Chapters {
		switch {
		case pairing.Book < 0:
			counts.Added++
		case pairing.Change == TextWords:
			counts.Words++
		case pairing.Change == TextPunctuation:
			counts.Punctuation++
		case !pairing.Renamed:
			counts.Unchanged++
		}
		if pairing.Renamed {
			counts.Renamed++
		}
	}
	return counts
}

// pairSimilar pairs the chapters left with the most similar chapter of the book near the same place, the most
// similar pairs first.
func pairSimilar(pairings []ChapterPairing, used []bool, book, file []chapterText) {
	reach := renamedChapterReach + abs(len(book)-len(file))
	type candidate struct {
		file, book int
		similarity float64
	}
	var candidates []candidate
	for i := range file {
		if pairings[i].Book >= 0 {
			continue
		}
		for j := max(0, i-reach); j < min(len(book), i+reach+1); j++ {
			if used[j] {
				continue
			}
			if similarity := shingleSimilarity(book[j].shingles(), file[i].shingles()); similarity >= RenamedTextSimilarity {
				candidates = append(candidates, candidate{file: i, book: j, similarity: similarity})
			}
		}
	}
	sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].similarity > candidates[b].similarity })
	for _, pair := range candidates {
		if pairings[pair.file].Book < 0 && !used[pair.book] {
			pairings[pair.file].Book, used[pair.book] = pair.book, true
		}
	}
}

// chapterText holds what the plan compares of one chapter.
type chapterText struct {
	title  string
	spaced []string // the paragraphs with one space for each run of spaces (R3.1)
	worded []string // the words of each paragraph (R3.2)
	words  string   // the words of the chapter, paragraph by paragraph
	triple []uint64 // the word triples of the chapter, sorted and unique; computed on demand
}

func chapterTexts(chapters []BookChapterText) []chapterText {
	texts := make([]chapterText, len(chapters))
	for i, chapter := range chapters {
		text := chapterText{title: titleKey(chapter.Title)}
		for _, paragraph := range chapter.Paragraphs {
			text.spaced = append(text.spaced, spacedText(paragraph))
			text.worded = append(text.worded, strings.Join(textWords(paragraph), " "))
		}
		text.words = strings.Join(text.worded, "\n")
		texts[i] = text
	}
	return texts
}

func (text chapterText) change(file chapterText) TextChange {
	switch {
	case slices.Equal(text.spaced, file.spaced):
		return TextSame
	case text.words == file.words:
		return TextPunctuation
	default:
		return TextWords
	}
}

func (text *chapterText) shingles() []uint64 {
	if text.triple == nil {
		text.triple = wordShingles(strings.Fields(text.words))
	}
	return text.triple
}

// TextSimilarity is the share of common word triples of two texts (the Jaccard coefficient of their sets).
func TextSimilarity(a, b []string) float64 {
	return shingleSimilarity(chapterTexts([]BookChapterText{{Paragraphs: a}})[0].shingles(), chapterTexts([]BookChapterText{{Paragraphs: b}})[0].shingles())
}

func shingleSimilarity(a, b []uint64) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	common := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			common++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(common) / float64(len(a)+len(b)-common)
}

// wordShingles hashes the triples of words in a row; a text of fewer than three words gives its words.
func wordShingles(words []string) []uint64 {
	size := min(3, len(words))
	shingles := make([]uint64, 0, len(words))
	for i := 0; size > 0 && i+size <= len(words); i++ {
		hash := fnv.New64a()
		for _, word := range words[i : i+size] {
			_, _ = hash.Write([]byte(word))
			_, _ = hash.Write([]byte{0})
		}
		shingles = append(shingles, hash.Sum64())
	}
	slices.Sort(shingles)
	shingles = slices.Compact(shingles)
	return slices.Clip(shingles)
}

func titleKey(title string) string {
	return strings.ToLower(spacedText(title))
}

// spacedText replaces every run of spaces (a no-break and a narrow no-break space, a tab and a line break too)
// with one space and drops the spaces at the edges.
func spacedText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// textWords lists the words of a text: letters and digits in a row, lower case, «ё» as «е»; signs and stress
// marks are not part of a word.
func textWords(text string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == 'ё' || r == 'Ё':
			word.WriteRune('е')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(unicode.ToLower(r))
		case unicode.Is(unicode.Mn, r):
		default:
			flush()
		}
	}
	flush()
	return words
}

// ChangePlace is one place a chapter changes in: the words around it before and after the update.
type ChangePlace struct {
	Before string
	After  string
}

// MaxChangePlaceRunes bounds each side of a place (R6.2).
const MaxChangePlaceRunes = 160

// changePlaceContext is how much of the unchanged text a place shows around a change.
const changePlaceContext = 40

// ChangePlaces finds up to limit places where the paragraphs of a chapter changed beyond spaces: paragraph by
// paragraph while their number is the same, otherwise the block between the paragraphs both sides share.
func ChangePlaces(before, after []string, limit int) []ChangePlace {
	old, next := make([]string, len(before)), make([]string, len(after))
	for i, paragraph := range before {
		old[i] = spacedText(paragraph)
	}
	for i, paragraph := range after {
		next[i] = spacedText(paragraph)
	}
	start := 0
	for start < len(old) && start < len(next) && old[start] == next[start] {
		start++
	}
	end := 0
	for end < len(old)-start && end < len(next)-start && old[len(old)-1-end] == next[len(next)-1-end] {
		end++
	}
	old, next = old[start:len(old)-end], next[start:len(next)-end]
	if len(old) != len(next) {
		if len(old) == 0 && len(next) == 0 || limit < 1 {
			return nil
		}
		return []ChangePlace{changePlace(strings.Join(old, " "), strings.Join(next, " "))}
	}
	var places []ChangePlace
	for i := range old {
		if len(places) == limit {
			break
		}
		if old[i] != next[i] {
			places = append(places, changePlace(old[i], next[i]))
		}
	}
	return places
}

// changePlace cuts two texts to what differs and some words around it.
func changePlace(before, after string) ChangePlace {
	a, b := []rune(before), []rune(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	from := wordStart(a, max(0, prefix-changePlaceContext))
	return ChangePlace{
		Before: placeText(a, from, len(a)-suffix),
		After:  placeText(b, from, len(b)-suffix),
	}
}

// placeText takes the text from a word start to the end of the change and some words after it, with «…» where
// it is cut, at most MaxChangePlaceRunes long.
func placeText(text []rune, from, changeEnd int) string {
	to := wordEnd(text, min(len(text), changeEnd+changePlaceContext))
	var place strings.Builder
	if from > 0 {
		place.WriteString("…")
	}
	body := strings.TrimSpace(string(text[from:to]))
	cut := to < len(text)
	if room := MaxChangePlaceRunes - utf8.RuneCountInString(place.String()) - 1; utf8.RuneCountInString(body) > room {
		body, cut = strings.TrimSpace(string([]rune(body)[:room])), true
	}
	place.WriteString(body)
	if cut && body != "" {
		place.WriteString("…")
	}
	return place.String()
}

func wordStart(text []rune, at int) int {
	for at > 0 && !unicode.IsSpace(text[at-1]) {
		at--
	}
	return at
}

func wordEnd(text []rune, at int) int {
	for at < len(text) && !unicode.IsSpace(text[at]) {
		at++
	}
	return at
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
