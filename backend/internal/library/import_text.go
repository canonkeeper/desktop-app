package library

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// ParseText reads a book kept as a plain text or as Markdown (specs/2026-11-10-book-file-kinds.md K2): the text is
// decoded as a text of a chapter is (UTF-8, UTF-16 by its mark, otherwise Windows-1251), its paragraphs are its lines —
// or the blocks of its lines between blank lines when the lines are cut by width — and its chapters start at the lines
// that name them and, in Markdown, at the headings of the first level. Markdown keeps its italic and bold; its links,
// lists, quotes and lower headings are read as text without their signs.
func ParseText(data []byte, markdown bool) (BookImport, error) {
	text, err := decodeBookText(data)
	if err != nil {
		return BookImport{}, err
	}
	layout := textLayoutOf(text, markdown)
	return bookOfLines(func(visit func(bookLine) error) error {
		return scanTextParagraphs(text, markdown, layout, visit)
	})
}

// decodeBookText decodes a text by the rule of a text of a chapter (chapterfile.DecodeText, K2.1); a text that no
// encoding of the rule reads without signs of replacement is in an encoding the import does not read.
func decodeBookText(data []byte) (string, error) {
	text, err := chapterfile.DecodeText(data)
	if err != nil || !utf8.Valid(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})) && strings.ContainsRune(text, utf8.RuneError) {
		return "", errImportUnsupportedEncoding
	}
	return text, nil
}

// eachLine calls visit with every line of a text; a line ends at "\n", "\r\n" or "\r".
func eachLine(text string, visit func(string) error) error {
	for len(text) > 0 {
		end := strings.IndexAny(text, "\r\n")
		if end < 0 {
			return visit(text)
		}
		next := end + 1
		if text[end] == '\r' && next < len(text) && text[next] == '\n' {
			next++
		}
		if err := visit(text[:end]); err != nil {
			return err
		}
		text = text[next:]
	}
	return nil
}

// textLineKind is what a line of a text is to its paragraphs.
type textLineKind uint8

const (
	textBlank textLineKind = iota
	// textProse is a line of prose: in a file cut by width it goes on the paragraph of the line before it.
	textProse
	// textItem is an item of a list of Markdown: it starts a paragraph.
	textItem
	// textAlone is a paragraph by itself: a heading, a line that names a chapter, a scene break, a line of code.
	textAlone
	// textSkipped is a line with no text of the book: a fence of code, the definition of a link.
	textSkipped
)

type textLine struct {
	kind textLineKind
	// text is the words of the line without its edges and without the signs of Markdown that are not faces.
	text string
	// indented says the line starts with a space or a tab: the mark of a paragraph in many texts cut by width.
	indented bool
	heading  bool
	// plain is a line read without faces: a scene break, a line of code.
	plain bool
}

