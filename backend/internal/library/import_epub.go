package library

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
)

const (
	epubMaxEntryBytes     uint64 = 8 * 1024 * 1024
	epubMaxAggregateBytes uint64 = 50 * 1024 * 1024
)

func ParseEPUB(readerAt io.ReaderAt, size int64) (BookImport, error) {
	archive, err := zip.NewReader(readerAt, size)
	if err != nil {
		return BookImport{}, err
	}

	files, err := indexEPUBFiles(archive)
	if err != nil {
		return BookImport{}, err
	}

	readBudget := epubReadBudget{remaining: epubMaxAggregateBytes}
	containerData, err := readEPUBFile(files, "META-INF/container.xml", &readBudget)
	if err != nil {
		return BookImport{}, err
	}
	opfPath, err := parseEPUBContainer(containerData)
	if err != nil {
		return BookImport{}, err
	}

	opfData, err := readEPUBFile(files, opfPath, &readBudget)
	if err != nil {
		return BookImport{}, err
	}
	opf, err := parseEPUBOPF(opfData)
	if err != nil {
		return BookImport{}, err
	}

	book := BookImport{Title: normalizeImportText(string(firstEPUBText(opf.Metadata.Titles)))}
	var limits importLimits
	manifest := make(map[string]epubManifestItem, len(opf.Manifest.Items))
	for _, item := range opf.Manifest.Items {
		if item.ID != "" {
			manifest[item.ID] = item
		}
	}

	// What the file says of itself: the names of its chapters and which of its documents are pages of the publisher
	// (specs/2026-11-10-ux1-names.md N7.1, N7.3).
	contents := readEPUBContents(files, opfPath, opf, &readBudget)
	// The documents of the spine up to the first that cannot be read: its error waits until the documents before it
	// are chapters, so a book over its limits still stops at the limit (R2.2).
	var documents []epubDocument
	var unread error
	for _, itemref := range opf.Spine.ItemRefs {
		item, ok := manifest[itemref.IDRef]
		if !ok {
			unread = fmt.Errorf("library: epub spine item %q missing from manifest", itemref.IDRef)
			break
		}
		documentPath, err := resolveEPUBHref(opfPath, item.Href)
		if err != nil {
			unread = err
			break
		}
		if contents.service[documentPath] {
			continue
		}
		data, err := readEPUBFile(files, documentPath, &readBudget)
		if err != nil {
			unread = err
			break
		}
		documents = append(documents, epubDocument{path: documentPath, data: data})
	}
	// The footnotes of the book stand in its text, each where its mark was (N7.2).
	notes := collectEPUBNotes(documents)
	for _, document := range documents {
		chapters, err := parseEPUBXHTML(document.data, notes.of(document.path), &limits)
		if err != nil {
			return BookImport{}, err
		}
		// A document without a heading of its own is one chapter, named by the contents of the file.
		if len(chapters) == 1 && chapters[0].Title == "" {
			chapters[0].Title = contents.labels[document.path]
		}
		for _, chapter := range chapters {
			if chapter.Title == "" {
				chapter.Title = chapterFallbackTitle(len(book.Chapters) + 1)
			}
			book.Chapters = append(book.Chapters, chapter)
		}
	}
	if unread != nil {
		return BookImport{}, unread
	}

	if len(book.Chapters) == 0 {
		return BookImport{}, ErrImportNoContent
	}
	return book, nil
}

type epubContainer struct {
	Rootfiles []epubRootfile `xml:"rootfiles>rootfile"`
}

type epubRootfile struct {
	FullPath string `xml:"full-path,attr"`
}

type epubOPF struct {
	Metadata epubMetadata         `xml:"metadata"`
	Manifest epubManifest         `xml:"manifest"`
	Spine    epubSpine            `xml:"spine"`
	Guide    []epubGuideReference `xml:"guide>reference"`
}

// epubGuideReference is what EPUB 2 says a document of the book is: its cover, its title page, its text.
type epubGuideReference struct {
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

// epubDocument is a document of the spine with its place in the archive.
type epubDocument struct {
	path string
	data []byte
}

type epubMetadata struct {
	Titles []epubText `xml:"title"`
}

type epubManifest struct {
	Items []epubManifestItem `xml:"item"`
}

type epubManifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type epubSpine struct {
	// Toc is the id of the NCX of EPUB 2.
	Toc      string             `xml:"toc,attr"`
	ItemRefs []epubSpineItemRef `xml:"itemref"`
}

type epubSpineItemRef struct {
	IDRef string `xml:"idref,attr"`
}

type epubReadBudget struct {
	remaining uint64
}

type epubText string

func (text *epubText) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	value, err := readElementText(decoder)
	if err != nil {
		return err
	}
	*text = epubText(value)
	return nil
}

func indexEPUBFiles(archive *zip.Reader) (map[string]*zip.File, error) {
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		name, ok := normalizeEPUBArchivePath(file.Name)
		if !ok {
			return nil, fmt.Errorf("library: epub unsafe archive path %q", file.Name)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		files[name] = file
	}
	return files, nil
}

func parseEPUBContainer(data []byte) (string, error) {
	var container epubContainer
	if err := xml.Unmarshal(data, &container); err != nil {
		return "", err
	}
	for _, rootfile := range container.Rootfiles {
		fullPath := strings.TrimSpace(rootfile.FullPath)
		if fullPath == "" {
			continue
		}
		opfPath, ok := normalizeEPUBArchivePath(fullPath)
		if !ok {
			return "", fmt.Errorf("library: epub unsafe rootfile path %q", fullPath)
		}
		return opfPath, nil
	}
	return "", fmt.Errorf("library: epub container has no safe rootfile")
}

