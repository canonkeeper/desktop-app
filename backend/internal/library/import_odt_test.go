package library

import (
	"bytes"
	"strings"
	"testing"
)

const odtNamespacesForTest = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
	`xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" ` +
	`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
	`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" ` +
	`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" ` +
	`xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/"`

// odtForTest packs an OpenDocument text: content.xml of its automatic styles and its body, styles.xml of its common
// styles when given, meta.xml of its title when given, and the other entries as they are.
func odtForTest(t *testing.T, automatic, body, common, title string, extra ...zipEntryForTest) []byte {
	t.Helper()
	entries := []zipEntryForTest{
		{name: "mimetype", body: []byte("application/vnd.oasis.opendocument.text")},
		{name: "content.xml", body: []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document-content ` + odtNamespacesForTest +
			`><office:automatic-styles>` + automatic + `</office:automatic-styles><office:body><office:text>` + body +
			`</office:text></office:body></office:document-content>`)},
	}
	if common != "" {
		entries = append(entries, zipEntryForTest{name: "styles.xml", body: []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document-styles ` +
			odtNamespacesForTest + `><office:styles>` + common + `</office:styles></office:document-styles>`)})
	}
	if title != "" {
		entries = append(entries, zipEntryForTest{name: "meta.xml", body: []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document-meta ` +
			odtNamespacesForTest + `><office:meta><dc:title>` + title + `</dc:title></office:meta></office:document-meta>`)})
	}
	return zipForTest(t, append(entries, extra...)...)
}

func parseODTForTest(t *testing.T, data []byte) BookImport {
	t.Helper()
	book, err := ParseODT(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseODT() error = %v", err)
	}
	return book
}

// K4.1, K4.2: headings of the first level are the chapters, the title is dc:title; a span takes the italic and bold of
// its automatic style and of the parent of that style, a paragraph those of its own; a note stands in square brackets,
// <text:s/> are spaces; tables, frames, a table of contents and what a revision deleted are left out.
func TestParseODTReadsChaptersFacesAndNotes(t *testing.T) {
	automatic := `<style:style style:name="T1" style:family="text"><style:text-properties fo:font-style="italic"/></style:style>` +
		`<style:style style:name="T2" style:family="text" style:parent-style-name="Strong_20_Emphasis"/>` +
		`<style:style style:name="T3" style:family="text"><style:text-properties fo:font-style="normal"/></style:style>` +
		`<style:style style:name="P1" style:family="paragraph" style:parent-style-name="Quote"/>`
	common := `<style:style style:name="Strong_20_Emphasis" style:family="text"><style:text-properties fo:font-weight="700"/></style:style>` +
		`<style:style style:name="Quote" style:family="paragraph"><style:text-properties fo:font-style="italic"/></style:style>`
	body := `<text:sequence-decls><text:sequence-decl text:name="Table"/></text:sequence-decls>` +
		`<text:table-of-content><text:index-body><text:p>Глава 1 ........ 3</text:p></text:index-body></text:table-of-content>` +
		`<text:tracked-changes><text:changed-region><text:deletion><text:p>Удалённое правкой.</text:p></text:deletion></text:changed-region></text:tracked-changes>` +
		`<text:h text:outline-level="1">Глава 1. <text:span text:style-name="T1">Порт</text:span></text:h>` +
		`<text:p>Корабль <text:span text:style-name="T1">вошёл</text:span> в <text:span text:style-name="T2">гавань</text:span>` +
		`<text:note text:note-class="footnote"><text:note-citation>1</text:note-citation><text:note-body><text:p>Гавань   Белого порта.</text:p></text:note-body></text:note>` +
		` на   рассвете.<text:s text:c="2"/>Марта<text:tab/>ждала.<text:line-break/>Ночью стихло.</text:p>` +
		`<text:p text:style-name="P1">Море помнит <text:span text:style-name="T3">всё</text:span>.</text:p>` +
		`<table:table><table:table-row><table:table-cell><text:p>Ячейка</text:p></table:table-cell></table:table-row></table:table>` +
		`<text:p><draw:frame><draw:text-box><text:p>Подпись к картинке</text:p></draw:text-box></draw:frame></text:p>` +
		`<text:list><text:list-item><text:p>Сеть.</text:p></text:list-item></text:list>` +
		`<text:h text:outline-level="2">Сцена у причала</text:h>` +
		`<text:h text:outline-level="1">Глава 2</text:h><text:section><text:p>Ночью начался <text:a xlink:href="#x" xmlns:xlink="http://www.w3.org/1999/xlink">шторм</text:a>.</text:p></text:section>`
	book := parseODTForTest(t, odtForTest(t, automatic, body, common, "Северный ветер"))
	assertOutline(t, book,
		"# Северный ветер",
		"## Глава 1. Порт",
		"Корабль *вошёл* в **гавань** [Гавань Белого порта.] на рассвете.  Марта\tждала.\nНочью стихло.",
		"*Море помнит* всё*.*",
		"Сеть.",
		"Сцена у причала",
		"## Глава 2",
		"Ночью начался шторм.",
	)
}

// K4.2: without headings the chapters start at the lines that name them, and the first short line names the book.
func TestParseODTWithoutHeadingsFindsTheTitleLines(t *testing.T) {
	body := `<text:p>Северный ветер</text:p><text:p/><text:p>Глава 1</text:p><text:p>Корабль вошёл в гавань.</text:p>` +
		`<text:p>Глава 2</text:p><text:p>Ночью начался шторм.</text:p>`
	assertOutline(t, parseODTForTest(t, odtForTest(t, "", body, "", "")),
		"# Северный ветер", "## Глава 1", "Корабль вошёл в гавань.", "## Глава 2", "Ночью начался шторм.")
	assertOutline(t, parseODTForTest(t, odtForTest(t, "", `<text:p>Корабль вошёл в гавань.</text:p>`, "", "  ")),
		"# ", "## ", "Корабль вошёл в гавань.", "(unsplit)")
}

// K4.3: an archive without content.xml, a locked document, broken markup and what is not an archive are an invalid
// ODT.
func TestParseODTRefusesABrokenOrLockedDocument(t *testing.T) {
	locked := odtForTest(t, "", `<text:p>Текст.</text:p>`, "", "", zipEntryForTest{name: "META-INF/manifest.xml", body: []byte(
		`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"><manifest:file-entry manifest:full-path="content.xml">` +
			`<manifest:encryption-data manifest:checksum-type="SHA1/1K"/></manifest:file-entry></manifest:manifest>`)})
	for name, data := range map[string][]byte{
		"no content":      zipForTest(t, zipEntryForTest{name: "mimetype", body: []byte("application/vnd.oasis.opendocument.text")}),
		"locked":          locked,
		"broken markup":   odtForTest(t, "", `<text:p>Обрыв`, "", ""),
		"not an archive":  append([]byte("PK\x03\x04"), bytes.Repeat([]byte{9}, 40)...),
		"no text in body": odtForTest(t, "", `<text:p/>`, "", ""),
	} {
		_, err := ParseODT(bytes.NewReader(data), int64(len(data)))
		want := "invalid_odt_document"
		if name == "no text in body" {
			want = "empty_import_content"
		}
		if failure := importParseFailure(err, ".odt"); err == nil || failure.Code != want {
			t.Fatalf("%s: failure = %q (%v), want %q", name, failure.Code, err, want)
		}
		if strings.Contains(importParseFailure(err, ".odt").Message, "Обрыв") {
			t.Fatalf("%s: the message carries the text of the file", name)
		}
	}
}
