package chapterfile

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

const (
	layoutStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:asciiTheme="minorHAnsi" w:hAnsiTheme="minorHAnsi"/><w:sz w:val="24"/></w:rPr></w:rPrDefault>` +
		`<w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="259" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="a"><w:name w:val="Normal"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/><w:basedOn w:val="a"/><w:rPr><w:b/><w:sz w:val="32"/></w:rPr></w:style>` +
		`<w:style w:type="character" w:styleId="Emph"><w:name w:val="Emphasis"/><w:rPr><w:i/></w:rPr></w:style>` +
		`</w:styles>`
	layoutTheme = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Тема"><a:themeElements><a:fontScheme name="Office">` +
		`<a:majorFont><a:latin typeface="Georgia"/></a:majorFont><a:minorFont><a:latin typeface="Georgia"/></a:minorFont>` +
		`</a:fontScheme></a:themeElements></a:theme>`
	layoutRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>` +
		`<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>` +
		`</Relationships>`
)

// layoutFile is a Word file with a look of its own: its styles and theme, a heading in Russian Word's style «1», a
// paragraph with its spacing and indent whose mark has a font, a word in italic by a character style, a list and a
// revision of the paragraph's properties, a section with a header.
func layoutFile(t *testing.T) []byte {
	t.Helper()
	body := `<w:p><w:pPr><w:pStyle w:val="1"/><w:jc w:val="center"/></w:pPr><w:r><w:t>Глава 3. Брод</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:spacing w:line="360" w:lineRule="auto"/><w:ind w:firstLine="567"/>` +
		`<w:rPr><w:ins w:id="4" w:author="x" w:date="2026-11-01T00:00:00Z"/><w:rFonts w:ascii="Georgia"/></w:rPr>` +
		`<w:pPrChange w:id="5" w:author="x"><w:pPr><w:ind w:firstLine="0"/></w:pPr></w:pPrChange></w:pPr>` +
		`<w:r><w:t xml:space="preserve">Кира шла </w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Emph"/><w14:ligatures w14:val="standard"/><w:rPrChange w:id="6" w:author="x"><w:rPr/></w:rPrChange></w:rPr><w:t>медленно</w:t></w:r>` +
		`<w:r><w:t>.</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr><w:spacing w:line="360" w:lineRule="auto"/></w:pPr>` +
		`<w:r><w:t>Ольм ждал у ворот.</w:t></w:r></w:p>` +
		`<w:sectPr><w:headerReference w:type="default" r:id="rId9"/><w:pgSz w:w="8392" w:h="11907"/>` +
		`<w:pgMar w:top="1134" w:right="851" w:bottom="1134" w:left="851" w:header="709" w:footer="709" w:gutter="0"/></w:sectPr>`
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, part := range []struct{ name, content string }{
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
			`xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" ` +
			`xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml" mc:Ignorable="w14"><w:body>` + body + `</w:body></w:document>`},
		{"word/styles.xml", layoutStyles},
		{"word/theme/theme1.xml", layoutTheme},
		{"word/_rels/document.xml.rels", layoutRels},
	} {
		file, err := archive.Create(part.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// partsOf reads every part of a Word file.
func partsOf(t *testing.T, file []byte) map[string]string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, part := range archive.File {
		stream, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[part.Name] = string(content)
	}
	return parts
}

func TestTheFileWithRevisionsKeepsTheLookOfTheAuthorsWordFile(t *testing.T) {
	parsed, err := Parse("Глава 3.docx", layoutFile(t))
	if err != nil {
		t.Fatal(err)
	}
	// The original lies in PostgreSQL as JSON: the look must come back from it.
	stored, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	var original Document
	if err := json.Unmarshal(stored, &original); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, parsed) {
		t.Fatalf("the document does not come back from its JSON:\n%#v\nwant\n%#v", original, parsed)
	}
	if original.Text() != "Кира шла медленно.\n\nОльм ждал у ворот." || original.TitleText() != "Глава 3. Брод" {
		t.Fatalf("text %q, title %q", original.Text(), original.TitleText())
	}
	lines := now("Кира шла ⟨медленно⟩.", "Ольм ждал у северных ворот.", "Светало.")
	file := trackedFile(t, original, "Глава 3. Брод", lines)

	accepted, revisions, _ := revisionsOf(t, file, true)
	rejected, _, _ := revisionsOf(t, file, false)
	if revisions == 0 || !reflect.DeepEqual(accepted, append([]string{"Глава 3. Брод"}, texts(lines)...)) ||
		!reflect.DeepEqual(rejected, []string{"Глава 3. Брод", "Кира шла медленно.", "Ольм ждал у ворот."}) {
		t.Fatalf("accepted %q, rejected %q, revisions %d", accepted, rejected, revisions)
	}
	parts := partsOf(t, file)
	if parts["word/styles.xml"] != layoutStyles || parts["word/theme/theme1.xml"] != layoutTheme {
		t.Fatalf("the styles and the theme of the author's file are not written as they stood")
	}
	if !strings.Contains(parts["word/_rels/document.xml.rels"], `Target="theme/theme1.xml"`) ||
		!strings.Contains(parts["[Content_Types].xml"], `PartName="/word/theme/theme1.xml"`) {
		t.Fatalf("the theme is not bound: %s\n%s", parts["word/_rels/document.xml.rels"], parts["[Content_Types].xml"])
	}
	document := parts["word/document.xml"]
	for _, want := range []string{
		`xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"`, `mc:Ignorable="w14"`,
		// The heading in its own style, not the export's Heading1.
		`<w:pPr><w:pStyle w:val="1"/><w:jc w:val="center"/></w:pPr>`,
		// The paragraph's spacing and indent, its mark's font after no revision of the file.
		`<w:pPr><w:spacing w:line="360" w:lineRule="auto"/><w:ind w:firstLine="567"/><w:rPr><w:rFonts w:ascii="Georgia"/></w:rPr></w:pPr>`,
		// The italic of the character style, as it stood.
		`<w:r><w:rPr><w:rStyle w:val="Emph"/><w14:ligatures w14:val="standard"/></w:rPr><w:t xml:space="preserve">медленно</w:t></w:r>`,
		// The inserted words in the run before them, the inserted paragraph in the look of the paragraph before it.
		`<w:ins `, `<w:pPr><w:spacing w:line="360" w:lineRule="auto"/><w:rPr><w:ins `,
		`<w:pgSz w:w="8392" w:h="11907"/><w:pgMar w:top="1134" w:right="851" w:bottom="1134" w:left="851" w:header="709" w:footer="709" w:gutter="0"/>`,
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("document.xml has no %s:\n%s", want, document)
		}
	}
	for _, gone := range []string{"Heading1", "headerReference", "pPrChange", "rPrChange", "numPr", `w:id="4"`, "Times New Roman"} {
		if strings.Contains(document, gone) {
			t.Fatalf("document.xml keeps %s:\n%s", gone, document)
		}
	}
}

