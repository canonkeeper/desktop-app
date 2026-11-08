package chapterfile

import (
	"archive/zip"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// docxOf packs a body and styles into a Word file of the test.
func docxOf(t *testing.T, body, styles string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
			`xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"><w:body>` + body + `</w:body></w:document>`,
	}
	if styles != "" {
		parts["word/styles.xml"] = `<?xml version="1.0" encoding="UTF-8"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + styles + `</w:styles>`
	}
	for name, content := range parts {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestAWordFileIsReadParagraphByParagraphWithItsFaces(t *testing.T) {
	body := `<w:p><w:pPr><w:pStyle w:val="1"/></w:pPr><w:r><w:t>Глава 7. Переправа</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t xml:space="preserve">Кайл </w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t>шёл</w:t></w:r>` +
		`<w:r><w:rPr><w:b w:val="1"/></w:rPr><w:t xml:space="preserve"> к реке</w:t></w:r>` +
		`<w:r><w:rPr><w:i w:val="0"/></w:rPr><w:t>,</w:t><w:tab/><w:t>не</w:t></w:r>` +
		`<w:del w:id="1" w:author="x"><w:r><w:delText> удалено</w:delText></w:r></w:del>` +
		`<w:ins w:id="2" w:author="x"><w:r><w:t xml:space="preserve"> оглядываясь</w:t></w:r></w:ins>` +
		`<w:r><w:footnoteReference w:id="3"/></w:r>` +
		`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> PAGE </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>.</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>` +
		`<w:p/>` +
		`<w:p><w:r><w:t>Ольм молчал</w:t></w:r><w:r><w:drawing><w:txbxContent><w:p><w:r><w:t>надпись на рисунке</w:t></w:r></w:p></w:txbxContent></w:drawing></w:r><w:r><w:t>.</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Строка таблицы</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
	styles := `<w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/></w:style>`
	document, err := Parse("Глава.docx", docxOf(t, body, styles))
	if err != nil {
		t.Fatal(err)
	}
	if document.Format != FormatDOCX || document.TitleText() != "Глава 7. Переправа" {
		t.Fatalf("format %q, title %q", document.Format, document.TitleText())
	}
	want := []Paragraph{
		{Runs: []Run{{Text: "Кайл "}, {Text: "шёл", Italic: true}, {Text: " к реке", Bold: true}, {Text: ", не оглядываясь."}}},
		{},
		{Runs: []Run{{Text: "Ольм молчал."}}},
		{Runs: []Run{{Text: "Строка таблицы"}}},
	}
	if !reflect.DeepEqual(document.Paragraphs, want) {
		t.Fatalf("paragraphs:\n%#v\nwant\n%#v", document.Paragraphs, want)
	}
	if document.Text() != "Кайл шёл к реке, не оглядываясь.\n\nОльм молчал.\n\nСтрока таблицы" {
		t.Fatalf("text %q", document.Text())
	}
}

func TestAWordFileWithoutAHeadingTakesTheLineOfAChapterAsItsTitle(t *testing.T) {
	body := `<w:p/><w:p><w:r><w:t>Глава 12. Мост</w:t></w:r></w:p><w:p><w:r><w:t>Текст главы.</w:t></w:r></w:p>`
	document, err := Parse("x.DOCX", docxOf(t, body, ""))
	if err != nil {
		t.Fatal(err)
	}
	if document.TitleText() != "Глава 12. Мост" || document.Text() != "Текст главы." {
		t.Fatalf("title %q, text %q", document.TitleText(), document.Text())
	}
	plain, err := Parse("x.docx", docxOf(t, `<w:p><w:r><w:t>Просто начало главы.</w:t></w:r></w:p>`, ""))
	if err != nil || plain.Title != nil {
		t.Fatalf("a first line that names no chapter is text: %+v %v", plain, err)
	}
}

func TestTextFilesInTheirEncodingsGiveOneText(t *testing.T) {
	text := "Глава 3. Ночь\r\n\r\nКайл не спал.\r\n  Ольм ⟦смеялся⟧.\n"
	want := "Кайл не спал.\n\nОльм [смеялся]."
	utf16, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	cp1251, err := charmap.Windows1251.NewEncoder().Bytes([]byte(strings.ReplaceAll(text, "⟦смеялся⟧", "[смеялся]")))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"utf-8": []byte(text), "utf-8 with its mark": append([]byte{0xEF, 0xBB, 0xBF}, text...), "utf-16": utf16, "windows-1251": cp1251,
	} {
		document, err := Parse("глава.txt", data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if document.TitleText() != "Глава 3. Ночь" || document.Text() != want {
			t.Fatalf("%s: title %q, text %q", name, document.TitleText(), document.Text())
		}
	}
	pasted, err := ParseText("Первая строка.\nВторая строка.")
	if err != nil || pasted.Format != FormatPasted || len(pasted.Lines()) != 2 {
		t.Fatalf("pasted text: %+v %v", pasted, err)
	}
}

func TestMarkdownKeepsItalicAndBoldAndJoinsTheLinesOfABlock(t *testing.T) {
	text := "# Глава 5. Колодец\n\nКайл *заглянул*\nв колодец и **замер**.\n\n* * *\n\nЕго звали \\*Звёздочка\\*, а файл — snake_case_name.\n\n## Подзаголовок\nНезакрытая *звезда.\n"
	document, err := Parse("chapter.md", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if document.TitleText() != "Глава 5. Колодец" {
		t.Fatalf("title %q", document.TitleText())
	}
	want := []Paragraph{
		{Runs: []Run{{Text: "Кайл "}, {Text: "заглянул", Italic: true}, {Text: " в колодец и "}, {Text: "замер", Bold: true}, {Text: "."}}},
		{Runs: []Run{{Text: "* * *"}}},
		{Runs: []Run{{Text: "Его звали *Звёздочка*, а файл — snake_case_name."}}},
		{Runs: []Run{{Text: "Подзаголовок"}}},
		{Runs: []Run{{Text: "Незакрытая *звезда."}}},
	}
	if !reflect.DeepEqual(document.Paragraphs, want) {
		t.Fatalf("paragraphs:\n%#v\nwant\n%#v", document.Paragraphs, want)
	}
}

func TestTheLimitsOfAChapterFile(t *testing.T) {
	for name, check := range map[string]struct {
		file string
		data []byte
		want error
	}{
		"too large":   {"a.txt", bytes.Repeat([]byte("а"), MaxFileBytes/2+1), ErrTooLarge},
		"too long":    {"a.txt", []byte(strings.Repeat("слово ", MaxTextRunes/6+1)), ErrTooLong},
		"pdf":         {"a.pdf", []byte("%PDF"), ErrFormat},
		"broken word": {"a.docx", []byte("not a zip"), ErrUnreadable},
		"only spaces": {"a.txt", []byte("  \n\t\n"), ErrEmpty},
		"no name":     {"", []byte("текст"), ErrFormat},
	} {
		if _, err := Parse(check.file, check.data); !errors.Is(err, check.want) {
			t.Fatalf("%s: %v, want %v", name, err, check.want)
		}
	}
	if _, err := ParseText(strings.Repeat("а", MaxFileBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a pasted text over the limit: %v", err)
	}
}
