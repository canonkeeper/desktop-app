package chapterfile

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// parseDOCX reads the body of a Word file paragraph by paragraph (PR1.2): the text of its runs with their italic and
// bold, a tab or a break as a space; a paragraph of a table cell is a paragraph. What is not the chapter stays out:
// footnotes, comments, pictures and text boxes, the codes of fields, and the text the file's own revisions deleted —
// it is read as its revisions accepted. The first paragraph with words in a style of a heading is the title.
func parseDOCX(data []byte) (Document, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Document{}, ErrUnreadable
	}
	var body, styles []byte
	for _, file := range archive.File {
		switch file.Name {
		case "word/document.xml":
			if body != nil {
				return Document{}, ErrUnreadable
			}
			if body, err = readPart(file); err != nil {
				return Document{}, err
			}
		case "word/styles.xml":
			if styles, err = readPart(file); err != nil {
				return Document{}, err
			}
		}
	}
	if body == nil {
		return Document{}, ErrUnreadable
	}
	headings, err := headingStyles(styles)
	if err != nil {
		return Document{}, err
	}
	return documentOf(body, headings)
}

func readPart(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxPartBytes {
		return nil, ErrTooLarge
	}
	stream, err := file.Open()
	if err != nil {
		return nil, ErrUnreadable
	}
	defer stream.Close()
	content, err := io.ReadAll(io.LimitReader(stream, maxPartBytes+1))
	if err != nil {
		return nil, ErrUnreadable
	}
	if len(content) > maxPartBytes {
		return nil, ErrTooLarge
	}
	return content, nil
}

// headingStyles are the ids of the styles of a heading of the first level or of a title: Word of another language
// keeps «heading 1» under an id of its own («1» in Russian Word).
func headingStyles(styles []byte) (map[string]bool, error) {
	headings := map[string]bool{}
	if styles == nil {
		return headings, nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(styles))
	id := ""
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return headings, nil
		}
		if err != nil {
			return nil, ErrUnreadable
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "style":
			id = attribute(start, "styleId")
		case "name":
			if id != "" && headingName(attribute(start, "val")) {
				headings[id] = true
			}
		}
	}
}

// headingName says a style names a heading of the first level or a title.
func headingName(name string) bool {
	switch strings.Join(strings.Fields(strings.ToLower(name)), "") {
	case "heading1", "заголовок1", "title", "название", "заголовок":
		return true
	}
	return false
}

func attribute(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

// on reads a toggle of a run: <w:i/> or <w:i w:val="true"/> is on, a value 0, false, off or none is off.
func on(element xml.StartElement) bool {
	switch strings.ToLower(attribute(element, "val")) {
	case "0", "false", "off", "none":
		return false
	}
	return true
}

// skipped are the elements whose text is not the chapter: what a revision deleted or moved away, pictures and text
// boxes (and the copy Word keeps of them for old readers), the codes of fields, the marks of notes and comments.
var skipped = map[string]bool{
	"del": true, "moveFrom": true, "delText": true, "drawing": true, "pict": true, "object": true, "AlternateContent": true,
	"txbxContent": true, "instrText": true, "delInstrText": true, "footnoteReference": true, "endnoteReference": true,
	"commentReference": true, "commentRangeStart": true, "commentRangeEnd": true,
}

// documentOf walks the body of a Word file.
func documentOf(body []byte, headings map[string]bool) (Document, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	document := Document{Format: FormatDOCX}
	var (
		paragraph           *Paragraph
		heading, titleTaken bool
		inProperties, inRun bool
		inRunProperties     bool
		inText              bool
		italic, bold        bool
		text                strings.Builder
		skip                int
		seenText            bool
	)
	endRun := func() {
		if paragraph != nil && text.Len() > 0 {
			paragraph.Runs = append(paragraph.Runs, Run{Text: text.String(), Italic: italic, Bold: bold})
		}
		text.Reset()
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Document{}, ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			name := element.Name.Local
			if skipped[name] {
				skip = 1
				continue
			}
			switch name {
			case "p":
				paragraph, heading = &Paragraph{}, false
			case "pPr":
				inProperties = true
			case "pStyle":
				if inProperties && headings[attribute(element, "val")] || inProperties && headingName(attribute(element, "val")) {
					heading = true
				}
			case "outlineLvl":
				if inProperties && attribute(element, "val") == "0" {
					heading = true
				}
			case "r":
				inRun, italic, bold = true, false, false
				text.Reset()
			case "rPr":
				inRunProperties = inRun
			case "i":
				if inRunProperties {
					italic = on(element)
				}
			case "b":
				if inRunProperties {
					bold = on(element)
				}
			case "t":
				inText = inRun
			case "tab", "br", "cr", "ptab":
				if inRun {
					text.WriteByte(' ')
				}
			case "noBreakHyphen":
				if inRun {
					text.WriteByte('-')
				}
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			switch element.Name.Local {
			case "p":
				if paragraph == nil {
					continue
				}
				endRun()
				words := strings.TrimSpace(paragraph.Text()) != ""
				if heading && words && !titleTaken && !seenText {
					title := *paragraph
					document.Title, titleTaken = &title, true
				} else {
					document.Paragraphs = append(document.Paragraphs, *paragraph)
					seenText = seenText || words
				}
				paragraph = nil
			case "pPr":
				inProperties = false
			case "r":
				endRun()
				inRun = false
			case "rPr":
				inRunProperties = false
			case "t":
				inText = false
			}
		case xml.CharData:
			if inText && skip == 0 {
				text.Write(element)
			}
		}
	}
	return document, nil
}