var (
	markdownHeading    = regexp.MustCompile(`^(#{1,6})(?:\s+|$)`)
	markdownListItem   = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])\s+`)
	markdownDefinition = regexp.MustCompile(`^\[[^\]]+\]:\s*\S`)
)

// textLines reads the lines of a text one by one; a Markdown fence of code is a state of its reading.
type textLines struct {
	markdown bool
	fenced   bool
}

func (lines *textLines) read(raw string) textLine {
	raw = cleanTextLine(raw)
	trimmed := strings.TrimSpace(raw)
	indented := trimmed != "" && raw[0] != trimmed[0]
	if lines.markdown && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
		lines.fenced = !lines.fenced
		return textLine{kind: textSkipped}
	}
	switch {
	case trimmed == "":
		return textLine{kind: textBlank}
	case lines.fenced:
		return textLine{kind: textAlone, text: trimmed, plain: true}
	case isSceneBreak(trimmed):
		return textLine{kind: textAlone, text: trimmed, plain: true}
	case !lines.markdown:
		if chapterTitleLine(trimmed) {
			return textLine{kind: textAlone, text: trimmed}
		}
		return textLine{kind: textProse, text: trimmed, indented: indented}
	}
	for strings.HasPrefix(trimmed, ">") {
		trimmed = strings.TrimSpace(trimmed[1:])
	}
	if trimmed == "" {
		return textLine{kind: textBlank}
	}
	if match := markdownHeading.FindStringSubmatch(trimmed); match != nil {
		heading := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trimmed[len(match[0]):]), "#"))
		if heading == "" {
			return textLine{kind: textSkipped}
		}
		return textLine{kind: textAlone, text: heading, heading: len(match[1]) == 1}
	}
	if markdownDefinition.MatchString(trimmed) {
		return textLine{kind: textSkipped}
	}
	if match := markdownListItem.FindString(trimmed); match != "" {
		return textLine{kind: textItem, text: strings.TrimSpace(trimmed[len(match):])}
	}
	if chapterTitleLine(chapterfile.Paragraph{Runs: chapterfile.MarkdownRuns(markdownPlain(trimmed))}.Text()) {
		return textLine{kind: textAlone, text: trimmed}
	}
	return textLine{kind: textProse, text: trimmed, indented: indented}
}

// cleanTextLine drops the signs a text cannot show: the marks of the byte order and the control signs; a page or a line
// feed inside a line is a space.
func cleanTextLine(line string) string {
	if strings.IndexFunc(line, func(r rune) bool { return r == '\uFEFF' || r != '\t' && unicode.IsControl(r) }) < 0 {
		return line
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\f' || r == '\v':
			return ' '
		case r == '\uFEFF' || r != '\t' && unicode.IsControl(r):
			return -1
		}
		return r
	}, line)
}

// isSceneBreak says a line is a break between scenes: only stars, dashes, signs like them and spaces («* * *», «***»).
func isSceneBreak(line string) bool {
	return strings.Trim(line, "*-_=~#•·+ ") == "" && utf8.RuneCountInString(line) >= 3
}

// textLayout is how the paragraphs of a text are marked (K2.2).
type textLayout struct {
	// wrapped says the lines are cut by width: a paragraph is a block of lines.
	wrapped bool
	// indents says an indented line starts a paragraph, as a blank line does.
	indents bool
}

// minFullLineRunes is the length from which a line of a text is not cut by width: most lines of a text cut by width are
// shorter.
const minFullLineRunes = 100

// textLayoutOf measures how the paragraphs of a text are marked (K2.2): its lines are cut by width when most of its lines
// with words are shorter than 100 signs, its paragraphs are marked — by blank lines or by an indent where the other lines
// have none — and most lines that go on with another line of prose stop inside a sentence. Otherwise every line with
// words is a paragraph.
func textLayoutOf(text string, markdown bool) textLayout {
	lines, indented, short := 0, 0, 0
	blanks := false
	reader := textLines{markdown: markdown}
	_ = eachLine(text, func(raw string) error {
		switch line := reader.read(raw); line.kind {
		case textProse, textItem:
			lines++
			if line.indented {
				indented++
			}
			if utf8.RuneCountInString(line.text) < minFullLineRunes {
				short++
			}
		case textBlank:
			blanks = true
		}
		return nil
	})
	layout := textLayout{indents: indented > 0 && indented < lines}
	if short*2 <= lines || !blanks && !layout.indents {
		return layout
	}
	continued, open, marks := 0, 0, 0
	var previous textLine
	reader = textLines{markdown: markdown}
	_ = eachLine(text, func(raw string) error {
		line := reader.read(raw)
		switch {
		case line.kind == textProse && previous.kind == textProse && !(layout.indents && line.indented):
			continued++
			if !endsSentence(previous.text) {
				open++
			}
		case line.kind == textProse || line.kind == textItem:
			marks++
		}
		previous = line
		return nil
	})
	layout.wrapped = continued > 0 && open*2 > continued && marks >= 2
	return layout
}

// endsSentence says a line of prose ends where a sentence or a line of a dialogue may end.
func endsSentence(line string) bool {
	last, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(line))
	return strings.ContainsRune(`.!?…:;»"”)`, last)
}

// startsDialogue says a line of prose starts a line of a dialogue.
func startsDialogue(line string) bool {
	first, _ := utf8.DecodeRuneInString(line)
	return first == '—' || first == '–' || first == '-'
}

