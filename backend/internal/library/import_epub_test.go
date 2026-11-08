package library

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseEPUB(t *testing.T) {
	epub := buildSimpleEPUB(t)

	book, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatal(err)
	}

	if book.Title != "Fixture EPUB" {
		t.Fatalf("bad title %q", book.Title)
	}
	if len(book.Chapters) != 1 {
		t.Fatalf("bad chapter count %#v", book.Chapters)
	}
	chapter := book.Chapters[0]
	if chapter.Title != "Chapter One" {
		t.Fatalf("bad chapter title %q", chapter.Title)
	}
	if got := chapter.Paragraphs; len(got) != 2 || got[0] != "First paragraph." || got[1] != "Second paragraph." {
		t.Fatalf("bad paragraphs %#v", got)
	}
}

// A whole volume in one document (Wikisource export) still becomes one chapter per chapter
// heading, while headings inside a chapter (letters, signs) stay its text.
func TestParseEPUBSplitsDocumentAtChapterHeadings(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf":      minimalOPF("chapter1.xhtml"),
		"OEBPS/chapter1.xhtml": `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
  <p>Посвящение.</p>
  <h1>Книга</h1>
  <h2>Часть первая.</h2>
  <h3>Глава I. Поездка</h3>
  <p>Первый.</p>
  <div role="heading" class="heading">Письмо,</div>
  <p>Второй.</p>
  <h3>Глава II. Гроза</h3>
  <p>Третий.</p>
  <h4>Вывеска</h4>
  <p>Четвёртый.</p>
  <div role="heading" aria-level="3">Глава III.</div>
  <p>Пятый.</p>
  <h3>Пустой заголовок в конце</h3>
</body></html>`,
	})

	book, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatal(err)
	}
	want := []ChapterImport{
		{Title: "Книга. Часть первая. Глава I. Поездка", Paragraphs: []string{"Посвящение.", "Первый.", "Письмо,", "Второй."}},
		{Title: "Глава II. Гроза", Paragraphs: []string{"Третий.", "Вывеска", "Четвёртый."}},
		{Title: "Глава III.", Paragraphs: []string{"Пятый."}},
	}
	if len(book.Chapters) != len(want) {
		t.Fatalf("chapters = %#v, want %#v", book.Chapters, want)
	}
	for index, chapter := range book.Chapters {
		if chapter.Title != want[index].Title || strings.Join(chapter.Paragraphs, "|") != strings.Join(want[index].Paragraphs, "|") {
			t.Fatalf("chapter %d = %#v, want %#v", index, chapter, want[index])
		}
	}
}

func TestJoinEPUBHeadingsKeepsTitlesWithinLimit(t *testing.T) {
	long := strings.Repeat("Ч", maxImportedChapterTitleLength)
	if got := joinEPUBHeadings(long, "Глава I"); got != "Глава I" {
		t.Fatalf("joined title = %q, want the chapter heading alone", got)
	}
	if got := joinEPUBHeadings("Часть первая", "Глава I"); got != "Часть первая. Глава I" {
		t.Fatalf("joined title = %q", got)
	}
}

func TestParseEPUBFixtureMatchesGeneratedEPUB(t *testing.T) {
	want := buildSimpleEPUB(t)
	got, err := os.ReadFile("testdata/simple.epub")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("simple.epub fixture does not match generated EPUB bytes")
	}
}

func TestParseEPUBRejectsEmptyContent(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata><dc:title>Empty EPUB</dc:title></metadata>
  <manifest><item id="chap1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="chap1"/></spine>
</package>`,
		"OEBPS/chapter1.xhtml": `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Only title</h1></body></html>`,
	})

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if !errors.Is(err, ErrImportNoContent) {
		t.Fatalf("expected no content, got %v", err)
	}
}

func TestParseEPUBRejectsZipSlipEntry(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf":      minimalOPF("chapter1.xhtml"),
		"OEBPS/chapter1.xhtml":   minimalChapterXHTML(),
		"../evil.xhtml":          "evil",
	})

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err == nil {
		t.Fatal("expected zip slip entry to be rejected")
	}
}

func TestParseEPUBRejectsUnsafeContainerRootfile(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="../evil.opf" media-type="application/oebps-package+xml"/>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"OEBPS/content.opf":    minimalOPF("chapter1.xhtml"),
		"OEBPS/chapter1.xhtml": minimalChapterXHTML(),
	})

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err == nil {
		t.Fatal("expected unsafe container rootfile to be rejected")
	}
}

func TestParseEPUBRejectsUnsafeManifestHref(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf":      minimalOPF("../chapter1.xhtml"),
		"OEBPS/chapter1.xhtml":   minimalChapterXHTML(),
	})

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err == nil {
		t.Fatal("expected unsafe manifest href to be rejected")
	}
}

func TestParseEPUBRejectsOversizedEntry(t *testing.T) {
	oversizedTitle := strings.Repeat("A", int(epubMaxEntryBytes)+1)
	epub := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>` + oversizedTitle + `</dc:title>
  </metadata>
  <manifest>
    <item id="chap1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chap1"/>
  </spine>
</package>`,
		"OEBPS/chapter1.xhtml": minimalChapterXHTML(),
	})

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err == nil {
		t.Fatal("expected oversized EPUB entry to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds EPUB entry limit") {
		t.Fatalf("expected oversized entry error, got %v", err)
	}
}

