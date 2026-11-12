package library

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/domain/chapterfile"
	"canonkeeper/backend/internal/domain/textface"
)

// scanEPUBBlocks passes the non-empty paragraphs and headings of a spine document to visit in order, their text in the
// italic of <i>, <em> and font-style: italic and the bold of <b>, <strong> and font-weight: bold.
func scanEPUBBlocks(data []byte, visit func(epubBlock) error) error {
	return scanEPUBDocument(data, nil, visit)
}

// scanEPUBDocument is scanEPUBBlocks of a document of a book with its footnotes (specs/2026-11-10-ux1-names.md N7.2):
// the mark of a footnote gives way to its text in square brackets right after the words it explains — in a heading
// the mark simply goes — and the body of a footnote that stands in the text is neither a paragraph nor a heading.
// Elements are counted as the collector of the footnotes counted them.
func scanEPUBDocument(data []byte, notes *epubDocumentNotes, visit func(epubBlock) error) error {
	reader := newImportXMLReader(bytes.NewReader(data))
	captureLevel := -1
	var runs []chapterfile.Run
	// faces are the italic and bold of the open elements of the block, the innermost last.
	var faces []chapterfile.Faces
	depth := 0
	// skipped is the depth inside an element that is left out: the mark or the body of a footnote.
	skipped := 0
	element := 0

	for {
		token, err := reader.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			element++
			if skipped > 0 {
				skipped++
				continue
			}
			if note, marked := notes.mark(element); marked {
				if depth > 0 && captureLevel == 0 {
					runs = append(runs, chapterfile.Run{Text: " [" + note + "]"})
				}
				skipped = 1
				continue
			}
			if notes.body(element) {
				skipped = 1
				continue
			}
			if depth > 0 {
				depth++
				faces = append(faces, epubFaces(token).Over(faces[len(faces)-1]))
				continue
			}
			if level, ok := epubBlockLevel(strings.ToLower(token.Name.Local), token.Attr); ok {
				captureLevel = level
				runs, faces = nil, []chapterfile.Faces{epubFaces(token)}
				depth = 1
			}
		case xml.CharData:
			if depth > 0 && skipped == 0 {
				face := faces[len(faces)-1]
				runs = append(runs, chapterfile.Run{Text: string(token), Italic: face.Italic == chapterfile.On, Bold: face.Bold == chapterfile.On})
			}
		case xml.EndElement:
			if skipped > 0 {
				skipped--
				continue
			}
			if depth == 0 {
				continue
			}
			depth--
			faces = faces[:len(faces)-1]
			if depth > 0 {
				continue
			}
			spaced := textface.Spaced(runs)
			if text := (chapterfile.Paragraph{Runs: spaced}).Text(); text != "" {
				if err := visit(epubBlock{text: text, level: captureLevel, runs: spaced}); err != nil {
					return err
				}
			}
		}
	}
}

// epubFaces are the italic and bold an element of a block sets: <i> and <em> italic, <b> and <strong> bold, and its
// inline style — font-style italic or oblique, font-weight bold, bolder or 600 and more, normal setting them off.
func epubFaces(element xml.StartElement) chapterfile.Faces {
	var faces chapterfile.Faces
	switch strings.ToLower(element.Name.Local) {
	case "i", "em":
		faces.Italic = chapterfile.On
	case "b", "strong":
		faces.Bold = chapterfile.On
	}
	for _, attribute := range element.Attr {
		if !strings.EqualFold(attribute.Name.Local, "style") {
			continue
		}
		for _, declaration := range strings.Split(attribute.Value, ";") {
			property, value, found := strings.Cut(declaration, ":")
			if !found {
				continue
			}
			value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important")))
			switch strings.ToLower(strings.TrimSpace(property)) {
			case "font-style":
				switch value {
				case "italic", "oblique":
					faces.Italic = chapterfile.On
				case "normal":
					faces.Italic = chapterfile.Off
				}
			case "font-weight":
				weight, err := strconv.Atoi(value)
				switch {
				case value == "bold" || value == "bolder" || err == nil && weight >= 600:
					faces.Bold = chapterfile.On
				case value == "normal" || value == "lighter" || err == nil:
					faces.Bold = chapterfile.Off
				}
			}
		}
	}
	return faces
}

// epubBlockLevel recognizes paragraphs (0), h1–h6 and ARIA headings (`role="heading"`
// with `aria-level`, which Wikisource exports use for chapter numbers).
func epubBlockLevel(name string, attributes []xml.Attr) (int, bool) {
	switch name {
	case "p":
		return 0, true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return int(name[1] - '0'), true
	}
	heading, level := false, 7
	for _, attribute := range attributes {
		switch attribute.Name.Local {
		case "role":
			heading = strings.EqualFold(strings.TrimSpace(attribute.Value), "heading")
		case "aria-level":
			if value, err := strconv.Atoi(strings.TrimSpace(attribute.Value)); err == nil && value >= 1 && value <= 6 {
				level = value
			}
		}
	}
	return level, heading
}
