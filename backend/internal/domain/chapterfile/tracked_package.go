package chapterfile

import (
	"archive/zip"
	"io"
	"strings"
	"time"
)

const (
	xmlDeclaration = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	wordNamespace  = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	// A4 with margins of 2.5 cm, in twentieths of a point, as the export of a book writes it.
	trackedSection = `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1418" w:right="1418" w:bottom="1418" w:left="1418" w:header="709" w:footer="709" w:gutter="0"/></w:sectPr>`
)

// The parts of the file around the body: the styles of the export of a book (Times New Roman 14, a first-line indent,
// the chapter heading of the first level) and the settings that open it with revisions shown.
const (
	trackedContentTypes = xmlDeclaration + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`
	trackedRelationships = xmlDeclaration + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
		`</Relationships>`
	trackedDocumentRelationships = xmlDeclaration + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rIdSettings" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>`
	// The theme of the author's file, when it is kept (PR6.7).
	trackedThemeType         = `<Override PartName="/word/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`
	trackedThemeRelationship = `<Relationship Id="rIdTheme" Type="` + themeRelationship + `" Target="theme/theme1.xml"/>`
	trackedStyles            = xmlDeclaration + `<w:styles xmlns:w="` + wordNamespace + `">` +
		`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="Times New Roman" w:cs="Times New Roman"/>` +
		`<w:sz w:val="28"/><w:szCs w:val="28"/><w:lang w:val="ru-RU" w:eastAsia="ru-RU"/></w:rPr></w:rPrDefault>` +
		`<w:pPrDefault><w:pPr><w:spacing w:after="0" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/>` +
		`<w:pPr><w:ind w:firstLine="709"/><w:jc w:val="both"/></w:pPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>` +
		`<w:pPr><w:keepNext/><w:spacing w:after="480"/><w:ind w:firstLine="0"/><w:jc w:val="center"/><w:outlineLvl w:val="0"/></w:pPr>` +
		`<w:rPr><w:b/><w:bCs/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style>` +
		`</w:styles>`
	trackedSettings = xmlDeclaration + `<w:settings xmlns:w="` + wordNamespace + `"><w:trackRevisions/>` +
		`<w:defaultTabStop w:val="709"/><w:characterSpacingControl w:val="doNotCompress"/><w:compat><w:compatSetting w:name="compatibilityMode" ` +
		`w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat></w:settings>`
)

// writeTrackedPackage writes the parts of the file into one archive: in the styles and the theme of the author's file
// when its look is kept (PR6.7), otherwise in those of the export of a book.
func writeTrackedPackage(writer io.Writer, document, title, author string, at time.Time, layout *Layout) error {
	types, relationships, styles := trackedContentTypes, trackedDocumentRelationships, trackedStyles
	parts := []struct{ name, content string }{}
	if layout != nil && layout.Styles != "" {
		styles = layout.Styles
	}
	if layout != nil && layout.Theme != "" {
		types += trackedThemeType
		relationships += trackedThemeRelationship
		parts = append(parts, struct{ name, content string }{"word/theme/theme1.xml", layout.Theme})
	}
	archive := zip.NewWriter(writer)
	date := at.UTC().Truncate(time.Second)
	var core strings.Builder
	core.WriteString(xmlDeclaration + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>`)
	escape(&core, title)
	core.WriteString(`</dc:title><dc:creator>`)
	escape(&core, author)
	stamp := date.Format(time.RFC3339)
	core.WriteString(`</dc:creator><dc:language>ru-RU</dc:language><dcterms:created xsi:type="dcterms:W3CDTF">` + stamp +
		`</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:modified></cp:coreProperties>`)
	for _, part := range append([]struct{ name, content string }{
		{"[Content_Types].xml", types + `</Types>`},
		{"_rels/.rels", trackedRelationships},
		{"docProps/core.xml", core.String()},
		{"word/_rels/document.xml.rels", relationships + `</Relationships>`},
		{"word/styles.xml", styles},
		{"word/settings.xml", trackedSettings},
		{"word/document.xml", document},
	}, parts...) {
		file, err := archive.CreateHeader(&zip.FileHeader{Name: part.name, Method: zip.Deflate, Modified: date})
		if err != nil {
			return err
		}
		if _, err := io.WriteString(file, part.content); err != nil {
			return err
		}
	}
	return archive.Close()
}
