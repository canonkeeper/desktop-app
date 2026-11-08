package bookexport

import (
	"io"
	"strconv"
)

const xmlDeclaration = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

const docxRelationships = xmlDeclaration + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

// A chapter is a level-1 heading: Word and LibreOffice show it in their navigation, a screen reader jumps by it.
// The author's line has a style of its own, so the import of this very file does not take it for the text of the book.
const docxStyles = xmlDeclaration + `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="Times New Roman" w:cs="Times New Roman"/>` +
	`<w:sz w:val="28"/><w:szCs w:val="28"/><w:lang w:val="ru-RU" w:eastAsia="ru-RU"/></w:rPr></w:rPrDefault>` +
	`<w:pPrDefault><w:pPr><w:spacing w:after="0" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:ind w:firstLine="709"/><w:jc w:val="both"/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:pageBreakBefore/><w:spacing w:after="480"/><w:ind w:firstLine="0"/><w:jc w:val="center"/><w:outlineLvl w:val="0"/></w:pPr>` +
	`<w:rPr><w:b/><w:bCs/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="BookAuthor"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:before="2400" w:after="480"/><w:ind w:firstLine="0"/><w:jc w:val="center"/></w:pPr>` +
	`<w:rPr><w:b/><w:bCs/><w:sz w:val="56"/><w:szCs w:val="56"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="BookAuthor"><w:name w:val="Book Author"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:ind w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:rPr><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>` +
	`</w:styles>`

// A4 with margins of 2.5 cm, in twentieths of a point.
const docxSection = `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
	`<w:pgMar w:top="1418" w:right="1418" w:bottom="1418" w:left="1418" w:header="709" w:footer="709" w:gutter="0"/></w:sectPr>`

func writeDOCX(writer io.Writer, book Book) error {
	pack := newPack(writer, book.Date)
	if err := pack.part("[Content_Types].xml", func(out *output) error {
		out.raw(xmlDeclaration + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
		if book.Cover != nil {
			out.raw(`<Default Extension="jpg" ContentType="image/jpeg"/><Default Extension="png" ContentType="image/png"/>`)
		}
		out.raw(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`)
		return nil
	}); err != nil {
		return err
	}
	if err := pack.markup("_rels/.rels", docxRelationships); err != nil {
		return err
	}
	if err := pack.part("docProps/core.xml", func(out *output) error {
		date := book.Date.UTC().Format("2006-01-02T15:04:05Z")
		out.raw(xmlDeclaration + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
			`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>`)
		out.text(book.Title)
		out.raw(`</dc:title><dc:creator>`)
		out.text(book.Author)
		out.raw(`</dc:creator><dc:language>ru-RU</dc:language><dcterms:created xsi:type="dcterms:W3CDTF">` + date +
			`</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + date + `</dcterms:modified></cp:coreProperties>`)
		return nil
	}); err != nil {
		return err
	}
	if err := pack.markup("word/styles.xml", docxStyles); err != nil {
		return err
	}
	if err := pack.part("word/_rels/document.xml.rels", func(out *output) error {
		out.raw(xmlDeclaration + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
		if book.Cover != nil {
			out.raw(`<Relationship Id="rIdCover" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/` + book.Cover.fileName() + `"/>`)
		}
		out.raw(`</Relationships>`)
		return nil
	}); err != nil {
		return err
	}
	if book.Cover != nil {
		if err := pack.stored("word/media/"+book.Cover.fileName(), book.Cover.Content); err != nil {
			return err
		}
	}
	if err := pack.part("word/document.xml", func(out *output) error { return writeDOCXBody(out, book) }); err != nil {
		return err
	}
	return pack.zip.Close()
}

func writeDOCXBody(out *output, book Book) error {
	out.raw(xmlDeclaration + `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><w:body>`)
	titleProperties := `<w:pStyle w:val="Title"/>`
	if book.Cover != nil {
		writeDOCXCover(out, book)
		titleProperties += `<w:pageBreakBefore/>`
	}
	writeDOCXParagraph(out, titleProperties, book.Title)
	writeDOCXParagraph(out, `<w:pStyle w:val="BookAuthor"/>`, book.Author)
	for chapter, title := range book.Chapters {
		writeDOCXParagraph(out, `<w:pStyle w:val="Heading1"/>`, title)
		paragraphs, err := book.Text(chapter)
		if err != nil {
			return err
		}
		for _, paragraph := range paragraphs {
			writeDOCXParagraph(out, "", paragraph)
		}
	}
	out.raw(docxSection + `</w:body></w:document>`)
	return nil
}

func writeDOCXParagraph(out *output, properties, text string) {
	out.raw(`<w:p>`)
	if properties != "" {
		out.raw(`<w:pPr>` + properties + `</w:pPr>`)
	}
	out.raw(`<w:r><w:t xml:space="preserve">`)
	out.text(text)
	out.raw(`</w:t></w:r></w:p>`)
}

// writeDOCXCover puts the cover alone on the first page, with a description a screen reader says.
func writeDOCXCover(out *output, book Book) {
	width, height := docxCoverExtent(*book.Cover)
	extent := `cx="` + strconv.FormatInt(width, 10) + `" cy="` + strconv.FormatInt(height, 10) + `"`
	out.raw(`<w:p><w:pPr><w:ind w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:r><w:drawing>` +
		`<wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent ` + extent + `/><wp:docPr id="1" name="cover" descr="`)
	out.text(coverDescription(book.Title))
	out.raw(`"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<pic:nvPicPr><pic:cNvPr id="1" name="` + book.Cover.fileName() + `"/><pic:cNvPicPr/></pic:nvPicPr>` +
		`<pic:blipFill><a:blip r:embed="rIdCover"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>` +
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext ` + extent + `/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>` +
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`)
}

// The text area of the page in the units of Word (914400 to an inch), a little lower than it is: the line of the
// picture has its own spacing, and a cover one step too tall would push itself onto a second page.
const (
	docxCoverMaxWidth  = 5_759_450
	docxCoverMaxHeight = 7_920_000
)

// docxCoverExtent fits a cover into the text area of a page and keeps its shape.
func docxCoverExtent(cover Cover) (width, height int64) {
	width, height = docxCoverMaxWidth, docxCoverMaxWidth*int64(cover.Height)/int64(cover.Width)
	if height > docxCoverMaxHeight {
		width, height = docxCoverMaxHeight*int64(cover.Width)/int64(cover.Height), docxCoverMaxHeight
	}
	return width, height
}
