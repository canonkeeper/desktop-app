package sagamemory

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type Mode string

const (
	ModeAny       Mode = "any"
	ModeAll       Mode = "all"
	ModePhrase    Mode = "phrase"
	ModeSubstring Mode = "substring"
)

// Query selects paragraphs. From and To are reading-order chapter indexes (To < 0 means
// the last chapter); BookIDs limits the search to books; Story orders hits by reading order.
type Query struct {
	Text    string
	Mode    Mode
	From    int
	To      int
	BookIDs []string
	Story   bool
}

type Hit struct {
	Para    int
	Score   float64
	Matched int
	// Tokens is the length of the paragraph in words.
	Tokens int
	words  []string
}

type ChapterCount struct {
	Chapter int
	Count   int
}

// Result holds every matching paragraph in the requested order and the per-chapter
// distribution in reading order.
type Result struct {
	Terms    []string
	Hits     []Hit
	Chapters []ChapterCount
}

type term struct {
	display string
	seq     [][]string
}

func (index *Index) parse(text string, mode Mode) []term {
	norm := Normalize(text)
	var terms []term
	addWords := func(fragment string, phrase bool) {
		var seq [][]string
		var display []string
		prefix := !phrase && strings.HasSuffix(strings.TrimSpace(fragment), "*")
		for _, token := range Tokenize(fragment) {
			var alternatives []string
			if prefix {
				alternatives = index.prefixStems(token.Word)
			}
			if len(alternatives) == 0 {
				alternatives = index.wordStems(token.Word)
			}
			if len(alternatives) == 0 {
				continue
			}
			if phrase {
				seq = append(seq, alternatives)
				display = append(display, token.Word)
				continue
			}
			terms = append(terms, term{display: token.Word, seq: [][]string{alternatives}})
		}
		if phrase && len(seq) > 0 {
			terms = append(terms, term{display: strings.Join(display, " "), seq: seq})
		}
	}
	if mode == ModePhrase {
		addWords(strings.Trim(norm, `"«» `), true)
		return terms
	}
	rest := norm
	for {
		start := strings.IndexAny(rest, `"«`)
		if start < 0 {
			break
		}
		end := strings.IndexAny(rest[start+1:], `"»`)
		if end < 0 {
			break
		}
		addWords(rest[start+1:start+1+end], true)
		rest = rest[:start] + " " + rest[start+1+end+1:]
	}
	for _, field := range strings.Fields(rest) {
		addWords(field, false)
	}
	return terms
}

// Run executes one query against the index.
func (index *Index) Run(query Query) Result {
	mode := query.Mode
	if mode == "" {
		mode = ModeAny
	}
	inRange := index.rangeFilter(query)
	if mode == ModeSubstring {
		return index.runSubstring(query, inRange)
	}
	terms := index.parse(query.Text, mode)
	result := Result{}
	if len(terms) == 0 {
		return result
	}
	type acc struct {
		score   float64
		matched int
		words   []string
	}
	accs := map[int32]*acc{}
	for _, item := range terms {
		result.Terms = append(result.Terms, item.display)
		for para, score := range index.matchTerm(item) {
			if !inRange(int(para)) {
				continue
			}
			entry := accs[para]
			if entry == nil {
				entry = &acc{}
				accs[para] = entry
			}
			entry.score += score
			entry.matched++
			entry.words = append(entry.words, index.termWords(item)...)
		}
	}
	for para, entry := range accs {
		if (mode == ModeAll || mode == ModePhrase) && entry.matched < len(terms) {
			continue
		}
		result.Hits = append(result.Hits, Hit{Para: int(para), Score: entry.score, Matched: entry.matched, Tokens: index.docLen[para], words: entry.words})
	}
	index.finish(&result, query.Story)
	return result
}

func (index *Index) termWords(item term) []string {
	var words []string
	for _, element := range item.seq {
		for _, stem := range element {
			for word := range index.surface[stem] {
				words = append(words, word)
			}
		}
	}
	return words
}

// matchTerm scores paragraphs containing the term; a phrase requires each element to
// follow the previous one within three word positions (stop words are not indexed).
func (index *Index) matchTerm(item term) map[int32]float64 {
	scores := map[int32]float64{}
	if len(item.seq) == 1 {
		for _, stem := range item.seq[0] {
			for _, entry := range index.postings[stem] {
				scores[entry.para] += index.bm25(stem, entry)
			}
		}
		return scores
	}
	positions := map[int32][]int32{}
	for _, stem := range item.seq[0] {
		for _, entry := range index.postings[stem] {
			positions[entry.para] = append(positions[entry.para], entry.positions...)
		}
	}
	for _, element := range item.seq[1:] {
		next := map[int32][]int32{}
		for _, stem := range element {
			for _, entry := range index.postings[stem] {
				previous, ok := positions[entry.para]
				if !ok {
					continue
				}
				for _, pos := range entry.positions {
					for _, before := range previous {
						if pos > before && pos-before <= 3 {
							next[entry.para] = append(next[entry.para], pos)
							break
						}
					}
				}
			}
		}
		positions = next
	}
	for para, found := range positions {
		scores[para] = float64(len(found)) * 3
	}
	return scores
}

