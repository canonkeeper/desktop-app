package library

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// paragraphsOf are paragraphs of plain text, one run each.
func paragraphsOf(texts ...string) []chapterfile.Paragraph {
	paragraphs := make([]chapterfile.Paragraph, len(texts))
	for i, text := range texts {
		paragraphs[i] = chapterfile.Paragraph{Runs: []chapterfile.Run{{Text: text}}}
	}
	return paragraphs
}

// textsOf are the words of paragraphs without their faces.
func textsOf(paragraphs []chapterfile.Paragraph) []string {
	texts := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		texts[i] = paragraph.Text()
	}
	return texts
}

// markdownOf is what the book keeps of the paragraphs of a chapter: the markdown and the plain text of each.
func markdownOf(t *testing.T, chapter ChapterImport) []string {
	t.Helper()
	var kept []string
	for _, input := range importParagraphInputs(chapter.Paragraphs) {
		kept = append(kept, input.Markdown+"|"+*input.Text)
	}
	return kept
}

// specs/2026-11-08-italic-bold-in-text.md TF4.1: an FB2 book brings the italic of <emphasis> and the bold of <strong>
// into the paragraphs of its chapters; the book keeps them in markdown and the words in plain_text as before.
func TestAnFB2BookBringsItsItalicAndBold(t *testing.T) {
	book, err := ParseFB2(strings.NewReader(`<FictionBook><body><section><title><p>Глава <emphasis>1</emphasis></p></title>
<p>Он <emphasis>пришёл</emphasis>   <strong>домой <emphasis>сам</emphasis></strong>.</p>
<p>* * *</p></section></body></FictionBook>`))
	if err != nil {
		t.Fatal(err)
	}
	if book.Chapters[0].Title != "Глава 1" {
		t.Fatalf("title %q", book.Chapters[0].Title)
	}
	want := []string{"Он *пришёл* **домой *сам***.|Он пришёл домой сам.", `\* \* \*|* * *`}
	if got := markdownOf(t, book.Chapters[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("the book keeps %q, want %q", got, want)
	}
}

// TF4.1: an EPUB book brings the italic of <i>, <em> and font-style and the bold of <b>, <strong> and font-weight.
func TestAnEPUBBookBringsItsItalicAndBold(t *testing.T) {
	var blocks []epubBlock
	err := scanEPUBBlocks([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h1>Глава <i>первая</i></h1>
<p>Он <em>пришёл</em> и <b>сел</b>, <strong>сказав</strong>: <span style="font-style: italic">«Тише»</span>.</p>
<p style="font-weight:700">Весь жирный, <span style="font-weight: normal">кроме</span> <i>этого</i>.</p>
<p><span style="FONT-STYLE:oblique; color: red">Наклон</span> и <i><span style="font-style:normal">прямо</span></i>.</p>
</body></html>`), func(block epubBlock) error {
		blocks = append(blocks, block)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, block := range blocks[1:] {
		got = append(got, markdownOf(t, ChapterImport{Paragraphs: []chapterfile.Paragraph{{Runs: block.runs}}})...)
	}
	want := []string{
		"Он *пришёл* и **сел**, **сказав**: *«Тише»*.|Он пришёл и сел, сказав: «Тише».",
		"**Весь жирный,** кроме ***этого*.**|Весь жирный, кроме этого.",
		"*Наклон* и прямо.|Наклон и прямо.",
	}
	if blocks[0].text != "Глава первая" || !reflect.DeepEqual(got, want) {
		t.Fatalf("heading %q, the book keeps %q, want %q", blocks[0].text, got, want)
	}
}

// TF4.1, TF4.2: a Word book brings the italic and bold Word shows — its own toggles, a character style on a chain, a
// paragraph style — and a revision of formatting is not its face.
func TestAWordBookBringsTheFacesWordShows(t *testing.T) {
	styles := `<w:styles xmlns:w="` + docxWordprocessingMLNamespace + `">` +
		`<w:style w:type="character" w:styleId="Base"><w:name w:val="Base"/><w:rPr><w:i/></w:rPr></w:style>` +
		`<w:style w:type="character" w:styleId="Emph"><w:name w:val="Emphasis"/><w:basedOn w:val="Base"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:rPr><w:b/></w:rPr></w:style>` +
		`</w:styles>`
	document := `<w:document xmlns:w="` + docxWordprocessingMLNamespace + `"><w:body>` +
		`<w:p><w:r><w:t xml:space="preserve">Кира шла </w:t></w:r><w:r><w:rPr><w:rStyle w:val="Emph"/></w:rPr><w:t>медленно</w:t></w:r>` +
		`<w:r><w:rPr><w:b/><w:rPrChange w:id="1" w:author="x"><w:rPr><w:i/></w:rPr></w:rPrChange></w:rPr><w:t xml:space="preserve"> и тихо</w:t></w:r>` +
		`<w:r><w:t>.</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr><w:r><w:t xml:space="preserve">Цитата </w:t></w:r><w:r><w:rPr><w:b w:val="0"/></w:rPr><w:t>сноска</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document}, docxEntryForTest{name: "word/styles.xml", body: styles})
	book, err := ParseDOCX(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Кира шла *медленно* **и тихо**.|Кира шла медленно и тихо.", "**Цитата** сноска|Цитата сноска"}
	if len(book.Chapters) != 1 || !reflect.DeepEqual(markdownOf(t, book.Chapters[0]), want) {
		t.Fatalf("the book keeps %#v, want %q", book.Chapters, want)
	}
}
