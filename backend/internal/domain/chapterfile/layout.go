package chapterfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strings"
)

// Layout is what a Word file keeps of its look besides its paragraphs (PR1.7): its styles and its theme as they stood,
// the section of its body — the page and its margins — and the namespaces of its root, in which the properties of its
// paragraphs and runs are written. The file with revisions is written in it (PR6.7).
type Layout struct {
	Styles     string `json:"styles,omitempty"`
	Theme      string `json:"theme,omitempty"`
	Section    string `json:"section,omitempty"`
	Namespaces string `json:"namespaces,omitempty"`
}

// maxLayoutPartBytes bounds the styles and the theme kept: a larger part is not kept, the file is written in the styles
// of the export of a book.
const maxLayoutPartBytes = 2 << 20

// maxLookBytes bounds the whole look of a file kept — properties, styles, theme, section — well under the 16 MiB of the
// stored original: a file in which the look outweighs it is kept without its look, as a text (PR1.7).
const maxLookBytes = 8 << 20

// lookBytes is the size of the look of a document.
func lookBytes(document Document) int {
	if document.Layout == nil {
		return 0
	}
	size := len(document.Layout.Styles) + len(document.Layout.Theme) + len(document.Layout.Section) + len(document.Layout.Namespaces)
	paragraphs := document.Paragraphs
	if document.Title != nil {
		paragraphs = append([]Paragraph{*document.Title}, paragraphs...)
	}
	for _, paragraph := range paragraphs {
		size += len(paragraph.Props) + len(paragraph.Mark)
		for _, run := range paragraph.Runs {
			size += len(run.Props)
		}
	}
	return size
}

// withoutLook is the document without its look: the runs of one face join again.
func withoutLook(document Document) Document {
	strip := func(paragraph Paragraph) Paragraph {
		runs := make([]Run, len(paragraph.Runs))
		for i, run := range paragraph.Runs {
			runs[i] = Run{Text: run.Text, Italic: run.Italic, Bold: run.Bold}
		}
		return clean(Paragraph{Runs: runs})
	}
	paragraphs := make([]Paragraph, len(document.Paragraphs))
	for i, paragraph := range document.Paragraphs {
		paragraphs[i] = strip(paragraph)
	}
	document.Paragraphs, document.Layout = paragraphs, nil
	if document.Title != nil {
		title := strip(*document.Title)
		document.Title = &title
	}
	return document
}

const themeRelationship = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme"

// What the properties kept leave out (PR1.7): the revisions of the file, a list (its numbering is not kept), a section
// inside a paragraph, and in the section of the body its headers, footers and printer, whose parts are not kept.
var (
	paragraphDropped = map[string]bool{"pPrChange": true, "numPr": true, "sectPr": true}
	runDropped       = map[string]bool{"rPrChange": true, "ins": true, "del": true, "moveFrom": true, "moveTo": true}
	sectionDropped   = map[string]bool{
		"headerReference": true, "footerReference": true, "printerSettings": true, "sectPrChange": true,
	}
)

// properties writes the children of a properties element back as they stood, by their prefixes, without the dropped
// ones; marks, when not nil, takes the children of a direct child rPr — the properties of a paragraph mark.
func properties(raw []byte, dropped map[string]bool, marks *string) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var out, mark strings.Builder
	depth, skip := 0, 0
	inMark := 0 // the depth of the rPr of the mark, or 0
	var open *xml.StartElement
	target := func() *strings.Builder {
		if inMark > 0 {
			return &mark
		}
		return &out
	}
	flush := func(closing bool) {
		if open == nil {
			return
		}
		writeStart(target(), *open, closing)
		open = nil
	}
	for {
		token, err := decoder.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			flush(false)
			depth++
			if skip > 0 {
				skip++
				continue
			}
			if depth == 1 && element.Name.Local == "rPr" && marks != nil {
				inMark = depth
				continue
			}
			if depth == 1 && dropped[element.Name.Local] || inMark > 0 && depth == inMark+1 && runDropped[element.Name.Local] {
				skip = 1
				continue
			}
			copied := element.Copy()
			open = &copied
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			if inMark > 0 && depth == inMark-1 {
				inMark = 0
				continue
			}
			if open != nil {
				flush(true)
				continue
			}
			target().WriteString("</" + qualified(element.Name) + ">")
		case xml.CharData:
			if skip > 0 {
				continue
			}
			flush(false)
			_ = xml.EscapeText(target(), element)
		}
	}
	if depth != 0 || skip != 0 {
		return "", ErrUnreadable
	}
	if marks != nil {
		*marks = mark.String()
	}
	return out.String(), nil
}

// section writes the section of the body — its start tag and its children — without its headers, footers and printer.
func section(tag, children []byte) (string, error) {
	token, err := xml.NewDecoder(bytes.NewReader(tag)).RawToken()
	if err != nil {
		return "", ErrUnreadable
	}
	start, ok := token.(xml.StartElement)
	if !ok || start.Name.Local != "sectPr" {
		return "", ErrUnreadable
	}
	kept, err := properties(children, sectionDropped, nil)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	writeStart(&out, start, false)
	out.WriteString(kept + "</" + qualified(start.Name) + ">")
	return out.String(), nil
}

// namespacesOf reads the declarations of the namespaces of the root of document.xml and its mc:Ignorable; ok is false
// for a file whose prefix w is not the namespace the file with revisions writes (Strict Open XML).
func namespacesOf(root []byte) (string, bool) {
	decoder := xml.NewDecoder(bytes.NewReader(root))
	token, err := decoder.RawToken()
	if err != nil {
		return "", false
	}
	start, isStart := token.(xml.StartElement)
	if !isStart {
		return "", false
	}
	var out strings.Builder
	transitional := false
	for _, attr := range start.Attr {
		if attr.Name.Space == "xmlns" || attr.Name.Local == "Ignorable" && attr.Name.Space != "" {
			if attr.Name.Space == "xmlns" && attr.Name.Local == "w" {
				if attr.Value != wordNamespace {
					return "", false
				}
				transitional = true
			}
			out.WriteString(" " + qualified(attr.Name) + `="`)
			_ = xml.EscapeText(&out, []byte(attr.Value))
			out.WriteString(`"`)
		}
	}
	return out.String(), transitional
}

// themeTarget is the part of the theme of document.xml by its relationships, or empty.
func themeTarget(relationships []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(relationships))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if ok && start.Name.Local == "Relationship" && attribute(start, "Type") == themeRelationship &&
			attribute(start, "TargetMode") != "External" {
			return path.Clean(path.Join("word", attribute(start, "Target")))
		}
	}
}

func writeStart(out *strings.Builder, start xml.StartElement, closing bool) {
	out.WriteString("<" + qualified(start.Name))
	for _, attr := range start.Attr {
		out.WriteString(" " + qualified(attr.Name) + `="`)
		_ = xml.EscapeText(out, []byte(attr.Value))
		out.WriteString(`"`)
	}
	if closing {
		out.WriteString("/>")
		return
	}
	out.WriteString(">")
}

func qualified(name xml.Name) string {
	if name.Space == "" {
		return name.Local
	}
	return name.Space + ":" + name.Local
}
