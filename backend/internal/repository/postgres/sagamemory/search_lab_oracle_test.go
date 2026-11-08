package sagamemory

// The search lab oracle: the paragraph index the backend kept in memory before
// specs/2026-10-28-saga-search-in-postgres.md, frozen as it was at 179a1cc4 (domain/sagamemory
// index.go, query.go, snippet.go and the hyphen helpers of text.go, renamed). The lab compares
// the stored-rows reader of the product with it, so a later change of the domain code cannot
// move the reference along with it.

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/sagamemory"
)

type oracleParaRef struct {
	chapter int
	para    int
}

type oraclePosting struct {
	para      int32
	positions []int32
}

// oracleIndex is the paragraph index of one text version: stems with word positions, BM25
// statistics and a sorted vocabulary for prefix queries.
type oracleIndex struct {
	Version  *domain.Version
	stems    map[string]string
	paras    []oracleParaRef
	postings map[string][]oraclePosting
	surface  map[string]map[string]struct{}
	vocab    []string
	docLen   []int
	avgLen   float64
}

// buildOracleIndex indexes every paragraph of the version. stems maps normalized words to
// russian_stem lexemes; an empty lexeme marks a stop word, a missing word stems to itself.
func buildOracleIndex(version *domain.Version, stems map[string]string) *oracleIndex {
	index := &oracleIndex{
		Version: version, stems: stems,
		postings: map[string][]oraclePosting{}, surface: map[string]map[string]struct{}{},
	}
	words := map[string]struct{}{}
	total := 0
	for chapterIndex := range version.Chapters {
		chapter := &version.Chapters[chapterIndex]
		for paraIndex := range chapter.Paragraphs {
			id := int32(len(index.paras))
			index.paras = append(index.paras, oracleParaRef{chapter: chapterIndex, para: paraIndex})
			byStem := map[string][]int32{}
			tokens := domain.Tokenize(chapter.Paragraphs[paraIndex].Norm)
			for _, token := range tokens {
				words[token.Word] = struct{}{}
				stem := index.stem(token.Word)
				if stem == "" {
					continue
				}
				byStem[stem] = append(byStem[stem], int32(token.Pos))
				if index.surface[stem] == nil {
					index.surface[stem] = map[string]struct{}{}
				}
				index.surface[stem][token.Word] = struct{}{}
			}
			for stem, positions := range byStem {
				index.postings[stem] = append(index.postings[stem], oraclePosting{para: id, positions: positions})
			}
			index.docLen = append(index.docLen, len(tokens))
			total += len(tokens)
		}
	}
	for word := range words {
		index.vocab = append(index.vocab, word)
	}
	sort.Strings(index.vocab)
	if len(index.paras) > 0 {
		index.avgLen = float64(total) / float64(len(index.paras))
	}
	return index
}

func (index *oracleIndex) stem(word string) string {
	if stem, ok := index.stems[word]; ok {
		return stem
	}
	return word
}

// Locate returns the chapter and paragraph of an indexed paragraph id.
func (index *oracleIndex) Locate(id int) (*domain.Chapter, *domain.Paragraph) {
	ref := index.paras[id]
	chapter := &index.Version.Chapters[ref.chapter]
	return chapter, &chapter.Paragraphs[ref.para]
}

func (index *oracleIndex) bm25(stem string, item oraclePosting) float64 {
	const k1, b = 1.2, 0.75
	df := float64(len(index.postings[stem]))
	n := float64(len(index.paras))
	idf := math.Log(1 + (n-df+0.5)/(df+0.5))
	tf := float64(len(item.positions))
	length := float64(index.docLen[item.para])
	return idf * tf * (k1 + 1) / (tf + k1*(1-b+b*length/index.avgLen))
}

// oracleCaseEndings are the noun and adjective case endings a word gains or loses across its forms.
var oracleCaseEndings = []string{"а", "у", "е", "ы", "и", "ю", "я", "ом", "ым", "ой", "ою", "ем", "ей", "ах", "ам", "ых", "ами", "ыми"}

// wordStems returns the stems a query word matches: its own and those of its case forms in
// the text. russian_stem cuts the nominative of a surname apart from its other cases
// ("кутузов" → "кутуз", "кутузова" → "кутузов"), so "Кутузов" also finds "Кутузова" and back.
func (index *oracleIndex) wordStems(word string) []string {
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
	for _, ending := range oracleCaseEndings {
		add(word + ending)
		if base, ok := strings.CutSuffix(word, ending); ok {
			add(base)
		}
	}
	return stems
}

func (index *oracleIndex) hasWord(word string) bool {
	at := sort.SearchStrings(index.vocab, word)
	return at < len(index.vocab) && index.vocab[at] == word
}

