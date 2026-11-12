package bookexport_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"image/png"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	"canonkeeper/backend/internal/domain/textface"
	"canonkeeper/backend/internal/library"
)

var when = time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC)

type chapter struct {
	title      string
	paragraphs []string
}

func testBook(cover bool, chapters ...chapter) bookexport.Book {
	book := bookexport.Book{
		ID: "75c4aac5-0000-4000-8000-000000000001", Title: `Том «Белый порт» & другие`, Author: "Игорь Вельский", Date: when,
		Text: func(index int) ([]string, error) { return chapters[index].paragraphs, nil },
	}
	for _, chapter := range chapters {
		book.Chapters = append(book.Chapters, chapter.title)
	}
	if cover {
		book.Cover = testCover()
	}
	return book
}

func testCover() *bookexport.Image {
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 60, 90))); err != nil {
		panic(err)
	}
	return &bookexport.Image{MediaType: "image/png", Content: content.Bytes(), Width: 600, Height: 900}
}

var smallBook = []chapter{
	{`Глава 1. «Кир» & компания`, []string{`"Стой!" — крикнул он: «Кир & Мила <вместе>».`, "", `Второй абзац с 'апострофом' и a<b>c.`}},
	{"Пролог", []string{"Первый.", "   ", "Второй, последний."}},
	{"Эпилог", []string{"Одна строка."}},
}

func written(t *testing.T, format bookexport.Format, book bookexport.Book) []byte {
	t.Helper()
	var file bytes.Buffer
	if err := bookexport.Write(&file, format, book); err != nil {
		t.Fatalf("write %s: %v", format, err)
	}
	return file.Bytes()
}

func readBack(t *testing.T, format bookexport.Format, file []byte) library.BookImport {
	t.Helper()
	var book library.BookImport
	var err error
	switch format {
	case bookexport.FormatDOCX:
		book, err = library.ParseDOCX(bytes.NewReader(file), int64(len(file)))
	case bookexport.FormatEPUB:
		book, err = library.ParseEPUB(bytes.NewReader(file), int64(len(file)))
	case bookexport.FormatFB2:
		book, err = library.ParseFB2(bytes.NewReader(file))
	}
	if err != nil {
		t.Fatalf("read %s back: %v", format, err)
	}
	return book
}

var allFormats = []bookexport.Format{bookexport.FormatDOCX, bookexport.FormatEPUB, bookexport.FormatFB2}

// chaptersOf is what the book keeps of the chapters read back: the title and the markdown of each paragraph.
func chaptersOf(book library.BookImport) []chapter {
	chapters := make([]chapter, len(book.Chapters))
	for i, read := range book.Chapters {
		chapters[i].title = read.Title
		for _, paragraph := range read.Paragraphs {
			chapters[i].paragraphs = append(chapters[i].paragraphs, textface.Markdown(paragraph.Runs))
		}
	}
	return chapters
}

// specs/2026-11-08-italic-bold-in-text.md TF7.1: the italic and bold of the book are written in every format and come
// back through the import of the product as the same markdown, a star and a backslash of the text with them.
func TestTheFacesOfABookComeBackWordForWord(t *testing.T) {
	faced := []chapter{
		{"Глава 1", []string{"Он *пришёл* и **сел**, ***оба*** и *курсив **с жирным***.", `Звезда \* и обратная \\ черта, *5\*3*.`, "Без начертания."}},
		{"Глава 2", []string{"**Жирный абзац целиком.**", "*― Тише,* ― сказал он."}},
	}
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			if got := chaptersOf(readBack(t, format, written(t, format, testBook(false, faced...)))); !reflect.DeepEqual(got, faced) {
				t.Fatalf("chapters = %#v\nwant %#v", got, faced)
			}
		})
	}
}

// E1.1, E1.4: what is written comes back through the import of the product: the title, the chapters in their order,
// the paragraphs word for word; a paragraph without text is not written, and the title page is not a chapter.
func TestAWrittenBookComesBackWordForWord(t *testing.T) {
	for _, format := range allFormats {
		for _, cover := range []bool{true, false} {
			t.Run(string(format), func(t *testing.T) {
				book := readBack(t, format, written(t, format, testBook(cover, smallBook...)))
				if book.Title != `Том «Белый порт» & другие` {
					t.Fatalf("title = %q", book.Title)
				}
				want := []chapter{
					{`Глава 1. «Кир» & компания`, []string{`"Стой!" — крикнул он: «Кир & Мила <вместе>».`, `Второй абзац с 'апострофом' и a<b>c.`}},
					{"Пролог", []string{"Первый.", "Второй, последний."}},
					{"Эпилог", []string{"Одна строка."}},
				}
				if got := chaptersOf(book); !reflect.DeepEqual(got, want) {
					t.Fatalf("chapters = %#v", got)
				}
			})
		}
	}
}

