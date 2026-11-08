package sagadraft

import (
	"errors"
	"slices"
	"strings"
	"unicode"
)

// The author's material and the marks of what he did not write (specs/2026-11-02-writer-workshop.md, W2.3, W2.4):
// a variant of a scene the author inserted stays between ⟦ and ⟧ until he makes it his own, and the scale «ваш
// материал» counts only his words, against the usual length of his chapters at that place.

// ErrInvalidMarks reports marks that are not closed or are nested.
var ErrInvalidMarks = errors.New("chapter draft: the marks of what the author did not write are broken")

// The signs of the marks.
const (
	MarkOpen  = '⟦'
	MarkClose = '⟧'
)

// TypicalChapters is how many last chapters before the place the usual length is taken from; with fewer than
// MinTypicalChapters there is none.
const (
	TypicalChapters    = 14
	MinTypicalChapters = 3
)

// ValidMarks says every mark is closed and none is inside another.
func ValidMarks(text string) bool {
	inside := false
	for _, r := range text {
		switch r {
		case MarkOpen:
			if inside {
				return false
			}
			inside = true
		case MarkClose:
			if !inside {
				return false
			}
			inside = false
		}
	}
	return !inside
}

// CheckMarks refuses a text with broken marks.
func CheckMarks(text string) error {
	if !ValidMarks(text) {
		return ErrInvalidMarks
	}
	return nil
}

// WithoutMarks is the text with the signs of the marks taken off: what a Job writes from.
func WithoutMarks(text string) string {
	return strings.NewReplacer(string(MarkOpen), "", string(MarkClose), "").Replace(text)
}

// WordsOf are the words of a text: runs of letters, marks and digits (W2.4), by the rule of the shared table
// testdata/words.json the page counts «Ваших слов» with.
func WordsOf(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsNumber(r) })
}

// AuthorWords counts the words outside the marks: the author's own material.
func AuthorWords(text string) int {
	var own strings.Builder
	inside := false
	for _, r := range text {
		switch {
		case r == MarkOpen:
			inside = true
			own.WriteRune(' ')
		case r == MarkClose:
			inside = false
			own.WriteRune(' ')
		case !inside:
			own.WriteRune(r)
		}
	}
	return len(WordsOf(own.String()))
}

// TypicalWords is the median of the words of the last chapters before the place, nil with fewer than three.
func TypicalWords(words []int) *int {
	if len(words) < MinTypicalChapters {
		return nil
	}
	sorted := slices.Sorted(slices.Values(words))
	middle := len(sorted) / 2
	median := sorted[middle]
	if len(sorted)%2 == 0 {
		median = (sorted[middle-1] + sorted[middle]) / 2
	}
	return &median
}
