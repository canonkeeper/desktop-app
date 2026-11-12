package textface

import (
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// Runs are the runs of a paragraph of a working text: its pieces in one face, the marks of the model's text gone.
func Runs(paragraph string) []chapterfile.Run {
	var runs runBuilder
	for _, current := range parse(paragraph) {
		if current.mark == 0 {
			runs.add(current.text, current.faces)
		}
	}
	return runs.done()
}

// runBuilder makes runs of pieces as they come: a piece in the face of the last run joins it, an empty one is nothing.
// The text of the last run grows in one buffer, so a chapter goes through it in linear time.
type runBuilder struct {
	runs  []chapterfile.Run
	text  strings.Builder
	faces face
}

func (builder *runBuilder) add(text string, faces face) {
	if text == "" {
		return
	}
	if builder.text.Len() > 0 && builder.faces != faces {
		builder.flush()
	}
	builder.faces = faces
	builder.text.WriteString(text)
}

func (builder *runBuilder) flush() {
	if builder.text.Len() > 0 {
		builder.runs = append(builder.runs, runOf(builder.text.String(), builder.faces))
		builder.text.Reset()
	}
}

// done is the runs made, the last one closed; the builder starts anew.
func (builder *runBuilder) done() []chapterfile.Run {
	builder.flush()
	runs := builder.runs
	builder.runs = nil
	return runs
}

// Marked writes runs as a paragraph of a working text: its faces between the fewest marks, the signs of the marks in
// the text of a run made brackets (TF1.5).
func Marked(runs []chapterfile.Run) string {
	var signs []sign
	for _, run := range runs {
		var faces face
		if run.Italic {
			faces |= italic
		}
		if run.Bold {
			faces |= bold
		}
		text := literalAll(run.Text)
		for at := 0; at < len(text); {
			_, size := utf8.DecodeRuneInString(text[at:])
			current := sign{text: text[at : at+size], faces: faces}
			// No face crosses a paragraph.
			if last := len(signs) - 1; current.text == "\n" && last >= 0 && signs[last].text == "\n" {
				signs[last].faces, current.faces = 0, 0
			}
			signs = append(signs, current)
			at += size
		}
	}
	return write(signs)
}

// delimiters are the stars of the Markdown subset that change faces: one italic, two bold, three both (TF1.2).
var delimiters = map[face]string{italic: "*", bold: "**", italic | bold: "***"}

// Markdown writes runs in the Markdown subset of a paragraph of the book (TF1.2): a star and a backslash of the text
// escaped, the faces between stars. Every change of the faces is one run of stars, so the book reads back exactly what
// was written.
func Markdown(runs []chapterfile.Run) string {
	var text strings.Builder
	var current face
	for _, run := range runs {
		if run.Text == "" {
			continue
		}
		var next face
		if run.Italic {
			next |= italic
		}
		if run.Bold {
			next |= bold
		}
		text.WriteString(delimiters[current^next])
		text.WriteString(strings.NewReplacer(`\`, `\\`, `*`, `\*`).Replace(run.Text))
		current = next
	}
	text.WriteString(delimiters[current])
	return text.String()
}

// token is a piece of a paragraph of the book: its text, or a run of stars that changes faces.
type token struct {
	text  string
	stars face
}

// FromMarkdown reads a paragraph of the book in the Markdown subset into runs (TF1.2). A run of one, two or three
// unescaped stars changes the faces; a star or a pair of stars that is not closed in the paragraph stays text, and so
// do four stars and more and a backslash before another sign.
func FromMarkdown(markdown string) []chapterfile.Run {
	var tokens []token
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			tokens = append(tokens, token{text: text.String()})
			text.Reset()
		}
	}
	for at := 0; at < len(markdown); {
		switch markdown[at] {
		case '\\':
			if at+1 < len(markdown) && (markdown[at+1] == '\\' || markdown[at+1] == '*') {
				text.WriteByte(markdown[at+1])
				at += 2
				continue
			}
			text.WriteByte('\\')
			at++
		case '*':
			count := 0
			for at+count < len(markdown) && markdown[at+count] == '*' {
				count++
			}
			if count > 3 {
				text.WriteString(markdown[at : at+count])
			} else {
				flush()
				tokens = append(tokens, token{stars: [4]face{0, italic, bold, italic | bold}[count]})
			}
			at += count
		default:
			_, size := utf8.DecodeRuneInString(markdown[at:])
			text.WriteString(markdown[at : at+size])
			at += size
		}
	}
	flush()
	// A face changed an odd number of times was not closed: its last change is text.
	for _, f := range []face{italic, bold} {
		last, count := -1, 0
		for i, current := range tokens {
			if current.stars&f != 0 {
				last, count = i, count+1
			}
		}
		if count%2 == 1 {
			tokens[last].stars &^= f
			tokens[last].text += delimiters[f]
		}
	}
	var runs runBuilder
	var current face
	for _, piece := range tokens {
		current ^= piece.stars
		runs.add(piece.text, current)
	}
	return runs.done()
}
