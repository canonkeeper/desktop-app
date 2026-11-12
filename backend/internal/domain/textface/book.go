package textface

import (
	"strings"
	"unicode"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// The text of a chapter between its places (specs/2026-11-08-italic-bold-in-text.md TF3, TF4): the working text of a
// draft and the paragraphs of the book in the Markdown subset, the author's file and the import of books.

// Lines are the paragraphs of a working text as the book and the file with revisions take them (TF3.2, TF7.2): every
// line with words, its edges trimmed, in the runs of its faces; the marks of the model's text go, and a face over a line
// break closes at the end of the line and opens again on the next.
func Lines(text string) []chapterfile.Paragraph {
	var lines []chapterfile.Paragraph
	var line runBuilder
	flush := func() {
		if trimmed := Trimmed(line.done()); len(trimmed) > 0 {
			lines = append(lines, chapterfile.Paragraph{Runs: trimmed})
		}
	}
	for _, run := range Runs(text) {
		for {
			before, after, found := strings.Cut(run.Text, "\n")
			line.add(before, faceOf(run))
			if !found {
				break
			}
			flush()
			run.Text = after
		}
	}
	flush()
	return lines
}

// Book is the paragraphs of the book from a working text (TF3.2): its lines in the Markdown subset; their plain texts are
// the lines of its plain text.
func Book(text string) []string {
	lines := Lines(text)
	paragraphs := make([]string, len(lines))
	for i, line := range lines {
		paragraphs[i] = Markdown(line.Runs)
	}
	return paragraphs
}

// FromBook is the working text of a chapter of the book (TF3.2): its paragraphs in the Markdown subset written as marks,
// a blank line between them.
func FromBook(paragraphs []string) string {
	lines := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		lines[i] = Marked(FromMarkdown(paragraph))
	}
	return strings.Join(lines, "\n\n")
}

// MarkdownPlain is the plain text of a paragraph of the book in the Markdown subset: what plain_text holds (TF1.2).
func MarkdownPlain(markdown string) string {
	return chapterfile.Paragraph{Runs: FromMarkdown(markdown)}.Text()
}

// OfDocument is the working text of a chapter from the author's file (TF4.3): its paragraphs with words in the marks of
// their runs, a blank line between them. Its plain text is the text of the document.
func OfDocument(document chapterfile.Document) string {
	var lines []string
	for _, paragraph := range document.Paragraphs {
		if strings.TrimSpace(paragraph.Text()) != "" {
			lines = append(lines, Marked(paragraph.Runs))
		}
	}
	return strings.Join(lines, "\n\n")
}

// MarkdownText writes a working text in the Markdown subset line by line (TF3.4): its faces between stars, a star and a
// backslash of the text escaped; the marks of the model's text stay as signs, in the faces both sides of them have.
func MarkdownText(text string) string {
	signs := parse(text)
	var lines []string
	var line runBuilder
	for i, current := range signs {
		if current.text == "\n" {
			lines = append(lines, Markdown(line.done()))
			continue
		}
		piece, faces := current.text, current.faces
		if current.mark != 0 {
			piece, faces = string(current.mark), facesAround(signs, i)
		}
		line.add(piece, faces)
	}
	lines = append(lines, Markdown(line.done()))
	return strings.Join(lines, "\n")
}

// facesAround are the faces the characters on both sides of a mark have, within its line.
func facesAround(signs []sign, at int) face {
	side := func(step int) face {
		for i := at + step; i >= 0 && i < len(signs); i += step {
			switch {
			case signs[i].text == "\n":
				return 0
			case signs[i].mark == 0:
				return signs[i].faces
			}
		}
		return 0
	}
	return side(-1) & side(1)
}

// Trimmed are runs without the spaces at their edges and without empty runs.
func Trimmed(runs []chapterfile.Run) []chapterfile.Run {
	out := make([]chapterfile.Run, 0, len(runs))
	for _, run := range runs {
		if run.Text != "" {
			out = append(out, run)
		}
	}
	for len(out) > 0 {
		out[0].Text = strings.TrimLeftFunc(out[0].Text, unicode.IsSpace)
		if out[0].Text != "" {
			break
		}
		out = out[1:]
	}
	for len(out) > 0 {
		last := len(out) - 1
		out[last].Text = strings.TrimRightFunc(out[last].Text, unicode.IsSpace)
		if out[last].Text != "" {
			break
		}
		out = out[:last]
	}
	return out
}

// Spaced are runs with every run of spaces one space and none at the edges, as the import of books takes a paragraph:
// a space takes the faces both signs around it have, so a face does not hold the space after it.
func Spaced(runs []chapterfile.Run) []chapterfile.Run {
	var out runBuilder
	pending, started := false, false
	var before face
	for _, run := range runs {
		faces := faceOf(run)
		for _, r := range run.Text {
			if unicode.IsSpace(r) {
				pending = started
				continue
			}
			if pending {
				out.add(" ", before&faces)
				pending = false
			}
			out.add(string(r), faces)
			before, started = faces, true
		}
	}
	return out.done()
}

// Onto puts the faces of runs onto a text with the same signs between other spaces (TF3.3): a sign that is not a space
// takes the faces of the same sign of the runs, a space the faces both signs around it have. ok is false when the signs
// other than spaces differ.
func Onto(runs []chapterfile.Run, text string) ([]chapterfile.Run, bool) {
	var source []sign
	for _, run := range runs {
		for _, r := range run.Text {
			if !unicode.IsSpace(r) {
				source = append(source, sign{text: string(r), faces: faceOf(run)})
			}
		}
	}
	signs := make([]sign, 0, len(text))
	next := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			signs = append(signs, sign{text: string(r), mark: ' '})
			continue
		}
		if next == len(source) || source[next].text != string(r) {
			return nil, false
		}
		signs = append(signs, sign{text: string(r), faces: source[next].faces})
		next++
	}
	if next != len(source) {
		return nil, false
	}
	return spacesBetween(signs), true
}

// SpacesOutside are runs whose spaces take the faces both signs around them have (TF4.1): a face neither starts nor ends
// with a space, as a reader of Markdown wants it, and a space at an edge has no face.
func SpacesOutside(runs []chapterfile.Run) []chapterfile.Run {
	var signs []sign
	for _, run := range runs {
		for _, r := range run.Text {
			current := sign{text: string(r), faces: faceOf(run)}
			if unicode.IsSpace(r) {
				current.mark = ' '
			}
			signs = append(signs, current)
		}
	}
	return spacesBetween(signs)
}

// spacesBetween are the runs of signs, a space — a sign marked ' ' — in the faces both signs around it have.
func spacesBetween(signs []sign) []chapterfile.Run {
	var out runBuilder
	var before face
	hasBefore := false
	for i := 0; i < len(signs); {
		if signs[i].mark != ' ' {
			out.add(signs[i].text, signs[i].faces)
			before, hasBefore = signs[i].faces, true
			i++
			continue
		}
		end := i
		for end < len(signs) && signs[end].mark == ' ' {
			end++
		}
		var faces face
		if hasBefore && end < len(signs) {
			faces = before & signs[end].faces
		}
		for ; i < end; i++ {
			out.add(signs[i].text, faces)
		}
	}
	return out.done()
}

func faceOf(run chapterfile.Run) face {
	var faces face
	if run.Italic {
		faces |= italic
	}
	if run.Bold {
		faces |= bold
	}
	return faces
}

func runOf(text string, faces face) chapterfile.Run {
	return chapterfile.Run{Text: text, Italic: faces&italic != 0, Bold: faces&bold != 0}
}