func TestAWordFileInStrictOpenXMLKeepsNoLook(t *testing.T) {
	strict := `<w:document xmlns:w="http://purl.oclc.org/ooxml/wordprocessingml/main"><w:body>` +
		`<w:p><w:pPr><w:spacing w:line="360"/></w:pPr><w:r><w:rPr><w:i/></w:rPr><w:t>Кира шла.</w:t></w:r></w:p></w:body></w:document>`
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(strict)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	document, err := Parse("Глава.docx", buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []Paragraph{{Runs: []Run{{Text: "Кира шла.", Italic: true}}}}
	if document.Layout != nil || !reflect.DeepEqual(document.Paragraphs, want) {
		t.Fatalf("layout %#v, paragraphs %#v", document.Layout, document.Paragraphs)
	}
}

func TestAFileWhoseLookOutweighsItsBudgetIsKeptAsAText(t *testing.T) {
	heavy := `<w:rFonts w:ascii="` + strings.Repeat("Georgia", 900) + `"/>`
	var body strings.Builder
	for range 1400 {
		body.WriteString(`<w:p><w:r><w:rPr>` + heavy + `</w:rPr><w:t xml:space="preserve">Кира </w:t></w:r>`)
		body.WriteString(`<w:r><w:rPr>` + heavy + `<w:i/></w:rPr><w:t>шла.</w:t></w:r></w:p>`)
	}
	document, err := Parse("Глава.docx", docxOf(t, body.String(), ""))
	if err != nil {
		t.Fatal(err)
	}
	want := Paragraph{Runs: []Run{{Text: "Кира "}, {Text: "шла.", Italic: true}}}
	if document.Layout != nil || len(document.Paragraphs) != 1400 || !reflect.DeepEqual(document.Paragraphs[0], want) {
		t.Fatalf("layout %v, %d paragraphs, the first %#v", document.Layout != nil, len(document.Paragraphs), document.Paragraphs[0])
	}
}
