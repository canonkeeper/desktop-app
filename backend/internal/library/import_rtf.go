package library

import (
	"errors"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/chapterfile"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
)

var errRTFDocument = errors.New("library: the rtf document is invalid")

// ParseRTF reads a book kept as RTF (specs/2026-11-10-book-file-kinds.md K3): its text, its paragraphs (\par) and line
// breaks (\line), the signs \'hh in the code page of the document or of their font and \uN with its fallback skipped,
// italic and bold within their groups. Tables of fonts, colours and styles, the information of the document, pictures,
// objects, headers, footers, notes, field codes and every group {\*…} are left out; a paragraph of a heading of the first
// level (\outlinelevel0 or a style named «heading 1») starts a chapter, and so does a line that names a chapter. The
// parser is of this repository: groups nest no deeper than the elements of an imported XML part, and a document without
// {\rtf or with a group left open is invalid.
func ParseRTF(data []byte) (BookImport, error) {
	if !isRTFLike(data) {
		return BookImport{}, errRTFDocument
	}
	return bookOfLines(func(visit func(bookLine) error) error {
		parser := rtfParser{data: data, visit: visit, codepage: 1251, fontCharsets: map[int]int{}, headings: map[int]bool{}}
		return parser.parse()
	})
}

// rtfDestination is what the text of a group is.
type rtfDestination uint8

const (
	rtfText rtfDestination = iota
	// rtfSkipped is a group whose text is not the book.
	rtfSkipped
	rtfFonts
	rtfStyles
)

// rtfState is the state a group keeps and its subgroups inherit.
type rtfState struct {
	destination          rtfDestination
	italic, bold, hidden bool
	// uc is how many signs of fallback follow \uN.
	uc   int
	font int
	// style and outline are the paragraph style (\sN) and outline level (\outlinelevelN, -1 for none).
	style, outline int
}

type rtfParser struct {
	data  []byte
	pos   int
	visit func(bookLine) error

	state rtfState
	stack []rtfState
	// codepage is \ansicpgN of the document, fontCharsets the \fcharsetN of its fonts.
	codepage     int
	fontCharsets map[int]int
	font         int
	// headings are the paragraph styles of the headings of the first level.
	headings map[int]bool
	// stylesDepth is the depth of the group of the table of styles; its entries are the groups right inside it.
	stylesDepth int
	entry       rtfStyleEntry

	// pending are the bytes of \'hh and of 8-bit text that wait for their code page; fallback is how many signs of
	// the fallback of \uN are still to skip; high is the first half of a pair of \uN.
	pending  []byte
	fallback int
	high     rune

	runs []chapterfile.Run
	text strings.Builder
	face [2]bool
}

type rtfStyleEntry struct {
	number  int
	name    strings.Builder
	outline bool
}

// rtfSkippedDestinations are the groups whose text is not the text of the book.
var rtfSkippedDestinations = map[string]bool{
	"colortbl": true, "info": true, "pict": true, "object": true, "objdata": true,
	"header": true, "headerl": true, "headerr": true, "headerf": true, "footer": true, "footerl": true, "footerr": true,
	"footerf": true, "footnote": true, "annotation": true, "atnid": true, "atnauthor": true, "fldinst": true,
	"listtable": true, "listoverridetable": true, "revtbl": true, "rsidtbl": true, "generator": true, "xmlnstbl": true,
	"themedata": true, "colorschememapping": true, "latentstyles": true, "datastore": true, "pgdsctbl": true,
	"filetbl": true, "nonshppict": true, "shp": true, "shpinst": true, "sp": true, "sn": true, "sv": true, "tc": true,
	"xe": true, "txe": true, "pntext": true, "pntxta": true, "pntxtb": true, "listtext": true, "bkmkstart": true,
	"bkmkend": true, "template": true, "docvar": true, "userprops": true, "mmathPr": true, "ftnsep": true, "ftnsepc": true,
	"aftnsep": true, "aftnsepc": true, "private": true, "comment": true, "author": true, "operator": true,
}

// rtfSigns are the control words that stand for a sign.
var rtfSigns = map[string]string{
	"tab": "\t", "cell": "\t", "nestcell": "\t", "line": "\n", "emdash": "—", "endash": "–", "lquote": "‘", "rquote": "’",
	"ldblquote": "“", "rdblquote": "”", "bullet": "•", "emspace": " ", "enspace": " ", "qmspace": " ",
}