// scanTextParagraphs passes the paragraphs of a text to visit: every line with words, or in a text cut by width every
// block of lines up to a blank line, a line that stands alone or an indent, its lines joined by a space; a line of a
// dialogue after a line that ends a sentence starts a paragraph too. A blank line is passed as a blank paragraph.
func scanTextParagraphs(text string, markdown bool, layout textLayout, visit func(bookLine) error) error {
	reader := textLines{markdown: markdown}
	var block []string
	flush := func() error {
		if len(block) == 0 {
			return nil
		}
		joined := strings.Join(block, " ")
		block = block[:0]
		return visit(textParagraph(textLine{text: joined}, markdown))
	}
	err := eachLine(text, func(raw string) error {
		line := reader.read(raw)
		if !layout.wrapped {
			switch line.kind {
			case textBlank:
				return visit(bookLine{})
			case textSkipped:
				return nil
			default:
				return visit(textParagraph(line, markdown))
			}
		}
		switch line.kind {
		case textBlank:
			if err := flush(); err != nil {
				return err
			}
			return visit(bookLine{})
		case textSkipped:
			return flush()
		case textAlone:
			if err := flush(); err != nil {
				return err
			}
			return visit(textParagraph(line, markdown))
		case textItem:
			if err := flush(); err != nil {
				return err
			}
		default:
			if len(block) > 0 && (layout.indents && line.indented || startsDialogue(line.text) && endsSentence(block[len(block)-1])) {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		block = append(block, line.text)
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// textParagraph is a line or a block of a text as a paragraph: the faces of Markdown read, a plain line as it is.
func textParagraph(line textLine, markdown bool) bookLine {
	if !markdown || line.plain {
		return bookLine{runs: []chapterfile.Run{{Text: line.text}}}
	}
	return bookLine{runs: chapterfile.MarkdownRuns(markdownPlain(line.text)), heading: line.heading}
}

// markdownPlain is a line of Markdown without the signs of its links, pictures, notes and code (K2.5): [текст](адрес),
// [текст][метка] and [[Имя|подпись]] are their words, a picture ![…](…) and a note [^1] are nothing, <адрес> is the
// address. A sign after a backslash is left for the faces.
func markdownPlain(line string) string {
	if !strings.ContainsAny(line, "[<`") {
		return line
	}
	out := make([]byte, 0, len(line))
	// dropped leaves one space where a note or a picture stood between two.
	dropped := func(next int) int {
		if len(out) > 0 && out[len(out)-1] == ' ' && next < len(line) && line[next] == ' ' {
			return next + 1
		}
		return next
	}
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line) && line[i+1] < utf8.RuneSelf:
			out = append(out, line[i:i+2]...)
			i += 2
			continue
		case c == '`':
			i++
			continue
		case c == '<':
			if end := strings.IndexByte(line[i:], '>'); end > 1 {
				address := line[i+1 : i+end]
				if !strings.ContainsAny(address, " \t") && (strings.Contains(address, "://") || strings.Contains(address, "@")) {
					out = append(out, address...)
					i += end + 1
					continue
				}
			}
		case c == '!' && strings.HasPrefix(line[i+1:], "["):
			if _, end, ok := markdownLink(line, i+1); ok {
				i = dropped(end)
				continue
			}
		case c == '[':
			if label, end, ok := markdownLink(line, i); ok {
				if label == "" {
					end = dropped(end)
				}
				out = append(out, label...)
				i = end
				continue
			}
		}
		out = append(out, c)
		i++
	}
	return string(out)
}

// markdownLink reads a link that starts at line[start] == '[': its words and the end of it. A link of a wiki
// [[Имя|подпись]] gives its caption or its name, a note [^1] gives nothing.
func markdownLink(line string, start int) (string, int, bool) {
	if strings.HasPrefix(line[start:], "[[") {
		end := strings.Index(line[start+2:], "]]")
		if end < 0 {
			return "", 0, false
		}
		name := line[start+2 : start+2+end]
		if bar := strings.LastIndexByte(name, '|'); bar >= 0 {
			name = name[bar+1:]
		}
		return name, start + 2 + end + 2, true
	}
	closing := strings.IndexByte(line[start+1:], ']')
	if closing < 0 {
		return "", 0, false
	}
	closing += start + 1
	label, rest := line[start+1:closing], line[closing+1:]
	switch {
	case strings.HasPrefix(label, "^") && !strings.HasPrefix(rest, "(") && !strings.HasPrefix(rest, "["):
		return "", closing + 1, true
	case strings.HasPrefix(rest, "("):
		if end := strings.IndexByte(rest, ')'); end > 0 {
			return label, closing + 1 + end + 1, true
		}
	case strings.HasPrefix(rest, "["):
		if end := strings.IndexByte(rest, ']'); end > 0 {
			return label, closing + 1 + end + 1, true
		}
	}
	return "", 0, false
}
