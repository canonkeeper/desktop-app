package chapterfile

import (
	"unicode"
	"unicode/utf8"
)

// The difference of two sequences for the file with revisions (PR6.2, PR6.3): paragraphs against paragraphs, then the
// tokens of a changed paragraph against each other, by their longest common subsequence.

type editKind int8

const (
	editEqual editKind = iota
	editDelete
	editInsert
)

// edit is one step of a difference: a of the original, b of the text now (the one its kind uses).
type edit struct {
	kind editKind
	a, b int
}

// maxTable bounds the table of one longest common subsequence: past it unique lines anchor the difference first.
const maxTable = 4_000_000

// difference is the edit script turning a into b: equal steps where they agree, deletions before insertions where they
// do not.
func difference(a, b []string) []edit {
	var edits []edit
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		edits = append(edits, edit{editEqual, prefix, prefix})
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	edits = append(edits, middle(a, b, prefix, len(a)-suffix, prefix, len(b)-suffix)...)
	for i := range suffix {
		edits = append(edits, edit{editEqual, len(a) - suffix + i, len(b) - suffix + i})
	}
	return edits
}

// middle is the difference of a[a0:a1] and b[b0:b1].
func middle(a, b []string, a0, a1, b0, b1 int) []edit {
	n, m := a1-a0, b1-b0
	switch {
	case n == 0 && m == 0:
		return nil
	case n == 0 || m == 0 || n*m > maxTable:
		if anchored := byAnchors(a, b, a0, a1, b0, b1); anchored != nil {
			return anchored
		}
		return replaced(a0, a1, b0, b1)
	}
	// lengths[i*(m+1)+j] is the longest common subsequence of a[a0+i:a1] and b[b0+j:b1].
	lengths := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[a0+i] == b[b0+j] {
				lengths[i*(m+1)+j] = lengths[(i+1)*(m+1)+j+1] + 1
			} else {
				lengths[i*(m+1)+j] = max(lengths[(i+1)*(m+1)+j], lengths[i*(m+1)+j+1])
			}
		}
	}
	var edits []edit
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[a0+i] == b[b0+j]:
			edits = append(edits, edit{editEqual, a0 + i, b0 + j})
			i, j = i+1, j+1
		case i < n && (j == m || lengths[(i+1)*(m+1)+j] >= lengths[i*(m+1)+j+1]):
			edits = append(edits, edit{kind: editDelete, a: a0 + i})
			i++
		default:
			edits = append(edits, edit{kind: editInsert, b: b0 + j})
			j++
		}
	}
	return edits
}

// replaced deletes a[a0:a1] and inserts b[b0:b1].
func replaced(a0, a1, b0, b1 int) []edit {
	var edits []edit
	for i := a0; i < a1; i++ {
		edits = append(edits, edit{kind: editDelete, a: i})
	}
	for j := b0; j < b1; j++ {
		edits = append(edits, edit{kind: editInsert, b: j})
	}
	return edits
}

// byAnchors splits a difference too large for one table at the lines that stand once in each range and in the same
// order (the longest increasing run of them), then takes the pieces between them one by one; nil without such lines.
func byAnchors(a, b []string, a0, a1, b0, b1 int) []edit {
	if a1 == a0 || b1 == b0 {
		return nil
	}
	count := func(items []string, from, to int) map[string]int {
		seen := map[string]int{}
		for i := from; i < to; i++ {
			seen[items[i]]++
		}
		return seen
	}
	inA, inB := count(a, a0, a1), count(b, b0, b1)
	where := map[string]int{}
	for j := b0; j < b1; j++ {
		if inB[b[j]] == 1 && inA[b[j]] == 1 {
			where[b[j]] = j
		}
	}
	type pair struct{ i, j int }
	var pairs []pair
	for i := a0; i < a1; i++ {
		if j, ok := where[a[i]]; ok {
			pairs = append(pairs, pair{i, j})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	// The longest increasing run of pairs by j (patience sorting).
	tails, previous := []int{}, make([]int, len(pairs))
	for k, item := range pairs {
		low, high := 0, len(tails)
		for low < high {
			mid := (low + high) / 2
			if pairs[tails[mid]].j < item.j {
				low = mid + 1
			} else {
				high = mid
			}
		}
		previous[k] = -1
		if low > 0 {
			previous[k] = tails[low-1]
		}
		if low == len(tails) {
			tails = append(tails, k)
		} else {
			tails[low] = k
		}
	}
	var chain []pair
	for k := tails[len(tails)-1]; k >= 0; k = previous[k] {
		chain = append([]pair{pairs[k]}, chain...)
	}
	var edits []edit
	i, j := a0, b0
	for _, anchor := range chain {
		edits = append(edits, middle(a, b, i, anchor.i, j, anchor.j)...)
		edits = append(edits, edit{editEqual, anchor.i, anchor.j})
		i, j = anchor.i+1, anchor.j+1
	}
	return append(edits, middle(a, b, i, a1, j, b1)...)
}

// span is a token of a paragraph: a word, a run of spaces or one other sign, by its byte offsets.
type span struct{ start, end int }

// tokens cut a paragraph into words (letters and digits, with a hyphen or an apostrophe inside), runs of spaces and
// single signs: a difference by them changes whole words and leaves the commas apart (PR6.3).
func tokens(text string) []span {
	var out []span
	for at := 0; at < len(text); {
		r, size := utf8.DecodeRuneInString(text[at:])
		end := at + size
		switch {
		case wordRune(r):
			for end < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[end:])
				if wordRune(next) {
					end += nextSize
					continue
				}
				if next == '-' || next == '\'' || next == '’' {
					after, _ := utf8.DecodeRuneInString(text[end+nextSize:])
					if end+nextSize < len(text) && wordRune(after) {
						end += nextSize
						continue
					}
				}
				break
			}
		case unicode.IsSpace(r):
			for end < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[end:])
				if !unicode.IsSpace(next) {
					break
				}
				end += nextSize
			}
		}
		out = append(out, span{at, end})
		at = end
	}
	return out
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}
