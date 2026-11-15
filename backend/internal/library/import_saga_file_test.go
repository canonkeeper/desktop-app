package library

import (
	"archive/zip"
	"bytes"
	"testing"

	"canonkeeper/backend/internal/domain/sagaarchive"
)

// specs/2026-11-13-desk-sagafile.md SF1.10, open question 3: a made-up book of each of the eight kinds an import takes —
// with italic and bold, a tab, a no-break space, stars and a backslash in the words, the brackets of the marks of a draft
// and a sign of control — gives paragraphs the reader of a saga file takes as they are: a saga made by imports is saved
// whole.
func TestTheBooksOfEveryKindPassTheChecksOfTheSagaFile(t *testing.T) {
	words := "Мара\tшла *к* заставе Тарена \\ ⟦через⟧ ⟨перевал⟩ ⟪к⟫ маяку\x0b."
	fb2 := `<?xml version="1.0" encoding="utf-8"?><FictionBook><description><title-info><book-title>Северный предел</book-title></title-info></description>` +
		`<body><section><title><p>Глава 1</p></title><p>` + escapeXML(words) + ` <emphasis>снег</emphasis> <strong>соль</strong></p></section></body></FictionBook>`
	var fb2zip bytes.Buffer
	archive := zip.NewWriter(&fb2zip)
	entry, _ := archive.Create("book.fb2")
	_, _ = entry.Write([]byte(fb2))
	_ = archive.Close()
	docx := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t xml:space="preserve">` + escapeXML(words) + ` </w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t>снег</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve"> соль</w:t></w:r></w:p>` +
		`</w:body></w:document>`})
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf":      minimalOPF("chapter1.xhtml"),
		"OEBPS/chapter1.xhtml": `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Глава 1</title></head><body>` +
			`<h1>Глава 1</h1><p>` + escapeXML(words) + ` <em>снег</em> <strong>соль</strong></p></body></html>`,
	})
	odt := odtForTest(t, "", `<text:p>`+escapeXML(words)+` <text:span>снег</text:span></text:p>`, "", "")
	rtf := `{\rtf1\ansi\deff0 ` + rtfUnicodeForTest("Мара шла *к* заставе Тарена \\ ⟦через⟧ ⟨перевал⟩ маяку.", "?") + ` {\i снег} {\b соль}\par}`
	books := map[string][]byte{
		".txt": []byte("Глава 1\n\n" + words + "\n"), ".md": []byte("# Глава 1\n\n" + words + " *снег* **соль**\n"),
		".fb2": []byte(fb2), ".fb2.zip": fb2zip.Bytes(), ".docx": docx, ".epub": epub, ".odt": odt, ".rtf": []byte(rtf),
	}
	for kind, data := range books {
		book, err := parseImportBook(data, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		paragraphs := 0
		for _, chapter := range book.Chapters {
			for _, paragraph := range importParagraphInputs(chapter.Paragraphs) {
				paragraphs++
				if !sagaarchive.ValidMarkdown(paragraph.Markdown) {
					t.Errorf("%s: the paragraph %q is refused by the reader of a saga file", kind, paragraph.Markdown)
				}
			}
		}
		if paragraphs == 0 {
			t.Errorf("%s: no paragraph", kind)
		}
	}
}

func escapeXML(text string) string {
	var out bytes.Buffer
	for _, r := range text {
		switch r {
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '&':
			out.WriteString("&amp;")
		case '\x0b':
			// XML 1.0 keeps no sign of control but tab and new lines.
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
