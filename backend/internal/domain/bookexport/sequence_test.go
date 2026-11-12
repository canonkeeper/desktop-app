package bookexport_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/bookexport"
)

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

// sequence reads a file of a book back as its readers see it: the headings of the chapters, the paragraphs and the
// pictures with the bytes their references lead to, in the order of the file.
func sequence(t *testing.T, format bookexport.Format, file []byte) []string {
	t.Helper()
	switch format {
	case bookexport.FormatDOCX:
		return docxSequence(t, file)
	case bookexport.FormatEPUB:
		return epubSequence(t, file)
	default:
		return fb2Sequence(t, file)
	}
}

func docxSequence(t *testing.T, file []byte) []string {
	contents, _ := parts(t, file)
	targets := map[string]string{}
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal([]byte(contents["word/_rels/document.xml.rels"]), &relationships); err != nil {
		t.Fatal(err)
	}
	for _, item := range relationships.Items {
		targets[item.ID] = "word/" + item.Target
	}
	decoder := xml.NewDecoder(strings.NewReader(contents["word/document.xml"]))
	var seen []string
	var text strings.Builder
	style, described, embedded, inText := "", "", "", false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return seen
		}
		if err != nil {
			t.Fatal(err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "p":
				text.Reset()
				style, described, embedded = "", "", ""
			case "pStyle":
				style = attribute(token, "val")
			case "docPr":
				described = attribute(token, "descr")
			case "blip":
				embedded = attribute(token, "embed")
			case "t":
				inText = true
			}
		case xml.CharData:
			if inText {
				text.Write(token)
			}
		case xml.EndElement:
			switch token.Name.Local {
			case "t":
				inText = false
			case "p":
				switch {
				case style == "Heading1":
					seen = append(seen, "H:"+text.String())
				case style == "Title" || style == "BookAuthor" || described == "" && text.Len() == 0:
				case described != "" && embedded != "rIdCover":
					target, ok := targets[embedded]
					content, found := contents[target]
					if !ok || !found {
						t.Fatalf("the picture %q leads to %q, which the file has not", described, embedded)
					}
					seen = append(seen, "IMG:"+described+"="+digest([]byte(content)))
				case described == "":
					seen = append(seen, "P:"+text.String())
				}
			}
		}
	}
}

func epubSequence(t *testing.T, file []byte) []string {
	contents, names := parts(t, file)
	var seen []string
	for _, name := range names {
		if !strings.HasPrefix(name, "OEBPS/chapter-") {
			continue
		}
		decoder := xml.NewDecoder(strings.NewReader(contents[name]))
		var text strings.Builder
		block := ""
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			switch token := token.(type) {
			case xml.StartElement:
				switch token.Name.Local {
				case "h1", "p":
					block = token.Name.Local
					text.Reset()
				case "img":
					target := path.Join("OEBPS", attribute(token, "src"))
					content, found := contents[target]
					if !found {
						t.Fatalf("the picture %q leads to %q, which the file has not", attribute(token, "alt"), target)
					}
					seen = append(seen, "IMG:"+attribute(token, "alt")+"="+digest([]byte(content)))
				}
			case xml.CharData:
				if block != "" {
					text.Write(token)
				}
			case xml.EndElement:
				switch {
				case token.Name.Local == "h1" && block == "h1":
					seen = append(seen, "H:"+text.String())
					block = ""
				case token.Name.Local == "p" && block == "p":
					seen = append(seen, "P:"+text.String())
					block = ""
				}
			}
		}
	}
	return seen
}

func fb2Sequence(t *testing.T, file []byte) []string {
	binaries := map[string][]byte{}
	var parsed struct {
		Binaries []struct {
			ID      string `xml:"id,attr"`
			Content string `xml:",chardata"`
		} `xml:"binary"`
	}
	if err := xml.Unmarshal(file, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, binary := range parsed.Binaries {
		content, err := base64.StdEncoding.DecodeString(binary.Content)
		if err != nil {
			t.Fatal(err)
		}
		binaries[binary.ID] = content
	}
	decoder := xml.NewDecoder(bytes.NewReader(file))
	var seen []string
	var text strings.Builder
	inBody, inTitle, inParagraph := false, false, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return seen
		}
		if err != nil {
			t.Fatal(err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch name := token.Name.Local; {
			case name == "body":
				inBody = true
			case inBody && name == "title":
				inTitle = true
				text.Reset()
			case inBody && name == "p":
				inParagraph = true
				if !inTitle {
					text.Reset()
				}
			case inBody && name == "image":
				href := strings.TrimPrefix(attribute(token, "href"), "#")
				content, found := binaries[href]
				if !found {
					t.Fatalf("the picture %q leads to %q, which the file has not", attribute(token, "alt"), href)
				}
				seen = append(seen, "IMG:"+attribute(token, "alt")+"="+digest(content))
			}
		case xml.CharData:
			if inParagraph {
				text.Write(token)
			}
		case xml.EndElement:
			switch name := token.Name.Local; {
			case name == "body":
				inBody = false
			case inBody && name == "title":
				inTitle = false
				seen = append(seen, "H:"+text.String())
			case inBody && name == "p":
				inParagraph = false
				if !inTitle {
					seen = append(seen, "P:"+text.String())
				}
			}
		}
	}
}

func attribute(element xml.StartElement, name string) string {
	for _, attribute := range element.Attr {
		if attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}
