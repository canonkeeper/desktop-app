package library

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

// epubFile is one entry of a test EPUB, in the order it is written.
type epubFile struct{ name, content string }

// buildBookEPUB writes the entries of a test book as an EPUB archive.
func buildBookEPUB(t *testing.T, files ...epubFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range files {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// bookOPF is the package of a test book: its documents in the order of the spine, with a navigation document and an
// NCX when named, and what else the package says (a guide).
func bookOPF(nav, ncx bool, extra string, documents ...string) string {
	var manifest, spine strings.Builder
	spineAttribute := ""
	if nav {
		manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	}
	if ncx {
		manifest.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
		spineAttribute = ` toc="ncx"`
	}
	for _, document := range documents {
		id := strings.TrimSuffix(document, ".xhtml")
		manifest.WriteString(`<item id="` + id + `" href="` + document + `" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="` + id + `"/>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata><dc:title>Туманный берег</dc:title></metadata>
  <manifest>` + manifest.String() + `</manifest>
  <spine` + spineAttribute + `>` + spine.String() + `</spine>` + extra + `
</package>`
}

func bookPage(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Страница</title></head><body>` + body + `</body></html>`
}

func parseBook(t *testing.T, files ...epubFile) BookImport {
	t.Helper()
	epub := buildBookEPUB(t, append([]epubFile{{"META-INF/container.xml", containerXML("OPS/content.opf")}}, files...)...)
	book, err := ParseEPUB(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatal(err)
	}
	return book
}

func chapterTitles(book BookImport) []string {
	titles := make([]string, len(book.Chapters))
	for index, chapter := range book.Chapters {
		titles[index] = chapter.Title
	}
	return titles
}

const (
	navOfTheBook = `<nav epub:type="toc"><ol>
  <li><a href="title.xhtml">Заглавие</a></li>
  <li><a href="c1.xhtml">Глава I</a></li>
  <li><a href="c2.xhtml#start">Глава <i>II</i></a><ol><li><a href="c2.xhtml#scene">Письмо</a></li></ol></li>
  <li><a href="c3.xhtml">Глава III</a></li>
  <li><a href="about.xhtml">Информация</a></li>
</ol></nav>
<nav epub:type="landmarks"><ol>
  <li><a epub:type="bodymatter" href="c1.xhtml">Начало</a></li>
  <li><a epub:type="copyright-page" href="about.xhtml">Информация</a></li>
</ol></nav>`
	aboutThisEdition = `<h1>Об этом электронном издании</h1><p>Эта книга собрана добровольцами библиотеки.</p><p>Книги библиотеки доступны бесплатно.</p>`
)

// TestParseEPUBNamesChaptersByTheContents: a chapter whose document has no heading of its own is named by the contents
// of the file, so one book names its chapters in one way; a heading in the text is stronger than the contents
// (specs/2026-11-10-ux1-names.md N7.1; J-13: «Глава I…V», then «Глава 6…17»).
func TestParseEPUBNamesChaptersByTheContents(t *testing.T) {
	documents := []epubFile{
		{"OPS/title.xhtml", bookPage(`<h2>Туманный берег</h2><h3>автор Вымышленный Автор</h3>`)},
		{"OPS/c1.xhtml", bookPage(`<h2>Глава первая. Брод</h2><p>Мара вышла к броду на рассвете.</p>`)},
		{"OPS/c2.xhtml", bookPage(`<div><p id="start">Туман стоял до полудня.</p><p id="scene">Сава принёс письмо.</p></div>`)},
		{"OPS/c3.xhtml", bookPage(`<p>К вечеру они дошли до пристани.</p>`)},
		{"OPS/about.xhtml", bookPage(aboutThisEdition)},
	}
	spine := []string{"title.xhtml", "c1.xhtml", "c2.xhtml", "c3.xhtml", "about.xhtml"}

	t.Run("navigation document of EPUB 3", func(t *testing.T) {
		book := parseBook(t, append([]epubFile{
			{"OPS/content.opf", bookOPF(true, false, "", spine...)},
			{"OPS/nav.xhtml", bookPage(navOfTheBook)},
		}, documents...)...)
		if got, want := chapterTitles(book), []string{"Глава первая. Брод", "Глава II", "Глава III"}; strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("chapters = %q, want %q", got, want)
		}
		if got := strings.Join(textsOf(book.Chapters[1].Paragraphs), "|"); got != "Туман стоял до полудня.|Сава принёс письмо." {
			t.Fatalf("chapter II = %q", got)
		}
	})

	t.Run("NCX of EPUB 2", func(t *testing.T) {
		ncx := `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
  <navPoint id="t" playOrder="1"><navLabel><text>Заглавие</text></navLabel><content src="title.xhtml"/></navPoint>
  <navPoint id="a" playOrder="2"><navLabel><text>Глава I</text></navLabel><content src="c1.xhtml"/></navPoint>
  <navPoint id="b" playOrder="3"><navLabel><text> Глава
     II </text></navLabel><content src="c2.xhtml#start"/>
    <navPoint id="b1" playOrder="4"><navLabel><text>Письмо</text></navLabel><content src="c2.xhtml#scene"/></navPoint>
  </navPoint>
  <navPoint id="c" playOrder="5"><navLabel><text>Глава III</text></navLabel><content src="c3.xhtml"/></navPoint>
</navMap></ncx>`
		guide := `<guide><reference type="text" href="c1.xhtml" title="Начало"/><reference type="copyright-page" href="about.xhtml" title="Информация"/></guide>`
		book := parseBook(t, append([]epubFile{
			{"OPS/content.opf", bookOPF(false, true, guide, spine...)},
			{"OPS/toc.ncx", ncx},
		}, documents...)...)
		if got, want := chapterTitles(book), []string{"Глава первая. Брод", "Глава II", "Глава III"}; strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("chapters = %q, want %q", got, want)
		}
	})

	t.Run("no contents, a broken one or a name too long", func(t *testing.T) {
		long := strings.Repeat("Ч", maxImportedChapterTitleLength+1)
		for name, files := range map[string][]epubFile{
			"no contents": {{"OPS/content.opf", bookOPF(false, false, "", spine...)}},
			"broken":      {{"OPS/content.opf", bookOPF(true, false, "", spine...)}, {"OPS/nav.xhtml", `<html><body><nav epub:type="toc"><ol><li><a href="c2.xhtml">Глава II`}},
			"missing":     {{"OPS/content.opf", bookOPF(true, true, "", spine...)}},
			"too long":    {{"OPS/content.opf", bookOPF(true, false, "", spine...)}, {"OPS/nav.xhtml", bookPage(`<nav epub:type="toc"><ol><li><a href="c2.xhtml">` + long + `</a></li><li><a href="../../c3.xhtml">Вне книги</a></li></ol></nav>`)}},
		} {
			book := parseBook(t, append(files, documents...)...)
			if got, want := chapterTitles(book), []string{"Глава первая. Брод", "Глава 2", "Глава 3", "Об этом электронном издании"}; strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("%s: chapters = %q, want %q", name, got, want)
			}
		}
	})
}

// TestParseEPUBLeavesOutThePagesOfThePublisher: a document the file itself calls a service page — in its landmarks or
// its guide — is no chapter of the book, unless the file calls the same document the text of the book too; the
// navigation document in the spine is none either (N7.3; J-13: «Об этом электронном издании» was chapter 18).
func TestParseEPUBLeavesOutThePagesOfThePublisher(t *testing.T) {
	chapter := epubFile{"OPS/c1.xhtml", bookPage(`<h2>Глава I</h2><p>Мара вышла к броду на рассвете.</p>`)}
	about := epubFile{"OPS/about.xhtml", bookPage(aboutThisEdition)}
	nav := func(landmarks string) epubFile {
		return epubFile{"OPS/nav.xhtml", bookPage(`<h1>Содержание</h1><p>Оглавление книги.</p><nav epub:type="toc"><ol><li><a href="c1.xhtml">Глава I</a></li></ol></nav><nav epub:type="landmarks"><ol>` + landmarks + `</ol></nav>`)}
	}
	for name, test := range map[string]struct {
		files []epubFile
		want  string
	}{
		"landmarks": {[]epubFile{
			{"OPS/content.opf", bookOPF(true, false, "", "nav.xhtml", "c1.xhtml", "about.xhtml")},
			nav(`<li><a epub:type="copyright-page" href="about.xhtml">Информация</a></li>`), chapter, about,
		}, "Глава I"},
		"guide": {[]epubFile{
			{"OPS/content.opf", bookOPF(false, false, `<guide><reference type="colophon" href="about.xhtml#top"/></guide>`, "c1.xhtml", "about.xhtml")}, chapter, about,
		}, "Глава I"},
		"also the text of the book": {[]epubFile{
			{"OPS/content.opf", bookOPF(true, false, "", "c1.xhtml", "about.xhtml")},
			nav(`<li><a epub:type="copyright-page" href="about.xhtml">Информация</a></li><li><a epub:type="bodymatter" href="about.xhtml">Текст</a></li>`), chapter, about,
		}, "Глава I|Об этом электронном издании"},
		"called nothing": {[]epubFile{
			{"OPS/content.opf", bookOPF(false, false, "", "c1.xhtml", "about.xhtml")}, chapter, about,
		}, "Глава I|Об этом электронном издании"},
	} {
		if got := strings.Join(chapterTitles(parseBook(t, test.files...)), "|"); got != test.want {
			t.Fatalf("%s: chapters = %q, want %q", name, got, test.want)
		}
	}
}