// A book of one chapter stays one chapter, and a title too long for a line is still a title.
func TestABookOfOneChapterWithALongTitleComesBack(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			source := testBook(false, chapter{"Начало", []string{"Единственный абзац."}})
			source.Title = strings.Repeat("Очень длинное название ", 7) + "конец"
			book := readBack(t, format, written(t, format, source))
			want := []chapter{{"Начало", []string{"Единственный абзац."}}}
			if got := chaptersOf(book); !reflect.DeepEqual(got, want) || book.Title != source.Title {
				t.Fatalf("title = %q, chapters = %#v", book.Title, got)
			}
		})
	}
}

// A sign XML has no place for does not make the whole file unreadable.
func TestAStraySignDoesNotBreakAFile(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			book := readBack(t, format, written(t, format, testBook(false,
				chapter{"Глава\x01 1", []string{"Текст\x0bс\x00знаками."}}, chapter{"Глава 2", []string{"Дальше."}})))
			if len(book.Chapters) != 2 || book.Chapters[1].Paragraphs[0].Text() != "Дальше." {
				t.Fatalf("chapters = %#v", book.Chapters)
			}
		})
	}
}

func parts(t *testing.T, file []byte) (map[string]string, []string) {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	contents, names := map[string]string{}, []string{}
	for _, part := range archive.File {
		reader, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		contents[part.Name], names = string(content), append(names, part.Name)
	}
	return contents, names
}

func wellFormed(t *testing.T, name, content string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(content))
	decoder.Strict = true
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatalf("%s is not well-formed: %v", name, err)
		}
	}
}

func contains(t *testing.T, name, content string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("%s lacks %q:\n%.2000s", name, want, content)
		}
	}
}

// E1.2: the file says nothing of the product.
func noServiceMarks(t *testing.T, name, content string) {
	t.Helper()
	for _, mark := range []string{"canonkeeper", "CanonKeeper", "generator", "program-used"} {
		if strings.Contains(content, mark) {
			t.Fatalf("%s carries a service mark %q", name, mark)
		}
	}
}

// E1.2, E1.4: Word shows chapters in its navigation, and a screen reader hears the language and the cover.
func TestADOCXHasHeadingsMetadataAndACover(t *testing.T) {
	contents, names := parts(t, written(t, bookexport.FormatDOCX, testBook(true, smallBook...)))
	for _, name := range names {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			wellFormed(t, name, contents[name])
		}
		noServiceMarks(t, name, contents[name])
	}
	if names[0] != "[Content_Types].xml" {
		t.Fatalf("first part = %q", names[0])
	}
	document := contents["word/document.xml"]
	if count := strings.Count(document, `<w:pStyle w:val="Heading1"/>`); count != 3 {
		t.Fatalf("%d headings", count)
	}
	contains(t, "document", document, `<w:pStyle w:val="Title"/>`, `descr="Обложка книги «Том «Белый порт» &amp; другие»"`, `r:embed="rIdCover"`)
	contains(t, "styles", contents["word/styles.xml"], `<w:name w:val="heading 1"/>`, `<w:outlineLvl w:val="0"/>`, `w:lang w:val="ru-RU"`)
	contains(t, "core", contents["docProps/core.xml"], `<dc:title>Том «Белый порт» &amp; другие</dc:title>`, `<dc:creator>Игорь Вельский</dc:creator>`,
		`<dc:language>ru-RU</dc:language>`, `2026-11-05T12:30:00Z`)
	contains(t, "types", contents["[Content_Types].xml"], `Extension="png"`)
	if contents["word/media/cover.png"] != string(testCover().Content) {
		t.Fatal("the cover is not in the file")
	}
	withoutCover, _ := parts(t, written(t, bookexport.FormatDOCX, testBook(false, smallBook...)))
	if strings.Contains(withoutCover["word/document.xml"], "w:drawing") || withoutCover["word/media/cover.png"] != "" {
		t.Fatal("a book without a cover has a picture")
	}
}

