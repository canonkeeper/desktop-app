package library

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
)

const (
	odtOfficeNamespace = "urn:oasis:names:tc:opendocument:xmlns:office:1.0"
	odtStyleNamespace  = "urn:oasis:names:tc:opendocument:xmlns:style:1.0"
	odtTextNamespace   = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
	odtTableNamespace  = "urn:oasis:names:tc:opendocument:xmlns:table:1.0"
	odtDrawNamespace   = "urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
	odtFONamespace     = "urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"
	odtDCNamespace     = "http://purl.org/dc/elements/1.1/"
	// maxODTSpaces bounds the spaces one <text:s text:c="…"/> may ask for.
	maxODTSpaces = 1000
)

var errODTEncrypted = errors.New("library: the odt document is encrypted")

// ParseODT reads a book kept as an OpenDocument text (specs/2026-11-10-book-file-kinds.md K4): the paragraphs of
// content.xml in the italic and bold of their styles, the automatic ones and their parents in styles.xml, with the notes
// in square brackets where they stand; a heading of the first level starts a chapter, and so does a line that names a
// chapter. Tables, frames, pictures, a table of contents and what a revision deleted are not the text of the book. The
// title is dc:title of meta.xml, else the first short line. A part is read no further than the limit of a part of Word.
func ParseODT(reader io.ReaderAt, size int64) (BookImport, error) {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return BookImport{}, fmt.Errorf("parse odt archive: %w", err)
	}
	parts := map[string]*zip.File{}
	for _, file := range archive.File {
		switch file.Name {
		case "content.xml", "styles.xml", "meta.xml", "META-INF/manifest.xml":
			parts[file.Name] = file
		}
	}
	if parts["content.xml"] == nil {
		return BookImport{}, fmt.Errorf("parse odt: content.xml is missing")
	}
	if manifest := parts["META-INF/manifest.xml"]; manifest != nil {
		data, err := readDOCXPart(manifest)
		if err != nil {
			return BookImport{}, err
		}
		if bytes.Contains(data, []byte("encryption-data")) {
			return BookImport{}, errODTEncrypted
		}
	}
	contents, err := readDOCXPart(parts["content.xml"])
	if err != nil {
		return BookImport{}, err
	}
	styles := odtStyles{}
	if part := parts["styles.xml"]; part != nil {
		data, err := readDOCXPart(part)
		if err != nil {
			return BookImport{}, err
		}
		if err := styles.read(data); err != nil {
			return BookImport{}, err
		}
	}
	if err := styles.read(contents); err != nil {
		return BookImport{}, err
	}
	book, err := bookOfLines(func(visit func(bookLine) error) error {
		return scanODTParagraphs(contents, styles, visit)
	})
	if err != nil {
		return BookImport{}, err
	}
	if part := parts["meta.xml"]; part != nil {
		data, err := readDOCXPart(part)
		if err != nil {
			return BookImport{}, err
		}
		if title := odtTitle(data); title != "" {
			book.Title = title
		}
	}
	return book, nil
}

// odtStyle is what a style of an OpenDocument tells the import: the faces it sets, the style it is based on and the
// outline level of a heading of its paragraphs.
type odtStyle struct {
	faces   chapterfile.Faces
	parent  string
	outline string
}

// odtStyles are the styles of a document by their family and name ("text/T1", "paragraph/P2").
type odtStyles map[string]*odtStyle

// read adds the styles of a part: all of styles.xml, the automatic styles of content.xml up to its body.
func (styles odtStyles) read(data []byte) error {
	reader := newImportXMLReader(bytes.NewReader(data))
	var current *odtStyle
	for {
		token, err := reader.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse odt styles: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch {
			case isODTElement(value.Name, odtOfficeNamespace, "body"):
				return nil
			case isODTElement(value.Name, odtStyleNamespace, "style"):
				current = &odtStyle{
					parent:  odtAttribute(value, odtStyleNamespace, "parent-style-name"),
					outline: odtAttribute(value, odtStyleNamespace, "default-outline-level"),
				}
				styles[odtAttribute(value, odtStyleNamespace, "family")+"/"+odtAttribute(value, odtStyleNamespace, "name")] = current
			case current != nil && isODTElement(value.Name, odtStyleNamespace, "text-properties"):
				current.faces = odtFaces(value)
			}
		case xml.EndElement:
			if isODTElement(value.Name, odtStyleNamespace, "style") {
				current = nil
			}
		}
	}
}

