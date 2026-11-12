// Package textface is the face of the text of a chapter — italic and bold — in its two forms
// (specs/2026-11-08-italic-bold-in-text.md, TF1): the marks of the working text of a draft, ⟨…⟩ for italic and ⟪…⟫ for
// bold beside the marks ⟦…⟧ of the model's text, and the Markdown subset of a paragraph of the book — *italic*,
// **bold**, ***both***, \* and \\. It reads and writes both, turns them into runs of a chapter file and back, projects a
// text onto its plain words with a map of offsets, checks the marks and makes them whole. The face is the author's look,
// not the sense: what reads for sense — models, search, counts — reads the plain projection. Pure functions, no I/O;
// the web keeps the same rules in src/shared/text/faces.ts with one table of examples, testdata/faces.json.
package textface

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// The signs of the marks of a working text (TF1.1, Data «метки_черновика»).
const (
	// WrittenOpen and WrittenClose hold what the model wrote (specs/2026-11-02-writer-workshop.md W2.3).
	WrittenOpen  = '⟦'
	WrittenClose = '⟧'
	ItalicOpen   = '⟨'
	ItalicClose  = '⟩'
	BoldOpen     = '⟪'
	BoldClose    = '⟫'
)

// face is a set of faces of a sign of the text.
type face uint8

const (
	italic face = 1 << iota
	bold
)

// IsMark says a sign is a mark of a working text: of the model's text or of a face.
func IsMark(r rune) bool { return r == WrittenOpen || r == WrittenClose || IsFace(r) }

// IsFace says a sign is a mark of a face.
func IsFace(r rune) bool {
	return r == ItalicOpen || r == ItalicClose || r == BoldOpen || r == BoldClose
}

// IsOpener says a sign opens a mark of any kind.
func IsOpener(r rune) bool { return r == WrittenOpen || r == ItalicOpen || r == BoldOpen }

// Pair says two signs open and close a mark of one kind.
func Pair(open, closing rune) bool {
	return open == WrittenOpen && closing == WrittenClose || open == ItalicOpen && closing == ItalicClose || open == BoldOpen && closing == BoldClose
}

// HasFaces says a text holds a mark of a face.
func HasFaces(text string) bool { return strings.ContainsFunc(text, IsFace) }

// opener and closer are the signs of a face.
func opener(f face) rune {
	if f == italic {
		return ItalicOpen
	}
	return BoldOpen
}

func closer(f face) rune {
	if f == italic {
		return ItalicClose
	}
	return BoldClose
}

// Literal is a text that came as text — from a model, a file, a paste — with the signs of the marks of faces made the
// brackets they look like (TF1.5): in a working text those signs are only marks.
func Literal(text string) string {
	if !HasFaces(text) {
		return text
	}
	return strings.NewReplacer("⟨", "‹", "⟩", "›", "⟪", "«", "⟫", "»").Replace(text)
}

// literalAll makes the signs of every mark brackets: a run of a file or of the book holds no mark.
func literalAll(text string) string {
	return strings.NewReplacer("⟨", "‹", "⟩", "›", "⟪", "«", "⟫", "»", "⟦", "[", "⟧", "]").Replace(text)
}

// Projection is a text with some of its marks taken off and the map of its bytes to the bytes of the text (TF1.3).
type Projection struct {
	Text string
	// toSource is the byte of the text each byte of Text came from, and one more for the end; nil when nothing was
	// taken off.
	toSource []int
}

// Plain is the simple projection: every mark off — what models, voice and counts read (TF2.1).
func Plain(text string) Projection { return project(text, IsMark) }

// Faceless takes only the marks of faces off, the marks of the model's text stay: what the helper reads of a working
// text and where a quote is found (TF2.1, TF2.2).
func Faceless(text string) Projection { return project(text, IsFace) }

func project(text string, off func(rune) bool) Projection {
	if !strings.ContainsFunc(text, off) {
		return Projection{Text: text}
	}
	var plain strings.Builder
	plain.Grow(len(text))
	toSource := make([]int, 0, len(text)+1)
	for at := 0; at < len(text); {
		r, size := utf8.DecodeRuneInString(text[at:])
		if !off(r) {
			plain.WriteString(text[at : at+size])
			for i := range size {
				toSource = append(toSource, at+i)
			}
		}
		at += size
	}
	return Projection{Text: plain.String(), toSource: append(toSource, len(text))}
}

// At is the byte of the text a byte of the projection stands at, or the end of the text for its end: right before
// that sign, after the marks before it.
func (projection Projection) At(at int) int {
	if projection.toSource == nil {
		return at
	}
	return projection.toSource[at]
}

// Range is a place of the projection as bytes of the text, the marks at its edges outside it: from its first sign to
// right after its last one (TF2.3).
func (projection Projection) Range(start, end int) (int, int) {
	if end <= start {
		at := projection.At(start)
		return at, at
	}
	return projection.At(start), projection.At(end-1) + 1
}

// Valid says the marks of a working text make a tree (TF1.1): every mark is closed in order, none is inside a mark of
// its kind, a face holds a sign and does not cross a paragraph (a blank line, \n\n). A block of the model's text may be
// empty, as it always could.
func Valid(text string) bool {
	var open []rune
	newline := false
	last := rune(0)
	faces := func() bool { return slices.ContainsFunc(open, IsFace) }
	for _, r := range text {
		switch {
		case IsOpener(r):
			if slices.Contains(open, r) {
				return false
			}
			if r != WrittenOpen && !faces() {
				newline = false
			}
			open = append(open, r)
		case IsMark(r):
			if len(open) == 0 || !Pair(open[len(open)-1], r) || r != WrittenClose && last == open[len(open)-1] {
				return false
			}
			open = open[:len(open)-1]
		case r == '\n':
			if newline && faces() {
				return false
			}
			newline = true
		default:
			newline = false
		}
		last = r
	}
	return len(open) == 0
}
