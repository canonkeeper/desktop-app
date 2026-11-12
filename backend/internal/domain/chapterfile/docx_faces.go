package chapterfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// The face of a run of a Word file as Word shows it (specs/2026-11-08-italic-bold-in-text.md TF4.2): its own w:i and
// w:b, else its character style with the chain of its basedOn, else the style of its paragraph (the default paragraph
// style when it names none), else the defaults of the document; the nearest value set wins. The chapter of «Письмо» and
// the import of books read faces by these rules.

// Toggle is a face a run or a style sets on or off, or leaves to what is under it.
type Toggle int8

// The values of a toggle.
const (
	Unset Toggle = iota
	On
	Off
)

// ToggleOf reads a toggle element of the properties of a run: <w:i/> or <w:i w:val="true"/> is on, a value 0, false,
// off or none is off.
func ToggleOf(element xml.StartElement) Toggle {
	if on(element) {
		return On
	}
	return Off
}

// Faces are the italic and bold a run or a style sets.
type Faces struct {
	Italic, Bold Toggle
}

// Over is faces with what they leave unset taken from under.
func (faces Faces) Over(under Faces) Faces {
	if faces.Italic == Unset {
		faces.Italic = under.Italic
	}
	if faces.Bold == Unset {
		faces.Bold = under.Bold
	}
	return faces
}

// styleFaces are the faces a style sets and the style it is based on.
type styleFaces struct {
	faces   Faces
	basedOn string
}

// Styles are the faces the styles of a Word file give: of its character and paragraph styles by their ids, of its
// default paragraph style and of the defaults of the document. The zero value is a file without styles.
type Styles struct {
	character, paragraph map[string]styleFaces
	defaultParagraph     string
	defaults             Faces
}

// ReadStyles reads the faces of the styles of a Word file (word/styles.xml); a file without styles has none.
func ReadStyles(data []byte) (Styles, error) {
	styles := Styles{character: map[string]styleFaces{}, paragraph: map[string]styleFaces{}}
	if data == nil {
		return styles, nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var (
		path     []string
		current  *styleFaces
		kind, id string
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return styles, nil
		}
		if err != nil {
			return Styles{}, ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			parent := ""
			if len(path) > 0 {
				parent = path[len(path)-1]
			}
			path = append(path, element.Name.Local)
			switch name := element.Name.Local; {
			case name == "style":
				current, kind, id = nil, attribute(element, "type"), attribute(element, "styleId")
				if kind == "character" || kind == "paragraph" {
					current = &styleFaces{}
				}
				if kind == "paragraph" && toggleValue(attribute(element, "default")) {
					styles.defaultParagraph = id
				}
			case name == "basedOn" && parent == "style" && current != nil:
				current.basedOn = attribute(element, "val")
			case (name == "i" || name == "b") && parent == "rPr" && len(path) >= 3:
				var target *Faces
				switch path[len(path)-3] {
				case "style":
					if current != nil {
						target = &current.faces
					}
				case "rPrDefault":
					target = &styles.defaults
				}
				if target != nil && name == "i" {
					target.Italic = ToggleOf(element)
				} else if target != nil {
					target.Bold = ToggleOf(element)
				}
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
			if element.Name.Local == "style" {
				if current != nil && kind == "character" {
					styles.character[id] = *current
				} else if current != nil {
					styles.paragraph[id] = *current
				}
				current = nil
			}
		}
	}
}

// Face is the italic and bold of a run by its own toggles, its character style and the style of its paragraph.
func (styles Styles) Face(paragraphStyle, runStyle string, own Faces) (italic, bold bool) {
	if paragraphStyle == "" {
		paragraphStyle = styles.defaultParagraph
	}
	faces := own.Over(chain(styles.character, runStyle)).Over(chain(styles.paragraph, paragraphStyle)).Over(styles.defaults)
	return faces.Italic == On, faces.Bold == On
}

// chain is the faces of a style with what it leaves unset taken from the style it is based on, as far as the chain goes;
// a chain that comes back to itself ends.
func chain(styles map[string]styleFaces, id string) Faces {
	var faces Faces
	for steps := 0; id != "" && steps <= len(styles); steps++ {
		style, ok := styles[id]
		if !ok {
			break
		}
		faces = faces.Over(style.faces)
		id = style.basedOn
	}
	return faces
}

// toggleValue reads an on-off attribute that is off unless set: 1, true or on.
func toggleValue(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "on":
		return true
	}
	return false
}