// E1.4: EPUB 3: the mimetype first and stored, the contents in nav and in NCX, the chapters as headings of level 1.
func TestAnEPUBIsAProperEPUB3(t *testing.T) {
	file := written(t, bookexport.FormatEPUB, testBook(true, smallBook...))
	if !bytes.HasPrefix(file, []byte("PK\x03\x04")) || !bytes.Equal(file[30:38], []byte("mimetype")) || !bytes.Equal(file[38:58], []byte("application/epub+zip")) {
		t.Fatalf("the file starts with %q", file[:64])
	}
	if extra := int(file[28]) | int(file[29])<<8; extra != 0 || file[8] != 0 {
		t.Fatalf("mimetype: extra field %d, method %d", extra, file[8])
	}
	contents, names := parts(t, file)
	for _, name := range names {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".opf") || strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".ncx") {
			wellFormed(t, name, contents[name])
		}
		noServiceMarks(t, name, contents[name])
	}
	contains(t, "opf", contents["OEBPS/content.opf"], `version="3.0"`, `<dc:title>Том «Белый порт» &amp; другие</dc:title>`, `<dc:creator>Игорь Вельский</dc:creator>`,
		`<dc:language>ru</dc:language>`, `<dc:date>2026-11-05</dc:date>`, `<meta property="dcterms:modified">2026-11-05T12:30:00Z</meta>`, `properties="nav"`,
		`properties="cover-image"`, `urn:uuid:75c4aac5-0000-4000-8000-000000000001`, `<itemref idref="chapter-3"/>`)
	contains(t, "nav", contents["OEBPS/nav.xhtml"], `epub:type="toc"`, `lang="ru"`, `<a href="chapter-0002.xhtml">Пролог</a>`)
	contains(t, "ncx", contents["OEBPS/toc.ncx"], `<content src="chapter-0003.xhtml"/>`, `<text>Эпилог</text>`)
	contains(t, "chapter", contents["OEBPS/chapter-0001.xhtml"], `<h1>Глава 1. «Кир» &amp; компания</h1>`, `lang="ru"`)
	contains(t, "cover", contents["OEBPS/cover.xhtml"], `alt="Обложка книги`)
	contains(t, "title", contents["OEBPS/title.xhtml"], `Игорь Вельский`)
	if strings.Contains(contents["OEBPS/content.opf"], `idref="nav"`) {
		t.Fatal("the contents are in the spine: the import would take them for text")
	}
}

type fb2File struct {
	Description struct {
		TitleInfo struct {
			Genre  string `xml:"genre"`
			Author struct {
				First string `xml:"first-name"`
				Last  string `xml:"last-name"`
			} `xml:"author"`
			Title string `xml:"book-title"`
			Cover struct {
				Image struct {
					Href string `xml:"http://www.w3.org/1999/xlink href,attr"`
				} `xml:"image"`
			} `xml:"coverpage"`
			Lang string `xml:"lang"`
		} `xml:"title-info"`
		Document struct {
			Date struct {
				Value string `xml:"value,attr"`
			} `xml:"date"`
			ID      string `xml:"id"`
			Version string `xml:"version"`
			Program string `xml:"program-used"`
		} `xml:"document-info"`
	} `xml:"description"`
	Sections []struct {
		Title      string   `xml:"title>p"`
		Paragraphs []string `xml:"p"`
	} `xml:"body>section"`
	Binary struct {
		ID      string `xml:"id,attr"`
		Type    string `xml:"content-type,attr"`
		Content string `xml:",chardata"`
	} `xml:"binary"`
}

// E1.4: FB2 2.1 is proper XML with a description, a cover in binary and chapters as sections.
func TestAnFB2HasADescriptionACoverAndSections(t *testing.T) {
	raw := written(t, bookexport.FormatFB2, testBook(true, smallBook...))
	wellFormed(t, "fb2", string(raw))
	noServiceMarks(t, "fb2", string(raw))
	var file fb2File
	if err := xml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	info := file.Description.TitleInfo
	if info.Title != `Том «Белый порт» & другие` || info.Author.First != "Игорь" || info.Author.Last != "Вельский" || info.Lang != "ru" || info.Genre == "" {
		t.Fatalf("title-info = %+v", info)
	}
	document := file.Description.Document
	if document.Date.Value != "2026-11-05" || document.ID == "" || document.Version == "" {
		t.Fatalf("document-info = %+v", document)
	}
	if len(file.Sections) != 3 || file.Sections[1].Title != "Пролог" || len(file.Sections[1].Paragraphs) != 2 {
		t.Fatalf("sections = %+v", file.Sections)
	}
	if info.Cover.Image.Href != "#"+file.Binary.ID || file.Binary.Type != "image/png" {
		t.Fatalf("cover = %+v, binary = %s %s", info.Cover, file.Binary.ID, file.Binary.Type)
	}
	cover, err := base64.StdEncoding.DecodeString(file.Binary.Content)
	if err != nil || !bytes.Equal(cover, testCover().Content) {
		t.Fatalf("the cover does not come back: %v", err)
	}
	one := testBook(false, chapter{"Глава", []string{"Текст."}})
	one.Author = "Леч"
	contains(t, "fb2", string(written(t, bookexport.FormatFB2, one)), `<author><nickname>Леч</nickname></author>`)
}