// faces are the faces a style gives with those of its parents; the nearest set wins.
func (styles odtStyles) faces(family, name string) chapterfile.Faces {
	var faces chapterfile.Faces
	for depth := 0; name != "" && depth <= len(styles); depth++ {
		style := styles[family+"/"+name]
		if style == nil {
			break
		}
		faces = faces.Over(style.faces)
		name = style.parent
	}
	return faces
}

// outline is the outline level a paragraph style gives its headings, from its parents too.
func (styles odtStyles) outline(name string) string {
	for depth := 0; name != "" && depth <= len(styles); depth++ {
		style := styles["paragraph/"+name]
		if style == nil {
			break
		}
		if style.outline != "" {
			return style.outline
		}
		name = style.parent
	}
	return ""
}

// odtFaces reads the italic and bold of <style:text-properties>: fo:font-style italic or oblique, fo:font-weight bold or
// 600 and more; normal and less are off.
func odtFaces(element xml.StartElement) chapterfile.Faces {
	var faces chapterfile.Faces
	switch odtAttribute(element, odtFONamespace, "font-style") {
	case "italic", "oblique":
		faces.Italic = chapterfile.On
	case "normal":
		faces.Italic = chapterfile.Off
	}
	switch weight := odtAttribute(element, odtFONamespace, "font-weight"); weight {
	case "":
	case "bold":
		faces.Bold = chapterfile.On
	case "normal":
		faces.Bold = chapterfile.Off
	default:
		if number, err := strconv.Atoi(weight); err == nil {
			faces.Bold = chapterfile.Off
			if number >= 600 {
				faces.Bold = chapterfile.On
			}
		}
	}
	return faces
}

// odtLeftOut says an element of the body holds no text of the book: a table, a frame or a drawing, a table of contents or
// an index, a comment, what a revision deleted, the declarations of fields and the sign of a note.
func odtLeftOut(name xml.Name) bool {
	switch name.Space {
	case odtTableNamespace:
		return name.Local == "table"
	case odtDrawNamespace:
		return true
	case odtOfficeNamespace:
		return name.Local == "annotation" || name.Local == "annotation-end" || name.Local == "forms"
	case odtTextNamespace:
		switch name.Local {
		case "tracked-changes", "table-of-content", "bibliography", "note-citation", "index-title", "ruby-text",
			"sequence-decls", "variable-decls", "user-field-decls", "dde-connection-decls":
			return true
		}
		return strings.HasSuffix(name.Local, "-index")
	}
	return false
}

// odtElement is the role of an open element of the body.
type odtElement uint8

const (
	odtOther odtElement = iota
	odtBody
	odtParagraph
	odtSpan
	odtNote
)