func (parser *rtfParser) parse() error {
	parser.state = rtfState{uc: 1, outline: -1}
	opened := false
	for parser.pos < len(parser.data) {
		c := parser.data[parser.pos]
		switch c {
		case '{':
			parser.flushBytes()
			parser.fallback = 0
			if len(parser.stack) >= maxImportXMLDepth {
				return fmt.Errorf("%w: groups nest too deep", errRTFDocument)
			}
			parser.stack = append(parser.stack, parser.state)
			if parser.state.destination == rtfStyles && len(parser.stack) == parser.stylesDepth+1 {
				parser.entry = rtfStyleEntry{}
			}
			opened = true
			parser.pos++
		case '}':
			parser.flushBytes()
			parser.fallback = 0
			if len(parser.stack) == 0 {
				return fmt.Errorf("%w: a group closes that was not opened", errRTFDocument)
			}
			if parser.state.destination == rtfStyles && len(parser.stack) == parser.stylesDepth+1 {
				parser.endStyleEntry()
			}
			parser.state = parser.stack[len(parser.stack)-1]
			parser.stack = parser.stack[:len(parser.stack)-1]
			parser.pos++
			if len(parser.stack) == 0 {
				return parser.endParagraph(true)
			}
		case '\\':
			if err := parser.control(); err != nil {
				return err
			}
		case '\r', '\n':
			parser.pos++
		default:
			parser.pos++
			if parser.skipFallback() {
				continue
			}
			if c >= 0x80 {
				parser.pending = append(parser.pending, c)
				continue
			}
			parser.flushBytes()
			parser.write(string(c))
		}
	}
	if !opened || len(parser.stack) > 0 {
		return fmt.Errorf("%w: a group is not closed", errRTFDocument)
	}
	return nil
}

// skipFallback says a sign is a part of the fallback of \uN and is not read.
func (parser *rtfParser) skipFallback() bool {
	if parser.fallback == 0 {
		return false
	}
	parser.fallback--
	return true
}

func isRTFLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// control reads a control word or a control symbol at the backslash.
func (parser *rtfParser) control() error {
	parser.pos++
	if parser.pos >= len(parser.data) {
		return fmt.Errorf("%w: the document ends in a backslash", errRTFDocument)
	}
	c := parser.data[parser.pos]
	if !isRTFLetter(c) {
		parser.pos++
		switch c {
		case '\'':
			if parser.pos+2 > len(parser.data) {
				return fmt.Errorf("%w: a sign ends early", errRTFDocument)
			}
			value, ok := rtfHex(parser.data[parser.pos], parser.data[parser.pos+1])
			parser.pos += 2
			if !ok {
				return fmt.Errorf("%w: a sign is not hexadecimal", errRTFDocument)
			}
			if !parser.skipFallback() {
				parser.pending = append(parser.pending, value)
			}
		case '\\', '{', '}':
			if !parser.skipFallback() {
				parser.flushBytes()
				parser.write(string(c))
			}
		case '~':
			parser.flushBytes()
			parser.write("\u00a0")
		case '_':
			parser.flushBytes()
			parser.write("-")
		case '*':
			parser.state.destination = max(parser.state.destination, rtfSkipped)
		case '\r', '\n':
			if parser.state.destination == rtfText {
				return parser.endParagraph(false)
			}
		}
		return nil
	}
	start := parser.pos
	for parser.pos < len(parser.data) && isRTFLetter(parser.data[parser.pos]) && parser.pos-start < 32 {
		parser.pos++
	}
	word := string(parser.data[start:parser.pos])
	param, hasParam := 0, false
	negative := parser.pos < len(parser.data) && parser.data[parser.pos] == '-'
	if negative {
		parser.pos++
	}
	digits := parser.pos
	for parser.pos < len(parser.data) && parser.data[parser.pos] >= '0' && parser.data[parser.pos] <= '9' && parser.pos-digits < 10 {
		param = param*10 + int(parser.data[parser.pos]-'0')
		parser.pos++
	}
	hasParam = parser.pos > digits
	if negative {
		param = -param
	}
	if parser.pos < len(parser.data) && parser.data[parser.pos] == ' ' {
		parser.pos++
	}
	parser.fallback = 0
	// The bytes read before the word are in the face and the font they were written in.
	parser.flushBytes()
	return parser.word(word, param, hasParam)
}

func rtfHex(high, low byte) (byte, bool) {
	value := 0
	for _, c := range []byte{high, low} {
		switch {
		case c >= '0' && c <= '9':
			value = value*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			value = value*16 + int(c-'a'+10)
		case c >= 'A' && c <= 'F':
			value = value*16 + int(c-'A'+10)
		default:
			return 0, false
		}
	}
	return byte(value), true
}

// word acts on a control word with its parameter.
func (parser *rtfParser) word(word string, param int, hasParam bool) error {
	on := !hasParam || param != 0
	state := &parser.state
	if sign, ok := rtfSigns[word]; ok {
		parser.flushBytes()
		parser.write(sign)
		return nil
	}
	switch word {
	case "par", "sect", "row", "nestrow":
		if state.destination != rtfText {
			return nil
		}
		return parser.endParagraph(false)
	case "ansicpg":
		parser.codepage = param
	case "uc":
		state.uc = max(param, 0)
	case "u":
		parser.flushBytes()
		parser.unicode(param)
		parser.fallback = state.uc
	case "b":
		state.bold = on
	case "i":
		state.italic = on
	case "v":
		state.hidden = on
	case "plain":
		state.bold, state.italic, state.hidden = false, false, false
	case "pard":
		state.style, state.outline = 0, -1
	case "s":
		if state.destination == rtfStyles {
			parser.entry.number = param
		} else {
			state.style = param
		}
	case "outlinelevel":
		if state.destination == rtfStyles {
			parser.entry.outline = param == 0
		} else {
			state.outline = param
		}
	case "f":
		if state.destination == rtfFonts {
			parser.font = param
		} else {
			parser.flushBytes()
			state.font = param
		}
	case "fcharset":
		if state.destination == rtfFonts {
			parser.fontCharsets[parser.font] = param
		}
	case "fonttbl":
		state.destination = rtfFonts
	case "stylesheet":
		state.destination, parser.stylesDepth = rtfStyles, len(parser.stack)
	case "bin":
		parser.flushBytes()
		parser.pos += min(max(param, 0), len(parser.data)-parser.pos)
	default:
		if rtfSkippedDestinations[word] {
			state.destination = max(state.destination, rtfSkipped)
		}
	}
	return nil
}

