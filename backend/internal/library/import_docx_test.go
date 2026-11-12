package library

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseDOCXPreservesParagraphOrder(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t xml:space="preserve">Первый </w:t></w:r><w:r><w:t>абзац</w:t></w:r></w:p>
    <w:p><w:r><w:t>   </w:t></w:r></w:p>
    <w:p><w:hyperlink><w:r><w:t xml:space="preserve">Второй </w:t></w:r></w:hyperlink><w:r><w:t>абзац</w:t></w:r></w:p>
  </w:body>
</w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseDOCX() error = %v", err)
	}
	if parsed.Title != "" || len(parsed.Chapters) != 1 || parsed.Chapters[0].Title != "" {
		t.Fatalf("ParseDOCX() = %#v, want one untitled chapter", parsed)
	}
	want := []string{"Первый абзац", "Второй абзац"}
	got := textsOf(parsed.Chapters[0].Paragraphs)
	if len(got) != len(want) {
		t.Fatalf("paragraphs = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paragraph %d = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestParseDOCXSupportsStrictWordprocessingMLNamespace(t *testing.T) {
	document := `<w:document xmlns:w="http://purl.oclc.org/ooxml/wordprocessingml/main"><w:body><w:p><w:r><w:t>Strict paragraph</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseDOCX(strict namespace) error = %v", err)
	}
	if len(parsed.Chapters) != 1 || len(parsed.Chapters[0].Paragraphs) != 1 || parsed.Chapters[0].Paragraphs[0].Text() != "Strict paragraph" {
		t.Fatalf("ParseDOCX(strict namespace) = %#v", parsed)
	}
}

func TestParseDOCXPreservesInlineSeparators(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
	}{
		{name: "transitional", namespace: docxWordprocessingMLNamespace},
		{name: "strict", namespace: docxStrictWordprocessingMLNamespace},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := `<w:document xmlns:w="` + test.namespace + `"><w:body><w:p><w:r>` +
				`<w:t>до</w:t><w:tab/><w:t>табуляции</w:t><w:br/><w:t>разрыва</w:t><w:cr/><w:t>возврата</w:t>` +
				`</w:r></w:p></w:body></w:document>`
			data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

			parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("ParseDOCX() error = %v", err)
			}
			want := []ChapterImport{{Paragraphs: paragraphsOf("до\tтабуляции\nразрыва\nвозврата")}}
			if !reflect.DeepEqual(parsed.Chapters, want) {
				t.Fatalf("chapters = %#v, want %#v", parsed.Chapters, want)
			}
		})
	}
}

func TestParseDOCXCreatesChaptersFromHeading1Styles(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
		style     string
	}{
		{name: "heading1", namespace: docxWordprocessingMLNamespace, style: "Heading1"},
		{name: "heading 1 case insensitive", namespace: docxWordprocessingMLNamespace, style: "hEaDiNg 1"},
		{name: "localized", namespace: docxWordprocessingMLNamespace, style: "Заголовок1"},
		{name: "localized spaced case insensitive", namespace: docxWordprocessingMLNamespace, style: "зАГОЛОВОК 1"},
		{name: "strict", namespace: docxStrictWordprocessingMLNamespace, style: "Heading1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := `<w:document xmlns:w="` + test.namespace + `"><w:body>` +
				`<w:p><w:r><w:t>Вступление</w:t></w:r></w:p>` +
				`<w:p><w:r><w:t>Текст вступления</w:t></w:r></w:p>` +
				`<w:p><w:pPr><w:pStyle w:val="` + test.style + `"/></w:pPr><w:r><w:t>Первая глава</w:t></w:r></w:p>` +
				`<w:p><w:r><w:t>Текст главы</w:t></w:r></w:p>` +
				`</w:body></w:document>`
			data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

			parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("ParseDOCX() error = %v", err)
			}
			want := []ChapterImport{
				{Paragraphs: paragraphsOf("Вступление", "Текст вступления")},
				{Title: "Первая глава", Paragraphs: paragraphsOf("Текст главы")},
			}
			if !reflect.DeepEqual(parsed.Chapters, want) {
				t.Fatalf("chapters = %#v, want %#v", parsed.Chapters, want)
			}
		})
	}
}

// TestParseDOCXSplitsPlainChapterTitleLines: a manuscript without heading styles is split at its
// «Глава N…» lines; the line before the first chapter is the book title and prose that starts
// with «Глава» stays prose.
func TestParseDOCXSplitsPlainChapterTitleLines(t *testing.T) {
	var body strings.Builder
	for _, text := range []string{"13 том. Черновик", "Глава 618. Колизей", "После пары наводящих вопросов.", "Глава гильдии кивнул.",
		"Глава 619. Вопрос", "Ворон выяснил.", "Глава 630", "Глава 6"} {
		body.WriteString(`<w:p><w:pPr><w:pStyle w:val="a"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`)
	}
	document := `<w:document xmlns:w="` + docxWordprocessingMLNamespace + `"><w:body>` + body.String() + `</w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseDOCX() error = %v", err)
	}
	want := BookImport{Title: "13 том. Черновик", Chapters: []ChapterImport{
		{Title: "Глава 618. Колизей", Paragraphs: paragraphsOf("После пары наводящих вопросов.", "Глава гильдии кивнул.")},
		{Title: "Глава 619. Вопрос", Paragraphs: paragraphsOf("Ворон выяснил.")},
		{Title: "Глава 630"},
		{Title: "Глава 6"},
	}}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("ParseDOCX() = %#v, want %#v", parsed, want)
	}
	plan, err := buildImportPlan(ImportFileMetadata{SourceFileName: "13 том. Черновик.docx"}, parsed)
	if err != nil || len(plan.Chapters) != 2 || plan.BookTitle != "13 том. Черновик" {
		t.Fatalf("plan = %#v, err %v; want the two chapters with text", plan, err)
	}
}