func (index *Index) runSubstring(query Query, inRange func(int) bool) Result {
	var hits []Hit
	for chapter := range index.Version.Chapters {
		hits = append(hits, index.substringHits(query, chapter, inRange)...)
	}
	return index.SubstringResult(query, hits)
}

func substringNeedle(text string) string {
	return joinWordHyphens(Normalize(strings.TrimSpace(text)))
}

// SubstringChapters lists the reading-order indexes of the chapters a substring query reads:
// those in its range and books. Their paragraphs must be loaded before SubstringHits.
func (index *Index) SubstringChapters(query Query) []int {
	if substringNeedle(query.Text) == "" {
		return nil
	}
	inRange := index.rangeFilter(query)
	var chapters []int
	for chapter := range index.Version.Chapters {
		if index.chapterStart[chapter] < index.chapterStart[chapter+1] && inRange(int(index.chapterStart[chapter])) {
			chapters = append(chapters, chapter)
		}
	}
	return chapters
}

// SubstringWordPart returns the longest run of letters and digits of a substring query's needle.
// A paragraph matches only if one of its words, or the joined form of a hyphenated word,
// contains that run, so a chapter without such a word can be skipped unread. It is "" when the
// needle has no letter or digit.
func SubstringWordPart(text string) string {
	longest := ""
	for _, token := range Tokenize(substringNeedle(text)) {
		if utf8.RuneCountInString(token.Word) > utf8.RuneCountInString(longest) {
			longest = token.Word
		}
	}
	return longest
}

// SubstringHits matches a substring query against the loaded paragraphs of one chapter.
func (index *Index) SubstringHits(query Query, chapter int) []Hit {
	return index.substringHits(query, chapter, index.rangeFilter(query))
}

func (index *Index) substringHits(query Query, chapter int, inRange func(int) bool) []Hit {
	needle := substringNeedle(query.Text)
	if needle == "" {
		return nil
	}
	var hits []Hit
	for id := int(index.chapterStart[chapter]); id < int(index.chapterStart[chapter+1]); id++ {
		if !inRange(id) {
			continue
		}
		_, paragraph := index.Locate(id)
		if count, matched := countIgnoringWordHyphens(paragraph.Norm, needle); count > 0 {
			hits = append(hits, Hit{Para: id, Score: float64(count), Matched: 1, words: []string{matched}})
		}
	}
	return hits
}

// SubstringResult orders the substring hits of all chapters like Run does.
func (index *Index) SubstringResult(query Query, hits []Hit) Result {
	result := Result{Terms: []string{substringNeedle(query.Text)}, Hits: hits}
	if result.Terms[0] == "" {
		return Result{Terms: result.Terms}
	}
	index.finish(&result, query.Story)
	return result
}

func (index *Index) rangeFilter(query Query) func(int) bool {
	books := map[string]bool{}
	for _, book := range query.BookIDs {
		books[book] = true
	}
	to := query.To
	if to < 0 || to >= len(index.Version.Chapters) {
		to = len(index.Version.Chapters) - 1
	}
	return func(id int) bool {
		ref := index.paras[id]
		if ref.chapter < query.From || ref.chapter > to {
			return false
		}
		return len(books) == 0 || books[index.Version.Chapters[ref.chapter].BookID]
	}
}

func (index *Index) finish(result *Result, story bool) {
	sort.Slice(result.Hits, func(i, j int) bool {
		a, b := result.Hits[i], result.Hits[j]
		if !story {
			if a.Matched != b.Matched {
				return a.Matched > b.Matched
			}
			if a.Score != b.Score {
				return a.Score > b.Score
			}
		}
		return a.Para < b.Para
	})
	counts := map[int]int{}
	for _, hit := range result.Hits {
		counts[index.paras[hit.Para].chapter]++
	}
	for chapter, count := range counts {
		result.Chapters = append(result.Chapters, ChapterCount{Chapter: chapter, Count: count})
	}
	sort.Slice(result.Chapters, func(i, j int) bool { return result.Chapters[i].Chapter < result.Chapters[j].Chapter })
}
