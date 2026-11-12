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
	var body, styles, relationships []byte
	parts := map[string]*zip.File{}
	for _, file := range archive.File {
		parts[file.Name] = file
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
		case "word/_rels/document.xml.rels":
			if relationships, err = readPart(file); err != nil {
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
	faces, err := ReadStyles(styles)
	if err != nil {
		return Document{}, err
	}
	document, err := documentOf(body, headings, faces)
	if err != nil || document.Layout == nil {
		return document, err
	}
	// The styles and the theme as they stood (PR1.7); a larger part is not kept.
	if len(styles) <= maxLayoutPartBytes {
		document.Layout.Styles = string(styles)
	}
	if theme := parts[themeTarget(relationships)]; theme != nil && theme.UncompressedSize64 <= maxLayoutPartBytes {
		if content, err := readPart(theme); err == nil {
			document.Layout.Theme = string(content)
		}
	}
	return document, nil
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
// boxes (and the copy Word keeps of them for old readers), the codes of fields, the marks of notes and comments, and the
// former properties a revision of formatting keeps.
var skipped = map[string]bool{
	"del": true, "moveFrom": true, "delText": true, "drawing": true, "pict": true, "object": true, "AlternateContent": true,
	"txbxContent": true, "instrText": true, "delInstrText": true, "footnoteReference": true, "endnoteReference": true,
	"commentReference": true, "commentRangeStart": true, "commentRangeEnd": true,
	// The former properties a revision of the file keeps are not its look (PR1.7).
	"pPrChange": true, "rPrChange": true, "sectPrChange": true,
}

// documentOf walks the body of a Word file; a run takes the face Word shows by its styles (TF4.2).
func documentOf(body []byte, headings map[string]bool, styles Styles) (Document, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	document := Document{Format: FormatDOCX}
	var (
		paragraph           *Paragraph
		heading, titleTaken bool
		inProperties, inRun bool
		inRunProperties     bool
		inText              bool
		own                 Faces
		paragraphStyle      string
		runStyle            string
		text                strings.Builder
		skip                int
		seenText            bool
		// The look of the file (PR1.7): where the properties of a paragraph, of a run and the section of the body start
		// in the bytes of the body, and the properties of the run being read.
		rooted                    bool
		propertiesFrom, runFrom   int64
		sectionFrom, sectionInner int64
		runProps                  string
	)
	endRun := func() {
		if paragraph != nil && text.Len() > 0 {
			italic, bold := styles.Face(paragraphStyle, runStyle, own)
			paragraph.Runs = append(paragraph.Runs, Run{Text: text.String(), Italic: italic, Bold: bold, Props: runProps})
		}
		text.Reset()
	}
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Document{}, ErrUnreadable
		}
		after := decoder.InputOffset()
		switch element := token.(type) {
		case xml.StartElement:
			if !rooted {
				// The root declares the namespaces the properties are written in; a Strict file keeps no look.
				rooted = true
				if namespaces, ok := namespacesOf(body[before:after]); ok {
					document.Layout = &Layout{Namespaces: namespaces}
				}
			}
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
				paragraph, heading, paragraphStyle = &Paragraph{}, false, ""
			case "pPr":
				inProperties, propertiesFrom = true, after
			case "pStyle":
				if inProperties {
					paragraphStyle = attribute(element, "val")
				}
				if inProperties && headings[attribute(element, "val")] || inProperties && headingName(attribute(element, "val")) {
					heading = true
				}
			case "outlineLvl":
				if inProperties && attribute(element, "val") == "0" {
					heading = true
				}
			case "r":
				inRun, own, runStyle, runProps = true, Faces{}, "", ""
				text.Reset()
			case "rPr":
				inRunProperties, runFrom = inRun, after
			case "sectPr":
				if paragraph == nil {
					sectionFrom, sectionInner = before, after
				}
			case "rStyle":
				if inRunProperties {
					runStyle = attribute(element, "val")
				}
			case "i":
				if inRunProperties {
					own.Italic = ToggleOf(element)
				}
			case "b":
				if inRunProperties {
					own.Bold = ToggleOf(element)
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
				if paragraph != nil && document.Layout != nil {
					// A paragraph whose properties do not read keeps none.
					paragraph.Props, _ = properties(body[propertiesFrom:before], paragraphDropped, &paragraph.Mark)
				}
			case "r":
				endRun()
				inRun = false
			case "rPr":
				if inRunProperties && document.Layout != nil {
					runProps, _ = properties(body[runFrom:before], runDropped, nil)
				}
				inRunProperties = false
			case "sectPr":
				if paragraph == nil && document.Layout != nil {
					document.Layout.Section, _ = section(body[sectionFrom:sectionInner], body[sectionInner:before])
				}
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
