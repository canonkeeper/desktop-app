package sagamemory

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// The nearest paragraphs of a line of a chapter's plan, in the author's hand
// (specs/2026-11-01-writer-assistant-index.md, R6.2), are ranked by what is rare in the paragraphs they
// are taken from, as the blind exam measured: BM25 over that pool alone. The saga search keeps its own
// ranking, which puts the paragraphs with most of a question's words first.

// FunctionWords are the function words the writing exam counts and the ranking of the nearest paragraphs
// drops, normalized.
var FunctionWords = strings.Fields(`и в не на что с он а как но к по его это из у за от о так же бы то все
	она ему ли только если даже уже чтобы когда или еще для был при них вот тем более там после было этого
	того тоже просто хотя ведь впрочем однако поэтому пока лишь ни себя себе него`)

// minNearestWordRunes: a word of a line counts when it is longer than two letters.
const minNearestWordRunes = 3

// maxNearestShare: a stem found in more than one paragraph of the pool out of this many says nothing about
// what the line is about, and does not rank.
const maxNearestShare = 8

// minPostingBytesCap keeps a small saga from losing a word to the cap on the rows.
const minPostingBytesCap = 1024

// NearestQuery names the pool of a ranking, the paragraphs of the range and books of Query of at least
// MinTokens words, and asks for at most Limit of them near Query.Text. Query.Mode is not read.
type NearestQuery struct {
	Query
	MinTokens int
	Limit     int
}

// NearestText is a line as the words of the ranking: each word once, longer than two letters, without
// the function words, so that neither is looked up in the database nor do their rows load.
func NearestText(line string) string {
	seen := map[string]bool{}
	var words []string
	for _, token := range Tokenize(Normalize(line)) {
		if seen[token.Word] || utf8.RuneCountInString(token.Word) < minNearestWordRunes || slices.Contains(FunctionWords, token.Word) {
			continue
		}
		seen[token.Word] = true
		words = append(words, token.Word)
	}
	return strings.Join(words, " ")
}

// NearestPool is the size of the pool of a query and the chapters it lies in.
type NearestPool struct {
	Chapters   []int
	paragraphs int
	tokens     int
	inPool     func(id int) bool
}

// NearestPool sizes the pool of a query from the paragraph lengths.
func (index *Index) NearestPool(query NearestQuery) NearestPool {
	inRange := index.rangeFilter(query.Query)
	pool := NearestPool{inPool: func(id int) bool { return inRange(id) && index.docLen[id] >= query.MinTokens }}
	for chapter := range index.Version.Chapters {
		found := false
		for id := int(index.chapterStart[chapter]); id < int(index.chapterStart[chapter+1]); id++ {
			if pool.inPool(id) {
				found = true
				pool.paragraphs++
				pool.tokens += index.docLen[id]
			}
		}
		if found {
			pool.Chapters = append(pool.Chapters, chapter)
		}
	}
	return pool
}

// MaxPostingBytes is how many bytes the stored postings of a stem may hold to be worth reading. A
// paragraph takes 3 to 5 bytes of them, so a stem over one byte per paragraph of the pool is far over
// the share of maxNearestShare, and its rows, the largest of a saga, stay in the database.
func (pool NearestPool) MaxPostingBytes() int64 {
	return int64(max(pool.paragraphs, minPostingBytesCap))
}

// RankNearest ranks the pool over the word rows of the query's stems: BM25 with idf log(1+N/df), k1=1.2,
// b=0.75, the statistics taken from the pool alone, without the stems over the share. It returns the
// best limit paragraphs. Positions are not kept: a stem is decoded into its counts and dropped.
func (index *Index) RankNearest(pool NearestPool, limit int, rows []WordPostings) ([]Hit, error) {
	if pool.paragraphs == 0 {
		return nil, nil
	}
	const k1, b = 1.2, 0.75
	byStem := map[string][]WordPostings{}
	for _, row := range rows {
		if row.Chapter < 0 || row.Chapter >= len(index.Version.Chapters) || row.Stem == "" {
			return nil, ErrPostings
		}
		byStem[row.Stem] = append(byStem[row.Stem], row)
	}
	stems := make([]string, 0, len(byStem))
	for stem := range byStem {
		stems = append(stems, stem)
	}
	sort.Strings(stems)
	total, average := float64(pool.paragraphs), float64(pool.tokens)/float64(pool.paragraphs)
	scores := map[int32]float64{}
	for _, stem := range stems {
		counts := map[int32]int{}
		for _, row := range byStem[stem] {
			start, end := index.chapterStart[row.Chapter], index.chapterStart[row.Chapter+1]
			outside := false
			err := DecodePostings(row.Postings, func(paragraph int, positions []int32) {
				id := start + int32(paragraph)
				if id >= end {
					outside = true
				} else if pool.inPool(int(id)) {
					counts[id] += len(positions)
				}
			})
			if err != nil || outside {
				return nil, ErrPostings
			}
		}
		if len(counts) == 0 || len(counts) > max(1, pool.paragraphs/maxNearestShare) {
			continue
		}
		idf := math.Log(1 + total/float64(len(counts)))
		for id, count := range counts {
			tf, length := float64(count), float64(index.docLen[id])
			scores[id] += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*length/average))
		}
	}
	hits := make([]Hit, 0, len(scores))
	for id, score := range scores {
		hits = append(hits, Hit{Para: int(id), Score: score, Tokens: index.docLen[id]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Para < hits[j].Para
	})
	return hits[:min(limit, len(hits))], nil
}
