package bookexport

import (
	"fmt"
	"io"
)

const epubContainer = xmlDeclaration + `<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` +
	`<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

const epubStyles = `body { margin: 5%; line-height: 1.5; }
h1 { margin: 2em 0 1.5em; text-align: center; font-size: 1.5em; }
p { margin: 0; text-indent: 1.25em; }
.cover { margin: 0; text-align: center; }
.cover img { max-width: 100%; max-height: 100%; }
.book-title { margin: 30% 0 1em; text-align: center; font-size: 2em; font-weight: bold; }
.book-author { text-align: center; font-size: 1.2em; }
`

const epubDocumentStart = xmlDeclaration + `<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="ru" xml:lang="ru"><head><title>`

const epubDocumentHead = `</title><link rel="stylesheet" type="text/css" href="style.css"/></head>`

func epubChapterName(chapter int) string { return fmt.Sprintf("chapter-%04d.xhtml", chapter+1) }

func writeEPUB(writer io.Writer, book Book) error {
	pack := newPack(writer, book.Date)
	// The mimetype comes first, stored and without an extra field: that is how a reader tells an EPUB.
	if err := pack.stored("mimetype", []byte(FormatEPUB.MediaType())); err != nil {
		return err
	}
	if err := pack.markup("META-INF/container.xml", epubContainer); err != nil {
		return err
	}
	if err := pack.part("OEBPS/content.opf", func(out *output) error { writeEPUBPackage(out, book); return nil }); err != nil {
		return err
	}
	if err := pack.part("OEBPS/nav.xhtml", func(out *output) error { writeEPUBNavigation(out, book); return nil }); err != nil {
		return err
	}
	if err := pack.part("OEBPS/toc.ncx", func(out *output) error { writeEPUBContents(out, book); return nil }); err != nil {
		return err
	}
	if err := pack.markup("OEBPS/style.css", epubStyles); err != nil {
		return err
	}
	if book.Cover != nil {
		if err := pack.stored("OEBPS/"+book.Cover.fileName(), book.Cover.Content); err != nil {
			return err
		}
		if err := pack.part("OEBPS/cover.xhtml", func(out *output) error { writeEPUBCover(out, book); return nil }); err != nil {
			return err
		}
	}
	if err := pack.part("OEBPS/title.xhtml", func(out *output) error { writeEPUBTitle(out, book); return nil }); err != nil {
		return err
	}
	for chapter := range book.Chapters {
		paragraphs, err := book.Text(chapter)
		if err != nil {
			return err
		}
		if err := pack.part("OEBPS/"+epubChapterName(chapter), func(out *output) error {
			writeEPUBChapter(out, book.Chapters[chapter], paragraphs)
			return nil
		}); err != nil {
			return err
		}
	}
	return pack.zip.Close()
}

func writeEPUBPackage(out *output, book Book) {
	out.raw(xmlDeclaration + `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id" xml:lang="ru">` +
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="book-id">urn:uuid:`)
	out.text(book.ID)
	out.raw(`</dc:identifier><dc:title>`)
	out.text(book.Title)
	out.raw(`</dc:title><dc:creator>`)
	out.text(book.Author)
	out.raw(`</dc:creator><dc:language>ru</dc:language><dc:date>` + book.Date.UTC().Format("2006-01-02") + `</dc:date>` +
		`<meta property="dcterms:modified">` + book.Date.UTC().Format("2006-01-02T15:04:05Z") + `</meta>`)
	if book.Cover != nil {
		out.raw(`<meta name="cover" content="cover-image"/>`)
	}
	out.raw(`</metadata><manifest>` +
		`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` +
		`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` +
		`<item id="style" href="style.css" media-type="text/css"/>` +
		`<item id="title" href="title.xhtml" media-type="application/xhtml+xml"/>`)
	if book.Cover != nil {
		out.raw(`<item id="cover-image" href="` + book.Cover.fileName() + `" media-type="` + book.Cover.MediaType + `" properties="cover-image"/>` +
			`<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>`)
	}
	for chapter := range book.Chapters {
		out.raw(fmt.Sprintf(`<item id="chapter-%d" href="%s" media-type="application/xhtml+xml"/>`, chapter+1, epubChapterName(chapter)))
	}
	out.raw(`</manifest><spine toc="ncx">`)
	if book.Cover != nil {
		out.raw(`<itemref idref="cover"/>`)
	}
	out.raw(`<itemref idref="title"/>`)
	for chapter := range book.Chapters {
		out.raw(fmt.Sprintf(`<itemref idref="chapter-%d"/>`, chapter+1))
	}
	out.raw(`</spine></package>`)
}

// writeEPUBNavigation is the contents of EPUB 3. It stays out of the spine: a reader shows it as contents, not as a page.
func writeEPUBNavigation(out *output, book Book) {
	out.raw(epubDocumentStart + `Оглавление` + epubDocumentHead +
		`<body><nav epub:type="toc" role="doc-toc" aria-label="Оглавление"><h1>Оглавление</h1><ol>`)
	for chapter, title := range book.Chapters {
		out.raw(`<li><a href="` + epubChapterName(chapter) + `">`)
		out.text(title)
		out.raw(`</a></li>`)
	}
	out.raw(`</ol></nav></body></html>`)
}

// writeEPUBContents is the contents for the readers that do not know EPUB 3: many e-ink devices among them.
func writeEPUBContents(out *output, book Book) {
	out.raw(xmlDeclaration + `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head><meta name="dtb:uid" content="urn:uuid:`)
	out.text(book.ID)
	out.raw(`"/><meta name="dtb:depth" content="1"/><meta name="dtb:totalPageCount" content="0"/><meta name="dtb:maxPageNumber" content="0"/></head><docTitle><text>`)
	out.text(book.Title)
	out.raw(`</text></docTitle><navMap>`)
	for chapter, title := range book.Chapters {
		out.raw(fmt.Sprintf(`<navPoint id="point-%d" playOrder="%d"><navLabel><text>`, chapter+1, chapter+1))
		out.text(title)
		out.raw(`</text></navLabel><content src="` + epubChapterName(chapter) + `"/></navPoint>`)
	}
	out.raw(`</navMap></ncx>`)
}

func writeEPUBCover(out *output, book Book) {
	out.raw(epubDocumentStart)
	out.text(book.Title)
	out.raw(epubDocumentHead + `<body epub:type="cover"><p class="cover"><img src="` + book.Cover.fileName() + `" alt="`)
	out.text(coverDescription(book.Title))
	out.raw(`"/></p></body></html>`)
}

// writeEPUBTitle is the title page. Its lines are not a paragraph or a heading of the book: chapters are the only
// headings of level 1, and the import of this very file does not take the title page for a chapter.
func writeEPUBTitle(out *output, book Book) {
	out.raw(epubDocumentStart)
	out.text(book.Title)
	out.raw(epubDocumentHead + `<body epub:type="frontmatter"><section epub:type="titlepage"><div class="book-title">`)
	out.text(book.Title)
	out.raw(`</div><div class="book-author">`)
	out.text(book.Author)
	out.raw(`</div></section></body></html>`)
}

func writeEPUBChapter(out *output, title string, paragraphs []string) {
	out.raw(epubDocumentStart)
	out.text(title)
	out.raw(epubDocumentHead + `<body epub:type="bodymatter"><section epub:type="chapter" role="doc-chapter"><h1>`)
	out.text(title)
	out.raw(`</h1>`)
	for _, paragraph := range paragraphs {
		out.raw(`<p>`)
		out.text(paragraph)
		out.raw(`</p>`)
	}
	out.raw(`</section></body></html>`)
}
