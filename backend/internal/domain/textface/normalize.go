package textface

import (
	"strings"
	"unicode/utf8"
)

// sign is a sign of a text read for its faces: a character with the faces it stands in, or a mark of the model's text.
type sign struct {
	text  string
	mark  rune
	faces face
}

// parse reads the faces of the signs of a text. An opener of a face adds it and a closer takes it away, as many times
// as they stand; a closer without its opener is nothing. A paragraph (a blank line) ends every face: a face never
// crosses it.
func parse(text string) []sign {
	signs := make([]sign, 0, len(text))
	var depth [2]int
	last := -1
	for at := 0; at < len(text); {
		r, size := utf8.DecodeRuneInString(text[at:])
		switch r {
		case ItalicOpen:
			depth[0]++
		case ItalicClose:
			depth[0] = max(depth[0]-1, 0)
		case BoldOpen:
			depth[1]++
		case BoldClose:
			depth[1] = max(depth[1]-1, 0)
		case WrittenOpen, WrittenClose:
			signs = append(signs, sign{mark: r})
		default:
			if r == '\n' && last >= 0 && signs[last].text == "\n" {
				signs[last].faces = 0
				depth = [2]int{}
			}
			var faces face
			if depth[0] > 0 {
				faces |= italic
			}
			if depth[1] > 0 {
				faces |= bold
			}
			signs = append(signs, sign{text: text[at : at+size], faces: faces})
			last = len(signs) - 1
		}
		at += size
	}
	return signs
}

// write writes signs with the fewest marks of faces that make a tree (TF1.1): a face open before a block of the model's
// text stays open over it only when every character of the block has it, a face of the block closes before its end;
// of two faces opening at once the one that lasts longer is outside, italic when they end together.
func write(signs []sign) string {
	var out strings.Builder
	var stack []face
	base := 0
	closeTo := func(height int) {
		for len(stack) > height {
			out.WriteRune(closer(stack[len(stack)-1]))
			stack = stack[:len(stack)-1]
		}
	}
	open := func(at int, faces face) {
		for _, f := range outerFirst(signs, at, faces) {
			out.WriteRune(opener(f))
			stack = append(stack, f)
		}
	}
	for i, current := range signs {
		switch current.mark {
		case WrittenOpen:
			end := i + 1
			for end < len(signs) && signs[end].mark != WrittenClose {
				end++
			}
			keep := 0
			for keep < len(stack) && covers(signs[i+1:end], stack[keep]) {
				keep++
			}
			closeTo(keep)
			// A face over the whole block that goes on after it opens before it, so it stays one span.
			if end+1 < len(signs) && signs[end+1].mark == 0 && hasCharacter(signs[i+1:end]) {
				var wanted face
				for _, f := range []face{italic, bold} {
					if !onStack(stack, f) && covers(signs[i+1:end], f) && signs[end+1].faces&f != 0 {
						wanted |= f
					}
				}
				open(i+1, wanted)
			}
			out.WriteRune(WrittenOpen)
			base = len(stack)
		case WrittenClose:
			closeTo(base)
			out.WriteRune(WrittenClose)
			base = 0
		default:
			for k, f := range stack {
				if current.faces&f == 0 {
					closeTo(k)
					break
				}
			}
			var missing face
			for _, f := range []face{italic, bold} {
				if current.faces&f != 0 && !onStack(stack, f) {
					missing |= f
				}
			}
			open(i, missing)
			out.WriteString(current.text)
		}
	}
	closeTo(0)
	return out.String()
}

func onStack(stack []face, f face) bool {
	for _, open := range stack {
		if open == f {
			return true
		}
	}
	return false
}

// covers says every character of signs has a face.
func covers(signs []sign, f face) bool {
	for _, current := range signs {
		if current.mark == 0 && current.faces&f == 0 {
			return false
		}
	}
	return true
}

func hasCharacter(signs []sign) bool {
	for _, current := range signs {
		if current.mark == 0 {
			return true
		}
	}
	return false
}

// outerFirst orders faces that open at once: the one whose characters go on longer from a sign is outside.
func outerFirst(signs []sign, at int, faces face) []face {
	switch faces {
	case 0:
		return nil
	case italic, bold:
		return []face{faces}
	}
	for _, current := range signs[at:] {
		if current.mark != 0 {
			continue
		}
		hasItalic, hasBold := current.faces&italic != 0, current.faces&bold != 0
		switch {
		case hasItalic && !hasBold:
			return []face{italic, bold}
		case hasBold && !hasItalic:
			return []face{bold, italic}
		case !hasItalic:
			return []face{italic, bold}
		}
	}
	return []face{italic, bold}
}

// Normalize makes the marks of faces of a working text whole (TF1.1): an empty face goes, two faces of one kind side by
// side are one; marks that still do not make a tree — a face crossing a block of the model's text or a paragraph, a
// face inside its kind, a mark without its pair — are written anew from the faces of the characters, a face cut at the
// border of the block. Marks that make a tree stay where they are, and so do the marks of the model's text and every
// other sign; a text without faces stays to the byte.
func Normalize(text string) string {
	if !HasFaces(text) {
		return text
	}
	for {
		tidy := joined.Replace(text)
		if tidy == text {
			break
		}
		text = tidy
	}
	if Valid(text) {
		return text
	}
	return write(parse(text))
}

// joined takes off an empty face and joins two faces of one kind that touch.
var joined = strings.NewReplacer("⟨⟩", "", "⟪⟫", "", "⟩⟨", "", "⟫⟪", "")

// Around are the marks of faces a piece of a text cuts (TF2.3): the closers of faces opened before it, which close
// before what takes its place, and the openers of faces it leaves open, which open after.
func Around(piece string) (string, string) {
	var closes strings.Builder
	var opens []rune
	for _, r := range piece {
		switch r {
		case ItalicOpen, BoldOpen:
			opens = append(opens, r)
		case ItalicClose, BoldClose:
			if last := len(opens) - 1; last >= 0 && Pair(opens[last], r) {
				opens = opens[:last]
			} else {
				closes.WriteRune(r)
			}
		}
	}
	return closes.String(), string(opens)
}

// Whole is a piece of a text with its faces whole (TF2.3): a face it starts inside opens at its start, a face it ends
// inside closes at its end.
func Whole(piece string) string {
	if !HasFaces(piece) {
		return piece
	}
	closes, opens := Around(piece)
	var before strings.Builder
	closing := []rune(closes)
	for i := len(closing) - 1; i >= 0; i-- {
		if closing[i] == ItalicClose {
			before.WriteRune(ItalicOpen)
		} else {
			before.WriteRune(BoldOpen)
		}
	}
	var after strings.Builder
	opening := []rune(opens)
	for i := len(opening) - 1; i >= 0; i-- {
		if opening[i] == ItalicOpen {
			after.WriteRune(ItalicClose)
		} else {
			after.WriteRune(BoldClose)
		}
	}
	return Normalize(before.String() + piece + after.String())
}

// Piece is a piece of a text, [start, end) in bytes, in the faces it stands in: a face open at its start opens before
// it, a face open at its end closes after it.
func Piece(text string, start, end int) string {
	_, open := Around(text[:start])
	return Whole(open + text[start:end])
}

// Replace is a text with a place of it, [start, end) in bytes, given way to new words (TF2.3): a face around the whole
// place gives them its face, a face the place cuts keeps what is left of it, an empty face goes. The new words are
// taken as they are: what came as text goes through Literal first.
func Replace(text string, start, end int, with string) string {
	closes, opens := Around(text[start:end])
	return Normalize(text[:start] + closes + with + opens + text[end:])
}