func buildSimpleEPUB(t *testing.T) []byte {
	t.Helper()

	return buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML("OEBPS/content.opf"),
		"OEBPS/content.opf":      minimalOPF("chapter1.xhtml"),
		"OEBPS/chapter1.xhtml":   minimalChapterXHTML(),
	})
}

func buildEPUB(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/chapter1.xhtml",
		"../evil.xhtml",
	}
	for _, name := range names {
		content, ok := files[name]
		if !ok {
			continue
		}
		header := &zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		}
		header.SetModTime(time.Unix(0, 0).UTC())
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func containerXML(opfPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="` + opfPath + `" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
}

func minimalOPF(chapterHref string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>Fixture EPUB</dc:title>
  </metadata>
  <manifest>
    <item id="chap1" href="` + chapterHref + `" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chap1"/>
  </spine>
</package>`
}

func minimalChapterXHTML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <body>
    <h1>Chapter <span>One</span></h1>
    <p>First <em>paragraph</em>.</p>
    <p>Second paragraph.</p>
  </body>
</html>`
}

// TestParseEPUBStopsAtTheChapterLimit (R2.2): the parser stops at chapter 2001 and does not read
// the next spine document (here a missing one, which would fail the import otherwise).
func TestParseEPUBStopsAtTheChapterLimit(t *testing.T) {
	var chapters strings.Builder
	for number := 1; number <= maxImportedChapters+1; number++ {
		fmt.Fprintf(&chapters, "<h1>Глава %d</h1><p>Текст.</p>", number)
	}
	epub := epubWithSpineForTest(t, []string{chapters.String()}, 1)

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("ParseEPUB() error = %v, want ErrImportTooManyChapters", err)
	}
}

// TestParseEPUBStopsAtTheTextLimit (R2.2): three documents of 4 000 000 characters exceed
// 10 000 000 in the third, and the parser does not read the next spine document.
func TestParseEPUBStopsAtTheTextLimit(t *testing.T) {
	text := "<p>" + strings.Repeat("a", 4_000_000) + "</p>"
	epub := epubWithSpineForTest(t, []string{text, text, text}, 1)

	_, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if !errors.Is(err, ErrImportTooLarge) {
		t.Fatalf("ParseEPUB() error = %v, want ErrImportTooLarge", err)
	}
}

// epubWithSpineForTest builds an EPUB whose spine lists the given XHTML bodies and then `missing`
// documents absent from the archive.
func epubWithSpineForTest(t *testing.T, bodies []string, missing int) []byte {
	t.Helper()
	var manifest, spine strings.Builder
	entries := []docxEntryForTest{{name: "META-INF/container.xml", body: containerXML("OEBPS/content.opf")}}
	for index := range len(bodies) + missing {
		fmt.Fprintf(&manifest, `<item id="c%d" href="c%d.xhtml" media-type="application/xhtml+xml"/>`, index, index)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, index)
		if index < len(bodies) {
			entries = append(entries, docxEntryForTest{
				name: fmt.Sprintf("OEBPS/c%d.xhtml", index),
				body: `<html xmlns="http://www.w3.org/1999/xhtml"><body>` + bodies[index] + `</body></html>`,
			})
		}
	}
	entries = append(entries, docxEntryForTest{name: "OEBPS/content.opf", body: `<package version="3.0"><metadata/>` +
		`<manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`})
	return buildDOCXForTest(t, entries...)
}