// TestParseDOCXResolvesHeadingStylesFromStylesXML: Russian Word saves «Заголовок 1» under the
// style id «1» (the author's volume 13 came to production as one chapter of 1050 paragraphs
// because of it); a style based on it and a paragraph with outline level 0 are chapter titles
// too, and the single line before the first of them is the book title.
func TestParseDOCXResolvesHeadingStylesFromStylesXML(t *testing.T) {
	styles := `<w:styles xmlns:w="` + docxWordprocessingMLNamespace + `">` +
		`<w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="a5"><w:name w:val="Глава"/><w:basedOn w:val="1"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="2"><w:name w:val="heading 2"/><w:pPr><w:outlineLvl w:val="1"/></w:pPr></w:style>` +
		`</w:styles>`
	paragraph := func(properties, text string) string {
		return `<w:p><w:pPr>` + properties + `</w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	document := `<w:document xmlns:w="` + docxWordprocessingMLNamespace + `"><w:body>` + paragraph(``, "13 том") +
		paragraph(`<w:pStyle w:val="1"/>`, "Колизей") + paragraph(``, "Текст 1") +
		paragraph(`<w:pStyle w:val="a5"/>`, "Вопрос") + paragraph(`<w:pStyle w:val="2"/>`, "Сцена") + paragraph(``, "Текст 2") +
		paragraph(`<w:outlineLvl w:val="0"/>`, "Изолятор") + paragraph(``, "Текст 3") +
		`</w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document}, docxEntryForTest{name: "word/styles.xml", body: styles})

	parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseDOCX() error = %v", err)
	}
	want := BookImport{Title: "13 том", Chapters: []ChapterImport{
		{Title: "Колизей", Paragraphs: paragraphsOf("Текст 1")},
		{Title: "Вопрос", Paragraphs: paragraphsOf("Сцена", "Текст 2")},
		{Title: "Изолятор", Paragraphs: paragraphsOf("Текст 3")},
	}}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("ParseDOCX() = %#v, want %#v", parsed, want)
	}
}

func TestParseDOCXKeepsHeading2InUntitledChapter(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Подзаголовок</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Текст</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	parsed, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseDOCX() error = %v", err)
	}
	want := []ChapterImport{{Paragraphs: paragraphsOf("Подзаголовок", "Текст")}}
	if !reflect.DeepEqual(parsed.Chapters, want) {
		t.Fatalf("chapters = %#v, want %#v", parsed.Chapters, want)
	}
}

func TestParseDOCXRejectsMalformedDocumentXML(t *testing.T) {
	data := buildDOCXForTest(t, docxEntryForTest{
		name: "word/document.xml",
		body: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`,
	})

	if _, err := ParseDOCX(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("ParseDOCX() error = nil, want malformed XML rejection")
	}
}

func TestParseDOCXRejectsDuplicateDocumentXML(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Text</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t,
		docxEntryForTest{name: "word/document.xml", body: document},
		docxEntryForTest{name: "word/document.xml", body: document},
	)

	if _, err := ParseDOCX(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("ParseDOCX() error = nil, want duplicate document.xml rejection")
	}
}

func TestParseDOCXRejectsOversizedDocumentXML(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` +
		strings.Repeat("x", 32*1024*1024+1) +
		`</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	if _, err := ParseDOCX(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("ParseDOCX() error = nil, want oversized document.xml rejection")
	}
}

func TestReadDOCXDocumentRejectsActualOversize(t *testing.T) {
	reader := io.LimitReader(endlessDOCXReader{}, maxDOCXDocumentXMLBytes+1)

	if _, err := readDOCXDocument(reader); err == nil {
		t.Fatal("readDOCXDocument() error = nil, want actual-read size rejection")
	}
}

// TestParseDOCXStopsAtTheLimits (R2.2): chapters are assembled as the document is read, and the
// parser stops at the first chapter or paragraph over the limits — with heading styles before a
// broken tail it never reaches, and with plain «Глава N» lines in a whole document.
func TestParseDOCXStopsAtTheLimits(t *testing.T) {
	heading := func(text string) string {
		return `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	paragraph := func(text string) string { return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>` }
	var headed, titled strings.Builder
	for number := 1; number <= maxImportedChapters+1; number++ {
		headed.WriteString(heading(fmt.Sprintf("Глава %d", number)) + paragraph("Текст."))
		titled.WriteString(paragraph(fmt.Sprintf("Глава %d", number)) + paragraph("Текст."))
	}
	long := heading("Пролог") + heading("Глава 1") + strings.Repeat(paragraph(strings.Repeat("я", 1_000_000)), 11)
	const brokenTail = `<w:p><w:r><w:t>`
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{name: "heading chapters", body: headed.String() + brokenTail, want: ErrImportTooManyChapters},
		{name: "chapter title lines", body: titled.String() + `</w:body></w:document>`, want: ErrImportTooManyChapters},
		{name: "text", body: long + brokenTail, want: ErrImportTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := `<w:document xmlns:w="` + docxWordprocessingMLNamespace + `"><w:body>` + test.body
			data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

			_, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
			if !errors.Is(err, test.want) {
				t.Fatalf("ParseDOCX() error = %v, want %v", err, test.want)
			}
		})
	}
}

type endlessDOCXReader struct{}

func (endlessDOCXReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	return len(buffer), nil
}

type docxEntryForTest struct {
	name string
	body string
}

func buildDOCXForTest(t *testing.T, entries ...docxEntryForTest) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatalf("create DOCX entry %q: %v", entry.name, err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write DOCX entry %q: %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close DOCX fixture: %v", err)
	}
	return buffer.Bytes()
}