func (index *oracleIndex) prefixStems(prefix string) []string {
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

// oracleQuery selects paragraphs. From and To are reading-order chapter indexes (To < 0 means
// the last chapter); BookIDs limits the search to books; Story orders hits by reading order.
type oracleQuery struct {
	Text    string
	Mode    domain.Mode
	From    int
	To      int
	BookIDs []string
	Story   bool
}

type oracleHit struct {
	Para    int
	Score   float64
	Matched int
	words   []string
}

type oracleChapterCount struct {
	Chapter int
	Count   int
}

// oracleResult holds every matching paragraph in the requested order and the per-chapter
// distribution in reading order.
type oracleResult struct {
	Terms    []string
	Hits     []oracleHit
	Chapters []oracleChapterCount
}

type oracleTerm struct {
	display string
	seq     [][]string
}

func (index *oracleIndex) parse(text string, mode domain.Mode) []oracleTerm {
	norm := domain.Normalize(text)
	var terms []oracleTerm
	addWords := func(fragment string, phrase bool) {
		var seq [][]string
		var display []string
		prefix := !phrase && strings.HasSuffix(strings.TrimSpace(fragment), "*")
		for _, token := range domain.Tokenize(fragment) {
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
			terms = append(terms, oracleTerm{display: token.Word, seq: [][]string{alternatives}})
		}
		if phrase && len(seq) > 0 {
			terms = append(terms, oracleTerm{display: strings.Join(display, " "), seq: seq})
		}
	}
	if mode == domain.ModePhrase {
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
func (index *oracleIndex) Run(query oracleQuery) oracleResult {
	mode := query.Mode
	if mode == "" {
		mode = domain.ModeAny
	}
	inRange := index.rangeFilter(query)
	if mode == domain.ModeSubstring {
		return index.runSubstring(query, inRange)
	}
	terms := index.parse(query.Text, mode)
	result := oracleResult{}
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
		if (mode == domain.ModeAll || mode == domain.ModePhrase) && entry.matched < len(terms) {
			continue
		}
		result.Hits = append(result.Hits, oracleHit{Para: int(para), Score: entry.score, Matched: entry.matched, words: entry.words})
	}
	index.finish(&result, query.Story)
	return result
}

func (index *oracleIndex) termWords(item oracleTerm) []string {
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

// matchTerm scores paragraphs containing the oracleTerm; a phrase requires each element to
// follow the previous one within three word positions (stop words are not indexed).
func (index *oracleIndex) matchTerm(item oracleTerm) map[int32]float64 {
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

func (index *oracleIndex) runSubstring(query oracleQuery, inRange func(int) bool) oracleResult {
	needle := oracleJoinWordHyphens(domain.Normalize(strings.TrimSpace(query.Text)))
	result := oracleResult{Terms: []string{needle}}
	if needle == "" {
		return result
	}
	for id := range index.paras {
		if !inRange(id) {
			continue
		}
		_, paragraph := index.Locate(id)
		if count, matched := oracleCount(paragraph.Norm, needle); count > 0 {
			result.Hits = append(result.Hits, oracleHit{Para: id, Score: float64(count), Matched: 1, words: []string{matched}})
		}
	}
	index.finish(&result, query.Story)
	return result
}

func (index *oracleIndex) rangeFilter(query oracleQuery) func(int) bool {
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

func (index *oracleIndex) finish(result *oracleResult, story bool) {
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
		result.Chapters = append(result.Chapters, oracleChapterCount{Chapter: chapter, Count: count})
	}
	sort.Slice(result.Chapters, func(i, j int) bool { return result.Chapters[i].Chapter < result.Chapters[j].Chapter })
}

// Snippet cuts a window of the original paragraph text around the first matched word.
func (index *oracleIndex) Snippet(hit oracleHit, width int) string {
	_, paragraph := index.Locate(hit.Para)
	original := []rune(paragraph.Text)
	if len(original) <= width {
		return paragraph.Text
	}
	normToOrig := make([]int, 0, len(original))
	for i, r := range original {
		if r == '́' || r == '̀' {
			continue
		}
		normToOrig = append(normToOrig, i)
	}
	best := -1
	for _, word := range hit.words {
		if word == "" {
			continue
		}
		if at := strings.Index(paragraph.Norm, word); at >= 0 {
			runeAt := utf8.RuneCountInString(paragraph.Norm[:at])
			if best < 0 || runeAt < best {
				best = runeAt
			}
		}
	}
	center := 0
	if best >= 0 && best < len(normToOrig) {
		center = normToOrig[best]
	}
	start := max(0, center-width/3)
	end := min(len(original), start+width)
	start = max(0, end-width)
	text := strings.TrimSpace(string(original[start:end]))
	if start > 0 {
		text = "…" + text
	}
	if end < len(original) {
		text += "…"
	}
	return text
}

// oracleWordHyphen reports whether the hyphen at byte offset i joins two parts of one word.
func oracleWordHyphen(text string, i int) bool {
	before, _ := utf8.DecodeLastRuneInString(text[:i])
	after, _ := utf8.DecodeRuneInString(text[i+1:])
	return (unicode.IsLetter(before) || unicode.IsDigit(before)) && unicode.IsLetter(after)
}

// oracleJoinWordHyphens drops the hyphens inside words: "мини-контроллер" becomes "миниконтроллер".
func oracleJoinWordHyphens(text string) string {
	if !strings.Contains(text, "-") {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for i, r := range text {
		if r == '-' && oracleWordHyphen(text, i) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// oracleCount counts a needle without word hyphens in text whose words may be
// hyphenated, and returns the first matched span of the text.
func oracleCount(text, needle string) (int, string) {
	if !strings.Contains(text, "-") {
		return strings.Count(text, needle), needle
	}
	count, first := 0, ""
	_, firstSize := utf8.DecodeRuneInString(needle)
	for offset := 0; offset < len(text); {
		at := strings.Index(text[offset:], needle[:firstSize])
		if at < 0 {
			break
		}
		start := offset + at
		end, ok := oracleMatch(text, start, needle)
		if !ok {
			offset = start + firstSize
			continue
		}
		if count == 0 {
			first = text[start:end]
		}
		count++
		offset = end
	}
	return count, first
}

func oracleMatch(text string, start int, needle string) (int, bool) {
	i := start
	for j := 0; j < len(needle); {
		if i >= len(text) {
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		want, wantSize := utf8.DecodeRuneInString(needle[j:])
		switch {
		case r == want:
			i, j = i+size, j+wantSize
		case r == '-' && j > 0 && oracleWordHyphen(text, i):
			i += size
		default:
			return 0, false
		}
	}
	return i, true
}