// unicode writes the sign of \uN: N is a signed 16-bit number, and a pair of halves makes one sign.
func (parser *rtfParser) unicode(param int) {
	if param < 0 {
		param += 65536
	}
	r := rune(param)
	switch {
	case r >= 0xD800 && r < 0xDC00:
		parser.high = r
		return
	case r >= 0xDC00 && r < 0xE000 && parser.high != 0:
		r = 0x10000 + (parser.high-0xD800)<<10 + (r - 0xDC00)
	}
	parser.high = 0
	parser.write(string(r))
}

// flushBytes decodes the waiting bytes in the code page of their font, or of the document.
func (parser *rtfParser) flushBytes() {
	if len(parser.pending) == 0 {
		return
	}
	decoded, err := rtfEncoding(parser.codepageOf(parser.state.font)).NewDecoder().Bytes(parser.pending)
	parser.pending = parser.pending[:0]
	if err == nil {
		parser.write(string(decoded))
	}
}

// codepageOf is the code page of a font by its charset, or of the document for a font of the default one.
func (parser *rtfParser) codepageOf(font int) int {
	charsets := map[int]int{
		77: 10000, 128: 932, 129: 949, 134: 936, 136: 950, 161: 1253, 162: 1254, 163: 1258, 177: 1255, 178: 1256,
		186: 1257, 204: 1251, 222: 874, 238: 1250,
	}
	if charset, ok := parser.fontCharsets[font]; ok {
		if codepage, ok := charsets[charset]; ok {
			return codepage
		}
	}
	return parser.codepage
}

// rtfEncoding is the decoder of a code page; one the import does not know is read as Windows-1251.
func rtfEncoding(codepage int) encoding.Encoding {
	labels := map[int]string{
		866: "ibm866", 874: "windows-874", 932: "shift_jis", 936: "gbk", 949: "euc-kr", 950: "big5", 1250: "windows-1250",
		1251: "windows-1251", 1252: "windows-1252", 1253: "windows-1253", 1254: "windows-1254", 1255: "windows-1255",
		1256: "windows-1256", 1257: "windows-1257", 1258: "windows-1258", 10000: "macintosh", 10007: "x-mac-cyrillic",
		20866: "koi8-r", 21866: "koi8-u", 28595: "iso-8859-5", 65001: "utf-8",
	}
	if codepage == 437 {
		return charmap.CodePage437
	}
	if label, ok := labels[codepage]; ok {
		if found, err := htmlindex.Get(label); err == nil {
			return found
		}
	}
	return charmap.Windows1251
}

// write puts a text where the group sends it: the paragraph of the book, the name of a style, or nowhere.
func (parser *rtfParser) write(text string) {
	state := parser.state
	switch {
	case state.destination == rtfStyles:
		if len(parser.stack) == parser.stylesDepth+1 {
			parser.entry.name.WriteString(text)
		}
	case state.destination != rtfText || state.hidden:
	default:
		face := [2]bool{state.italic, state.bold}
		if face != parser.face {
			parser.endRun()
			parser.face = face
		}
		parser.text.WriteString(text)
	}
}

func (parser *rtfParser) endRun() {
	if parser.text.Len() > 0 {
		parser.runs = append(parser.runs, chapterfile.Run{Text: parser.text.String(), Italic: parser.face[0], Bold: parser.face[1]})
	}
	parser.text.Reset()
}

// endParagraph passes the paragraph read so far; at the end of the document only one with words.
func (parser *rtfParser) endParagraph(last bool) error {
	parser.flushBytes()
	parser.endRun()
	runs := parser.runs
	parser.runs = nil
	if last && len(runs) == 0 {
		return nil
	}
	heading := parser.state.outline == 0 || parser.headings[parser.state.style]
	return parser.visit(bookLine{runs: runs, heading: heading})
}

// endStyleEntry keeps a paragraph style of a heading of the first level: by its name or its outline level.
func (parser *rtfParser) endStyleEntry() {
	parser.flushBytes()
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parser.entry.name.String()), ";"))
	if parser.entry.outline || isDOCXHeading1Style(name) {
		parser.headings[parser.entry.number] = true
	}
}