func parseEPUBOPF(data []byte) (epubOPF, error) {
	var opf epubOPF
	if err := xml.Unmarshal(data, &opf); err != nil {
		return epubOPF{}, err
	}
	return opf, nil
}

// epubBlock is one paragraph (level 0) or heading (1–6, or 7 for an ARIA heading without
// a level, which exports use for in-text headings such as letters and signs).
type epubBlock struct {
	text  string
	level int
	// runs are the text in its italic and bold (specs/2026-11-08-italic-bold-in-text.md TF4.1).
	runs []chapterfile.Run
}

// parseEPUBXHTML splits one spine document into chapters. Chapter headings are those of the
// most significant level that is directly followed by text: such a heading after the text of a
// titled chapter starts the next chapter, consecutive headings form one title ("Часть первая.
// Глава I"), text before the first heading belongs to the first chapter, and deeper headings
// stay paragraphs of their chapter. A whole volume exported as one document keeps its chapters.
// The document is read twice — for the chapter level, then for the chapters — so that chapters
// are assembled only up to the book limits. Its footnotes stand in the text where their marks were.
func parseEPUBXHTML(data []byte, notes *epubDocumentNotes, limits *importLimits) ([]ChapterImport, error) {
	chapterLevel, previousLevel := 0, 0
	err := scanEPUBDocument(data, notes, func(block epubBlock) error {
		if block.level == 0 && previousLevel > 0 && (chapterLevel == 0 || previousLevel < chapterLevel) {
			chapterLevel = previousLevel
		}
		previousLevel = block.level
		return nil
	})
	if err != nil {
		return nil, err
	}
	if chapterLevel == 0 {
		chapterLevel = 7
	}
	var chapters []ChapterImport
	var current ChapterImport
	// titledText reports text after the title of the current chapter.
	titledText := false
	err = scanEPUBDocument(data, notes, func(block epubBlock) error {
		switch {
		case block.level == 0 || block.level > chapterLevel:
			if len(current.Paragraphs) == 0 {
				if err := limits.addChapter(); err != nil {
					return err
				}
			}
			if err := limits.addParagraph(block.text); err != nil {
				return err
			}
			current.Paragraphs = append(current.Paragraphs, chapterfile.Paragraph{Runs: block.runs})
			titledText = current.Title != ""
		case titledText:
			chapters = append(chapters, current)
			current = ChapterImport{Title: block.text}
			titledText = false
		default:
			current.Title = joinEPUBHeadings(current.Title, block.text)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(current.Paragraphs) > 0 {
		chapters = append(chapters, current)
	}
	return chapters, nil
}

// joinEPUBHeadings appends a heading to the title; when the joined title would exceed the
// chapter title limit, the latest (most specific) heading alone names the chapter.
func joinEPUBHeadings(title, heading string) string {
	joined := heading
	switch {
	case title == "":
	case strings.ContainsAny(title[len(title)-1:], ".!?…"):
		joined = title + " " + heading
	default:
		joined = title + ". " + heading
	}
	if utf8.RuneCountInString(joined) > maxImportedChapterTitleLength {
		return heading
	}
	return joined
}

func readEPUBFile(files map[string]*zip.File, name string, budget *epubReadBudget) ([]byte, error) {
	file, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("library: epub missing file %q", name)
	}
	if file.UncompressedSize64 > epubMaxEntryBytes {
		return nil, fmt.Errorf("library: epub entry %q exceeds EPUB entry limit", name)
	}
	if file.UncompressedSize64 > budget.remaining {
		return nil, fmt.Errorf("library: epub entry %q exceeds EPUB aggregate limit", name)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	limit := epubMaxEntryBytes
	if budget.remaining < limit {
		limit = budget.remaining
	}
	data, err := readEPUBLimited(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("library: epub entry %q: %w", name, err)
	}
	budget.remaining -= uint64(len(data))
	return data, nil
}

func readEPUBLimited(reader io.Reader, limit uint64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) > limit {
		if limit == epubMaxEntryBytes {
			return nil, fmt.Errorf("exceeds EPUB entry limit")
		}
		return nil, fmt.Errorf("exceeds EPUB aggregate limit")
	}
	return data, nil
}

func resolveEPUBHref(opfPath, href string) (string, error) {
	href, _, _ = strings.Cut(strings.TrimSpace(href), "#")
	if href == "" || strings.Contains(href, "\\") || strings.Contains(href, "\x00") || path.IsAbs(href) {
		return "", fmt.Errorf("library: epub unsafe manifest href %q", href)
	}
	if path.Clean(href) != href || hasParentPathSegment(href) {
		return "", fmt.Errorf("library: epub unsafe manifest href %q", href)
	}

	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}
	resolved := path.Clean(path.Join(opfDir, href))
	if _, ok := normalizeEPUBArchivePath(resolved); !ok {
		return "", fmt.Errorf("library: epub unsafe resolved href %q", href)
	}
	if opfDir != "" && !strings.HasPrefix(resolved, opfDir+"/") {
		return "", fmt.Errorf("library: epub resolved href escapes OPF directory %q", href)
	}
	return resolved, nil
}

func normalizeEPUBArchivePath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "\\") || strings.Contains(name, "\x00") || path.IsAbs(name) {
		return "", false
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" {
		return "", false
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != trimmed {
		return "", false
	}
	return clean, true
}

func hasParentPathSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func firstEPUBText(values []epubText) epubText {
	for _, value := range values {
		if normalizeImportText(string(value)) != "" {
			return value
		}
	}
	return ""
}

func readElementText(decoder *xml.Decoder) (string, error) {
	var builder strings.Builder
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			builder.Write(token)
		}
	}
	return builder.String(), nil
}
