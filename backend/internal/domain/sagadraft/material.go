package sagadraft

import (
	"errors"
	"slices"
	"strings"
	"unicode"

	"canonkeeper/backend/internal/domain/textface"
)

// The author's material and the marks of what he did not write (specs/2026-11-02-writer-workshop.md, W2.3, W2.4):
// a variant of a scene the author inserted stays between ⟦ and ⟧ until he makes it his own, and the scale «ваш
// материал» counts only his words, against the usual length of his chapters at that place. Beside them the working text
// carries the author's italic ⟨…⟩ and bold ⟪…⟫ (specs/2026-11-08-italic-bold-in-text.md, TF1): one tree of marks, and a
// mark of a face parts no word.

// ErrInvalidMarks reports marks that are not closed, are nested in their kind or cross one another.
var ErrInvalidMarks = errors.New("chapter draft: the marks of what the author did not write are broken")

// The signs of the marks of what the author did not write.
const (
	MarkOpen  = textface.WrittenOpen
	MarkClose = textface.WrittenClose
)

// TypicalChapters is how many last chapters before the place the usual length is taken from; with fewer than
// MinTypicalChapters there is none.
const (
	TypicalChapters    = 14
	MinTypicalChapters = 3
)

// ValidMarks says every mark is closed, none is inside another of its kind and the marks of all three kinds make a tree:
// a face is not empty and does not cross a paragraph (textface.Valid).
func ValidMarks(text string) bool {
	return textface.Valid(text)
}

// CheckMarks refuses a text with broken marks.
func CheckMarks(text string) error {
	if !ValidMarks(text) {
		return ErrInvalidMarks
	}
	return nil
}

// WithoutMarks is the text with the signs of the marks of all three kinds taken off — the plain projection: what a Job,
// a model and the voice read (TF2.1).
func WithoutMarks(text string) string {
	return textface.Plain(text).Text
}

// WithoutFaces is the working text with the marks of faces taken off and the marks ⟦…⟧ kept: what the helper, the
// check and the proofreading read, where their quotes stand (TF2.1, TF2.2).
func WithoutFaces(text string) string {
	return textface.Faceless(text).Text
}

// WordsOf are the words of a text: runs of letters, marks and digits (W2.4), by the rule of the shared table
// testdata/words.json the page counts «Ваших слов» with. A mark of a face stands inside a word without parting it
// (TF1.4); a mark of the model's text parts it, as it always did.
func WordsOf(text string) []string {
	return strings.FieldsFunc(WithoutFaces(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsNumber(r) })
}

// AuthorWords counts the words outside the marks of what the author did not write: his own material, whatever its face.
func AuthorWords(text string) int {
	var own strings.Builder
	inside := false
	for _, r := range WithoutFaces(text) {
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
