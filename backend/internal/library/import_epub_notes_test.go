package library

import (
	"strings"
	"testing"
)

// TestParseEPUBKeepsFootnotesInTheText: the text of a footnote stands in square brackets right after the words it
// explains — without its mark, its number, the way back and the last full stop — and the body of the footnote is
// neither a paragraph nor a chapter (specs/2026-11-10-ux1-names.md N7.2; J-13: bare «[1]» and lost translations).
func TestParseEPUBKeepsFootnotesInTheText(t *testing.T) {
	opf := func(documents ...string) epubFile {
		return epubFile{"OPS/content.opf", bookOPF(false, false, "", documents...)}
	}
	one := func(t *testing.T, body string) []string {
		t.Helper()
		book := parseBook(t, opf("c1.xhtml"), epubFile{"OPS/c1.xhtml", bookPage(body)})
		if len(book.Chapters) != 1 {
			t.Fatalf("chapters = %q", chapterTitles(book))
		}
		return markdownOf(t, book.Chapters[0])
	}

	t.Run("the markup of a wiki library", func(t *testing.T) {
		got := one(t, `<h2>Глава I</h2>
<p>Он был человек чести (homme d’honneur<sup about="#mwt2" class="mw-ref reference" id="cite_ref-1-n167" rel="dc:references"><a href="#cite_note-1-n173" epub:type="noteref"><span class="mw-reflink-text"><span class="cite-bracket">[</span>1<span class="cite-bracket">]</span></span></a></sup>). Потом <i>ушёл</i>.</p>
<p>— Trois!<sup class="mw-ref reference" id="cite_ref-2-n168"><a href="#cite_note-2-n174" epub:type="noteref"><span class="mw-reflink-text"><span class="cite-bracket">[</span>2<span class="cite-bracket">]</span></span></a></sup> — крикнул он.</p>
<ol class="mw-references references">
<li about="#cite_note-1" id="cite_note-1-n173" epub:type="footnote"><span class="mw-cite-backlink"><a href="#cite_ref-1-n167" rel="mw:referencedBy"><span class="mw-linkback-text">↑</span></a></span> <span class="mw-reference-text reference-text">человек чести (франц.).</span></li>
<li about="#cite_note-2" id="cite_note-2-n174" epub:type="footnote"><span class="mw-cite-backlink"><a href="#cite_ref-2-n168" rel="mw:referencedBy"><span class="mw-linkback-text">↑</span></a></span> <span class="mw-reference-text reference-text">Три! (франц.).</span></li>
</ol>`)
		want := []string{
			"Он был человек чести (homme d’honneur [человек чести (франц.)]). Потом *ушёл*.|Он был человек чести (homme d’honneur [человек чести (франц.)]). Потом ушёл.",
			"— Trois! [Три! (франц.)] — крикнул он.|— Trois! [Три! (франц.)] — крикнул он.",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("paragraphs = %q, want %q", got, want)
		}
	})

	t.Run("an aside of EPUB 3 with its number", func(t *testing.T) {
		got := one(t, `<p>Брод<a epub:type="noteref" href="#n1">1</a> был мелок.</p><aside epub:type="footnote" id="n1"><p>1. Мелкое место реки.</p><p>Его переходят пешком.</p></aside><p>Мара перешла.</p>`)
		want := []string{"Брод [Мелкое место реки. Его переходят пешком] был мелок.|Брод [Мелкое место реки. Его переходят пешком] был мелок.", "Мара перешла.|Мара перешла."}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("paragraphs = %q, want %q", got, want)
		}
	})

	t.Run("marks that lead back to each other inside paragraphs", func(t *testing.T) {
		got := one(t, `<p>Текст<a id="r5" href="#n5">[5]</a> дальше и<a id="r6" href="#n6"><sup>*</sup></a>.</p><p>Вторая строка.</p><p><a id="n5" href="#r5">[5]</a> Пятая сноска.</p><p><a id="n6" href="#r6">*</a> Звезда?</p>`)
		want := []string{"Текст [Пятая сноска] дальше и [Звезда?].|Текст [Пятая сноска] дальше и [Звезда?].", "Вторая строка.|Вторая строка."}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("paragraphs = %q, want %q", got, want)
		}
	})

	t.Run("a file of notes in the spine", func(t *testing.T) {
		book := parseBook(t, opf("c1.xhtml", "notes.xhtml", "c2.xhtml"),
			epubFile{"OPS/c1.xhtml", bookPage(`<h2>Глава I</h2><p>Пристань<a href="notes.xhtml#n2"><sup>[2]</sup></a> пуста.</p>`)},
			epubFile{"OPS/notes.xhtml", bookPage(`<h1>Примечания</h1><div class="note" id="n2"><p class="title">2</p><p>Место, где стоят лодки.</p></div>`)},
			epubFile{"OPS/c2.xhtml", bookPage(`<h2>Глава II</h2><p>Утром пришли лодки.</p>`)})
		if got, want := chapterTitles(book), "Глава I|Глава II"; strings.Join(got, "|") != want {
			t.Fatalf("chapters = %q, want %q", got, want)
		}
		if got := textsOf(book.Chapters[0].Paragraphs); len(got) != 1 || got[0] != "Пристань [Место, где стоят лодки] пуста." {
			t.Fatalf("chapter I = %q", got)
		}
	})

	t.Run("what is no footnote stays as it was", func(t *testing.T) {
		got := one(t, `<h2 id="h3">Глава III</h2><p>См. <a href="#h3">3</a> и <a href="#place">Белый порт</a>, а также<a epub:type="noteref" href="#gone">7</a> и сайт <a href="https://example.org/1">1</a>.</p>
<p id="place">Белый порт стоит у моря<a href="#place">*</a>.</p><aside epub:type="footnote" id="alone"><p>Сама по себе.</p></aside>`)
		want := []string{
			"См. 3 и Белый порт, а также7 и сайт 1.|См. 3 и Белый порт, а также7 и сайт 1.",
			"Белый порт стоит у моря\\*.|Белый порт стоит у моря*.",
			"Сама по себе.|Сама по себе.",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("paragraphs = %q, want %q", got, want)
		}
	})

	t.Run("a mark in a heading goes without its footnote", func(t *testing.T) {
		book := parseBook(t, opf("c1.xhtml"), epubFile{"OPS/c1.xhtml", bookPage(`<h2>Глава I<a epub:type="noteref" href="#n1">1</a></h2><p>Мара вышла.</p><aside epub:type="footnote" id="n1"><p>О названии.</p></aside>`)})
		if got := chapterTitles(book); len(got) != 1 || got[0] != "Глава I" || len(book.Chapters[0].Paragraphs) != 1 {
			t.Fatalf("chapters = %q with %d paragraphs", got, len(book.Chapters[0].Paragraphs))
		}
	})
}
