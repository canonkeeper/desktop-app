package authormaterials

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// ParseDOCX reads a document: a heading of the first level found starts a section, deeper headings stay in it as
// paragraphs; each table is a section under the last heading; comments, footnotes and endnotes stay with the text they
// are on (R9.3).
func ParseDOCX(data []byte) (Parsed, error) {
	source, err := openArchive(data)
	if err != nil {
		return Parsed{}, err
	}
	body, err := source.read("word/document.xml")
	if err != nil {
		return Parsed{}, err
	}
	if body == nil {
		return Parsed{}, ErrUnreadable
	}
	var notes documentNotes
	for _, part := range []struct {
		name, element string
		into          *map[string]string
	}{
		{"word/comments.xml", "comment", &notes.comments},
		{"word/footnotes.xml", "footnote", &notes.footnotes},
		{"word/endnotes.xml", "endnote", &notes.endnotes},
	} {
		if *part.into, err = documentNotesOf(source, part.name, part.element); err != nil {
			return Parsed{}, err
		}
	}
	blocks, err := documentBlocks(body, notes)
	if err != nil {
		return Parsed{}, err
	}
	return sectionsOf(blocks)
}

// documentNotes are the texts a paragraph may point to, by their ids.
type documentNotes struct {
	comments, footnotes, endnotes map[string]string
}

// docxBlock is a paragraph or a table of the body.
type docxBlock struct {
	paragraph Paragraph
	rows      [][]Cell
}

// documentNotesOf reads the comments, the footnotes or the endnotes of a document: the text of each by its id.
func documentNotesOf(source archive, name, element string) (map[string]string, error) {
	body, err := source.read(name)
	if err != nil || body == nil {
		return nil, err
	}
	comments := map[string]string{}
	note := element
	decoder := xml.NewDecoder(bytes.NewReader(body))
	current := ""
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return comments, nil
		}
		if err != nil {
			return nil, ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case note:
				current = attribute(element, "id")
				text.Reset()
			case "t":
				var part string
				if decoder.DecodeElement(&part, &element) != nil {
					return nil, ErrUnreadable
				}
				text.WriteString(part)
			case "p":
				if text.Len() > 0 {
					text.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if element.Name.Local == note && current != "" {
				if value := strings.TrimSpace(text.String()); value != "" {
					comments[current] = value
				}
				current = ""
			}
		}
	}
}

func attribute(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

// headingLevel reads the level of a paragraph style: Heading2, «Заголовок 2» or a bare 2, as Word names them by language.
func headingLevel(style string) int {
	value := strings.ToLower(strings.TrimSpace(style))
	for _, prefix := range []string{"heading", "заголовок", "title"} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
			if value == "" {
				return 1
			}
			break
		}
	}
	if level, err := strconv.Atoi(value); err == nil && level >= 1 && level <= 9 {
		return level
	}
	return 0
}

// documentBlocks walks the body. A table inside a table is read as the text of its cell. A text box is read where Word
// writes its shape, after the paragraph it stands in, and not again in the copy Word keeps for old readers.
func documentBlocks(body []byte, comments documentNotes) ([]docxBlock, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var blocks []docxBlock
	var boxes []docxBlock   // the text boxes of the paragraph being read
	var paragraphs []string // the text of the paragraphs of the cell being read
	var text strings.Builder
	var notes []string
	var row []Cell
	var rows [][]Cell
	level, tables := 0, 0
	note := func() string {
		seen := strings.Join(notes, "\n")
		notes = nil
		return seen
	}
	var cellNotes []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "Fallback":
				if decoder.Skip() != nil {
					return nil, ErrUnreadable
				}
			case "txbxContent":
				start := decoder.InputOffset()
				if decoder.Skip() != nil {
					return nil, ErrUnreadable
				}
				inner := body[start:decoder.InputOffset()]
				if end := bytes.LastIndex(inner, []byte("</")); end >= 0 {
					inner = inner[:end]
				}
				box, err := documentBlocks(inner, comments)
				if err != nil {
					return nil, err
				}
				boxes = append(boxes, box...)
			case "tbl":
				if tables++; tables == 1 {
					rows = nil
				}
			case "tr":
				if tables == 1 {
					row = nil
				}
			case "tc":
				if tables == 1 {
					paragraphs, cellNotes = nil, nil
				}
			case "p":
				text.Reset()
				level = 0
			case "pStyle":
				level = headingLevel(attribute(element, "val"))
			case "t":
				var part string
				if decoder.DecodeElement(&part, &element) != nil {
					return nil, ErrUnreadable
				}
				text.WriteString(part)
			case "tab":
				text.WriteByte(' ')
			case "br":
				text.WriteByte('\n')
			case "commentReference", "footnoteReference", "endnoteReference":
				found := map[string]map[string]string{
					"commentReference": comments.comments, "footnoteReference": comments.footnotes, "endnoteReference": comments.endnotes,
				}[element.Name.Local]
				if comment := found[attribute(element, "id")]; comment != "" {
					notes = append(notes, comment)
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "p":
				line := strings.TrimSpace(text.String())
				if tables > 0 {
					if line != "" {
						paragraphs = append(paragraphs, line)
					}
					if seen := note(); seen != "" {
						cellNotes = append(cellNotes, seen)
					}
				} else if line != "" {
					blocks = append(blocks, docxBlock{paragraph: Paragraph{Text: line, Level: level, Note: note()}})
				}
				blocks, boxes = append(blocks, boxes...), nil
				if len(blocks) > maxParagraphs {
					return nil, ErrTooLarge
				}
			case "tc":
				if tables == 1 {
					row = append(row, Cell{Text: strings.Join(paragraphs, "\n"), Note: strings.Join(cellNotes, "\n")})
				}
			case "tr":
				if tables == 1 {
					rows = append(rows, row)
					if len(rows) > maxRows {
						return nil, ErrTooLarge
					}
				}
			case "tbl":
				if tables--; tables == 0 && len(rows) > 0 {
					blocks = append(blocks, docxBlock{rows: rows})
				}
			}
		}
	}
	return append(blocks, boxes...), nil
}

// sectionsOf cuts the blocks into sections at the headings of the highest level the document has.
func sectionsOf(blocks []docxBlock) (Parsed, error) {
	top := 0
	for _, block := range blocks {
		if level := block.paragraph.Level; level > 0 && (top == 0 || level < top) {
			top = level
		}
	}
	var parsed Parsed
	var current Section
	flush := func() {
		if len(current.Paragraphs) > 0 {
			parsed.Sections = append(parsed.Sections, current)
		}
		current = Section{Title: current.Title}
	}
	tables := 0
	for _, block := range blocks {
		if len(block.rows) > 0 {
			flush()
			tables++
			table := 0
			if current.Title == "" {
				table = tables
			}
			parsed.Sections = append(parsed.Sections, Section{Title: current.Title, Table: table, Rows: block.rows})
			continue
		}
		if block.paragraph.Level == top && top > 0 {
			flush()
			current.Title = block.paragraph.Text
			continue
		}
		current.Paragraphs = append(current.Paragraphs, block.paragraph)
	}
	flush()
	if len(parsed.Sections) == 0 {
		return Parsed{}, ErrEmpty
	}
	return parsed, nil
}