// scanODTParagraphs passes the paragraphs of office:text to visit, a paragraph without words as a blank one. Spaces are
// read as OpenDocument keeps them: a run of white space in the text is one space, <text:s/> are the spaces it counts.
func scanODTParagraphs(contents []byte, styles odtStyles, visit func(bookLine) error) error {
	reader := newImportXMLReader(bytes.NewReader(contents))
	var (
		elements []odtElement
		faces    []chapterfile.Faces
		runs     []chapterfile.Run
		note     strings.Builder
		// leftOut is the depth inside an element that holds no text of the book.
		leftOut          int
		inBody, heading  bool
		paragraph, notes int
		// space says the text read last ends in a space of the text, so the next white space is not another.
		space bool
	)
	write := func(text string) {
		if notes > 0 {
			note.WriteString(text)
			return
		}
		face := faces[len(faces)-1]
		runs = append(runs, chapterfile.Run{Text: text, Italic: face.Italic == chapterfile.On, Bold: face.Bold == chapterfile.On})
	}
	for {
		token, err := reader.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse odt content: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if leftOut > 0 {
				leftOut++
				continue
			}
			role := odtOther
			switch {
			case !inBody:
				if isODTElement(value.Name, odtOfficeNamespace, "text") {
					inBody, role = true, odtBody
				}
			case odtLeftOut(value.Name):
				leftOut = 1
				continue
			case paragraph == 0 && (isODTElement(value.Name, odtTextNamespace, "p") || isODTElement(value.Name, odtTextNamespace, "h")):
				style := odtAttribute(value, odtTextNamespace, "style-name")
				level := odtAttribute(value, odtTextNamespace, "outline-level")
				if level == "" {
					level = styles.outline(style)
				}
				heading = value.Name.Local == "h" && (level == "" || level == "1")
				runs, faces, space, role = nil, []chapterfile.Faces{styles.faces("paragraph", style)}, true, odtParagraph
			case paragraph > 0 && notes == 0 && isODTElement(value.Name, odtTextNamespace, "span"):
				faces = append(faces, styles.faces("text", odtAttribute(value, odtTextNamespace, "style-name")).Over(faces[len(faces)-1]))
				role = odtSpan
			case paragraph > 0 && isODTElement(value.Name, odtTextNamespace, "note"):
				if notes == 0 {
					note.Reset()
				}
				notes++
				role = odtNote
			case paragraph > 0 && notes > 0 && isODTElement(value.Name, odtTextNamespace, "p"):
				note.WriteByte(' ')
			case paragraph > 0 && isODTElement(value.Name, odtTextNamespace, "s"):
				count, err := strconv.Atoi(odtAttribute(value, odtTextNamespace, "c"))
				if err != nil || count < 1 {
					count = 1
				}
				write(strings.Repeat(" ", min(count, maxODTSpaces)))
				space = false
			case paragraph > 0 && isODTElement(value.Name, odtTextNamespace, "tab"):
				write("\t")
				space = false
			case paragraph > 0 && isODTElement(value.Name, odtTextNamespace, "line-break"):
				write("\n")
				space = true
			}
			if role == odtParagraph {
				paragraph++
			}
			elements = append(elements, role)
		case xml.CharData:
			if leftOut > 0 || paragraph == 0 {
				continue
			}
			text := collapseODTSpaces(string(value))
			if space {
				text = strings.TrimPrefix(text, " ")
			}
			if text == "" {
				continue
			}
			space = strings.HasSuffix(text, " ")
			write(text)
		case xml.EndElement:
			if leftOut > 0 {
				leftOut--
				continue
			}
			if len(elements) == 0 {
				continue
			}
			role := elements[len(elements)-1]
			elements = elements[:len(elements)-1]
			switch role {
			case odtBody:
				inBody = false
			case odtSpan:
				faces = faces[:len(faces)-1]
			case odtNote:
				notes--
				if text := normalizeImportText(note.String()); notes == 0 && text != "" {
					runs = append(runs, chapterfile.Run{Text: " [" + text + "]"})
					space = false
				}
			case odtParagraph:
				paragraph--
				if err := visit(bookLine{runs: runs, heading: heading}); err != nil {
					return err
				}
				runs = nil
			}
		}
	}
}

// collapseODTSpaces makes every run of white space of a text one space, as OpenDocument reads its text.
func collapseODTSpaces(text string) string {
	if !strings.ContainsAny(text, " \t\r\n") {
		return text
	}
	var out strings.Builder
	blank := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !blank {
				out.WriteByte(' ')
			}
			blank = true
			continue
		}
		blank = false
		out.WriteRune(r)
	}
	return out.String()
}

// odtTitle is dc:title of meta.xml when it is short enough to be a title; "" otherwise or when it is not there.
func odtTitle(meta []byte) string {
	reader := newImportXMLReader(bytes.NewReader(meta))
	var title strings.Builder
	inTitle := false
	for {
		token, err := reader.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			inTitle = isODTElement(value.Name, odtDCNamespace, "title")
		case xml.CharData:
			if inTitle {
				title.Write(value)
			}
		case xml.EndElement:
			if inTitle {
				text := normalizeImportText(title.String())
				if utf8.RuneCountInString(text) > maxTitleLineRunes {
					return ""
				}
				return text
			}
		}
	}
	return ""
}

func isODTElement(name xml.Name, space, local string) bool {
	return name.Space == space && name.Local == local
}

func odtAttribute(element xml.StartElement, space, local string) string {
	for _, attribute := range element.Attr {
		if attribute.Name.Space == space && attribute.Name.Local == local {
			return attribute.Value
		}
	}
	return ""
}