// countingWriter counts what has gone out of a writer.
type countingWriter struct{ total int }

func (writer *countingWriter) Write(data []byte) (int, error) {
	writer.total += len(data)
	return len(data), nil
}

// E1.6: a file goes out while it is written: the bytes of a chapter are gone before the next one is read.
func TestAFileGoesOutChapterByChapter(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			var sink countingWriter
			paragraph := []string{}
			for index := range 4000 {
				paragraph = append(paragraph, strings.Repeat("слово"+string(rune('а'+index%30))+string(rune('а'+index%7))+" ", 40))
			}
			seen := []int{}
			book := bookexport.Book{
				ID: "id", Title: "Книга", Author: "Автор", Date: when, Chapters: []string{"Один", "Два", "Три"},
				Text: func(int) ([]string, error) {
					seen = append(seen, sink.total)
					return paragraph, nil
				},
			}
			if err := bookexport.Write(&sink, format, book); err != nil {
				t.Fatal(err)
			}
			if len(seen) != 3 || seen[2] <= seen[1] || seen[1] <= seen[0] {
				t.Fatalf("written before each chapter: %v", seen)
			}
		})
	}
}

// E1.6: a chapter that cannot be read stops the file: half a book is not a success.
func TestAFailedChapterStopsTheFile(t *testing.T) {
	failure := errors.New("the chapter is gone")
	for _, format := range allFormats {
		book := testBook(false, smallBook...)
		book.Text = func(index int) ([]string, error) {
			if index == 1 {
				return nil, failure
			}
			return []string{"Текст."}, nil
		}
		if err := bookexport.Write(io.Discard, format, book); !errors.Is(err, failure) {
			t.Fatalf("%s: %v", format, err)
		}
	}
}

func TestABookWithoutChaptersHasNoFile(t *testing.T) {
	for _, format := range allFormats {
		if err := bookexport.Write(io.Discard, format, bookexport.Book{Title: "Пусто"}); !errors.Is(err, bookexport.ErrEmptyBook) {
			t.Fatalf("%s: %v", format, err)
		}
	}
}

// E1.3: a name of a file is the name of the book, with the signs file systems do not allow replaced.
func TestAFileIsNamedByItsBook(t *testing.T) {
	for _, test := range []struct {
		title  string
		format bookexport.Format
		want   string
	}{
		{"Белый порт", bookexport.FormatDOCX, "Белый порт.docx"},
		{`Том 1: «Порт» / черновик?`, bookexport.FormatEPUB, "Том 1_ «Порт» _ черновик_.epub"},
		{`a\b*c"d<e>f|g`, bookexport.FormatFB2, "a_b_c_d_e_f_g.fb2"},
		{" Конец... ", bookexport.FormatFB2, "Конец.fb2"},
		{"...", bookexport.FormatDOCX, "Книга.docx"},
		{"строка\nвторая\x7f", bookexport.FormatDOCX, "строка_вторая_.docx"},
		{"CON", bookexport.FormatDOCX, "_CON.docx"},
		{"com1.том", bookexport.FormatDOCX, "_com1.том.docx"},
		{"COMET", bookexport.FormatDOCX, "COMET.docx"},
		{strings.Repeat("ё", 200), bookexport.FormatDOCX, strings.Repeat("ё", 120) + ".docx"},
	} {
		if got := bookexport.FileName(test.title, test.format); got != test.want {
			t.Errorf("FileName(%q) = %q, want %q", test.title, got, test.want)
		}
	}
}

func TestAFormatIsOneOfThree(t *testing.T) {
	for _, value := range []string{"docx", "epub", "fb2"} {
		if format, err := bookexport.ParseFormat(value); err != nil || string(format) != value {
			t.Fatalf("%s: %v", value, err)
		}
	}
	for _, value := range []string{"", "pdf", "DOCX", "../etc"} {
		if _, err := bookexport.ParseFormat(value); !errors.Is(err, bookexport.ErrUnknownFormat) {
			t.Fatalf("%q: %v", value, err)
		}
	}
}
