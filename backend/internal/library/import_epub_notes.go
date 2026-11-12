package library

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxEPUBNoteRunes is how long a footnote put into the text may be: what is longer is a part of the book someone links
// to, not a footnote.
const maxEPUBNoteRunes = 2000

// epubNotes are the footnotes of a book by its documents (specs/2026-11-10-ux1-names.md N7.2).
type epubNotes map[string]*epubDocumentNotes

func (notes epubNotes) of(documentPath string) *epubDocumentNotes { return notes[documentPath] }

func (notes epubNotes) at(documentPath string) *epubDocumentNotes {
	if notes[documentPath] == nil {
		notes[documentPath] = &epubDocumentNotes{marks: map[int]string{}, bodies: map[int]bool{}}
	}
	return notes[documentPath]
}

// epubDocumentNotes are the footnotes of one document by the count of its elements: the marks with the text of their
// footnotes, and the bodies of footnotes, which the text of the book leaves out.
type epubDocumentNotes struct {
	marks  map[int]string
	bodies map[int]bool
}

func (notes *epubDocumentNotes) mark(element int) (string, bool) {
	if notes == nil {
		return "", false
	}
	text, marked := notes.marks[element]
	return text, marked
}

func (notes *epubDocumentNotes) body(element int) bool { return notes != nil && notes.bodies[element] }

// epubNoteMark is a link that may be the mark of a footnote: where it stands — the document by its place in the spine
// and the element in it — where it leads, the sign it shows («1», «*»; empty for words) and whether the file itself
// calls it a mark (`epub:type="noteref"`). ids are the ids of the link and of the elements right around it: a footnote
// leads back to one of them.
type epubNoteMark struct {
	path     string
	document int
	element  int
	target   epubLink
	sign     string
	declared bool
	ids      []string
}

var (
	// epubNoteSign is the sign of a mark: a number, asterisks or a dagger, with or without brackets.
	epubNoteSign = regexp.MustCompile(`^[\[(]?(\d{1,4}|\*{1,3}|[†‡§])[\])]?$`)
	// epubWayBack is the whole text of a link of a footnote back to its mark.
	epubWayBack = regexp.MustCompile(`^[↑↩↵⏎←^]+$`)
)

// epubInline are the elements that stand inside a line: the body of a footnote is the block around them.
var epubInline = map[string]bool{
	"a": true, "span": true, "sup": true, "sub": true, "b": true, "i": true, "em": true, "strong": true, "small": true,
	"u": true, "s": true, "font": true, "abbr": true, "cite": true, "code": true, "q": true, "mark": true, "br": true, "img": true,
}

// collectEPUBNotes finds the footnotes of a book: a mark is a link the file calls a mark, or a link that shows only a
// sign and leads to what reads as a footnote — an element the file calls one, by its type or its class, or one that
// leads back to the mark. The body is that element, or the block around it when it stands inside a line, and it comes
// after its mark: a footnote and its mark lead to each other alike, and the one read later is the footnote. A link to a
// heading, to its own paragraph or to a long part of the book is no mark. A document that cannot be read to its end
// gives what it gave: the reading of its chapters reports it.
func collectEPUBNotes(documents []epubDocument) epubNotes {
	var marks []epubNoteMark
	for index, document := range documents {
		marks = append(marks, epubNoteMarks(document, index)...)
	}
	byTarget := map[epubLink][]epubNoteMark{}
	wanted := map[string]bool{}
	for _, mark := range marks {
		byTarget[mark.target] = append(byTarget[mark.target], mark)
		wanted[mark.target.path] = true
	}
	notes := epubNotes{}
	for index, document := range documents {
		if wanted[document.path] {
			epubNoteBodies(document, index, byTarget, notes)
		}
	}
	return notes
}

// epubNoteMarks are the links of a document that may be marks of footnotes.
func epubNoteMarks(document epubDocument, index int) []epubNoteMark {
	reader := newImportXMLReader(bytes.NewReader(document.data))
	var marks []epubNoteMark
	// ids are the ids of the open elements, the innermost last; empty for an element without one.
	var ids []string
	var link *epubNoteMark
	var text strings.Builder
	linkDepth, element := 0, 0
	for {
		token, err := reader.Token()
		if err != nil {
			return marks
		}
		switch token := token.(type) {
		case xml.StartElement:
			element++
			ids = append(ids, epubAttribute(token.Attr, "id"))
			if link != nil || strings.ToLower(token.Name.Local) != "a" {
				continue
			}
			target, ok := resolveEPUBLink(document.path, epubAttribute(token.Attr, "href"))
			if !ok || target.fragment == "" {
				continue
			}
			link = &epubNoteMark{path: document.path, document: index, element: element, target: target}
			for _, kind := range epubTypes(token.Attr) {
				link.declared = link.declared || kind == "noteref"
			}
			for _, id := range ids[max(0, len(ids)-3):] {
				if id != "" {
					link.ids = append(link.ids, id)
				}
			}
			linkDepth = len(ids)
			text.Reset()
		case xml.CharData:
			if link != nil {
				text.Write(token)
			}
		case xml.EndElement:
			if link != nil && len(ids) == linkDepth {
				if sign := epubNoteSign.FindStringSubmatch(strings.Join(strings.Fields(text.String()), "")); sign != nil {
					link.sign = sign[1]
				}
				if link.declared || link.sign != "" {
					marks = append(marks, *link)
				}
				link = nil
			}
			if len(ids) > 0 {
				ids = ids[:len(ids)-1]
			}
		}
	}
}

// epubOpenElement is an open element of a document while the bodies of its footnotes are looked for.
type epubOpenElement struct {
	name    string
	element int
	// start is where the text of the element starts in the text of the footnote being read; -1 outside one.
	start int
}

// epubNoteBody is the body of a footnote being read: its place, the element that holds it, its text so far and
// whether it reads as a footnote.
type epubNoteBody struct {
	target epubLink
	host   int
	depth  int
	text   []byte
	like   bool
}

// epubNoteBodies reads the bodies of the footnotes the marks lead to in one document and writes them to notes.
func epubNoteBodies(document epubDocument, index int, byTarget map[epubLink][]epubNoteMark, notes epubNotes) {
	reader := newImportXMLReader(bytes.NewReader(document.data))
	var open []epubOpenElement
	var body *epubNoteBody
	element := 0
	for {
		token, err := reader.Token()
		if err != nil {
			return
		}
		switch token := token.(type) {
		case xml.StartElement:
			element++
			name := strings.ToLower(token.Name.Local)
			open = append(open, epubOpenElement{name: name, element: element, start: -1})
			if body == nil {
				target := epubLink{path: document.path, fragment: epubAttribute(token.Attr, "id")}
				if target.fragment == "" || len(byTarget[target]) == 0 {
					continue
				}
				host := len(open) - 1
				for host >= 0 && epubInline[open[host].name] {
					host--
				}
				if host < 0 || open[host].name == "html" || open[host].name == "body" || isEPUBHeading(open[host].name) {
					continue
				}
				body = &epubNoteBody{target: target, host: open[host].element, depth: host + 1, like: epubNoteLike(token.Attr)}
			}
			open[len(open)-1].start = len(body.text)
			if name == "a" {
				// A footnote leads back to its mark.
				if back, ok := resolveEPUBLink(document.path, epubAttribute(token.Attr, "href")); ok {
					for _, mark := range byTarget[body.target] {
						for _, id := range mark.ids {
							body.like = body.like || back == epubLink{path: mark.path, fragment: id}
						}
					}
				}
			}
		case xml.CharData:
			if body != nil {
				body.text = append(body.text, token...)
			}
		case xml.EndElement:
			if len(open) == 0 {
				return
			}
			closed := open[len(open)-1]
			open = open[:len(open)-1]
			if body == nil {
				continue
			}
			if closed.start >= 0 && len(open) >= body.depth {
				// The way back and the own number of the footnote are not its text.
				inside := strings.Join(strings.Fields(string(body.text[closed.start:])), "")
				first := strings.TrimSpace(string(body.text[:closed.start])) == ""
				if closed.name == "a" && epubWayBack.MatchString(inside) || first && epubNoteSign.MatchString(inside) {
					body.text = body.text[:closed.start]
				} else if !epubInline[closed.name] {
					body.text = append(body.text, ' ')
				}
			}
			if len(open) < body.depth {
				keepEPUBNote(document.path, index, *body, byTarget[body.target], notes)
				body = nil
			}
		}
	}
}

func isEPUBHeading(name string) bool {
	return len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6'
}

// epubNoteLike reports an element the file calls a footnote: by its epub:type or by its class.
func epubNoteLike(attributes []xml.Attr) bool {
	for _, kind := range epubTypes(attributes) {
		if kind == "footnote" || kind == "endnote" || kind == "rearnote" || kind == "note" {
			return true
		}
	}
	class := strings.ToLower(epubAttribute(attributes, "class"))
	return strings.Contains(class, "note") || strings.Contains(class, "reference")
}

// keepEPUBNote writes a footnote read to its end: its text to each of its marks and its body out of the text of the
// book. Its marks must stand before it, and it must read as a footnote unless the file calls every mark one.
func keepEPUBNote(documentPath string, document int, body epubNoteBody, marks []epubNoteMark, notes epubNotes) {
	text := normalizeImportText(string(body.text))
	for _, mark := range marks {
		before := mark.document < document || mark.document == document && mark.element < body.host
		if !before || !mark.declared && !body.like {
			return
		}
		// «1. Текст», «[1] Текст», «* Текст»: the footnote repeats the sign of its mark before its text.
		if mark.sign != "" {
			leads := []string{"[" + mark.sign + "]", "(" + mark.sign + ")", mark.sign + ".", mark.sign + ")"}
			if mark.sign[0] < '0' || mark.sign[0] > '9' {
				leads = append(leads, mark.sign)
			}
			for _, lead := range leads {
				if rest, found := strings.CutPrefix(text, lead+" "); found {
					text = rest
					break
				}
			}
		}
	}
	if strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "..") {
		text = strings.TrimSpace(strings.TrimSuffix(text, "."))
	}
	if text == "" || utf8.RuneCountInString(text) > maxEPUBNoteRunes {
		return
	}
	notes.at(documentPath).bodies[body.host] = true
	for _, mark := range marks {
		notes.at(mark.path).marks[mark.element] = text
	}
}
