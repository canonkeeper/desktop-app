package library

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

func TestParseFB2(t *testing.T) {
	file, err := os.Open("testdata/simple.fb2")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	book, err := ParseFB2(file)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Fixture Book" || len(book.Chapters) != 1 || len(book.Chapters[0].Paragraphs) != 2 {
		t.Fatalf("bad parsed book %#v", book)
	}
}

func TestParseFB2NormalizesWhitespace(t *testing.T) {
	book, err := ParseFB2(strings.NewReader(`
<FictionBook>
  <description><title-info><book-title> Fixture
    Book </book-title></title-info></description>
  <body>
    <section>
      <title><p> Chapter
        One </p></title>
      <p> First
        paragraph. </p>
      <p> Second     paragraph. </p>
    </section>
  </body>
</FictionBook>`))
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Fixture Book" || book.Chapters[0].Title != "Chapter One" {
		t.Fatalf("bad normalized titles %#v", book)
	}
	if got := textsOf(book.Chapters[0].Paragraphs); len(got) != 2 || got[0] != "First paragraph." || got[1] != "Second paragraph." {
		t.Fatalf("bad normalized paragraphs %#v", got)
	}
}

func TestParseFB2PreservesInlineMarkupText(t *testing.T) {
	book, err := ParseFB2(strings.NewReader(`
<FictionBook>
  <description><title-info><book-title>Fixture Book</book-title></title-info></description>
  <body>
    <section>
      <title><p>Chapter <strong>One</strong></p></title>
      <p>First <emphasis>important</emphasis> paragraph.</p>
    </section>
  </body>
</FictionBook>`))
	if err != nil {
		t.Fatal(err)
	}
	if book.Chapters[0].Title != "Chapter One" {
		t.Fatalf("bad inline title %#v", book.Chapters[0])
	}
	if got := textsOf(book.Chapters[0].Paragraphs); len(got) != 1 || got[0] != "First important paragraph." {
		t.Fatalf("bad inline paragraphs %#v", got)
	}
}

func TestParseFB2ImportsNestedSections(t *testing.T) {
	book, err := ParseFB2(strings.NewReader(`
<FictionBook>
  <description><title-info><book-title>Nested Book</book-title></title-info></description>
  <body>
    <section>
      <title><p>Part One</p></title>
      <section>
        <title><p>Chapter One</p></title>
        <p>First chapter paragraph.</p>
      </section>
      <section>
        <title><p>Chapter Two</p></title>
        <p>Second chapter paragraph.</p>
      </section>
    </section>
  </body>
</FictionBook>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("parsed chapters = %d, want 2", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Chapter One" || len(book.Chapters[0].Paragraphs) != 1 {
		t.Fatalf("first nested chapter = %#v, want chapter one with one paragraph", book.Chapters[0])
	}
	if book.Chapters[1].Title != "Chapter Two" || len(book.Chapters[1].Paragraphs) != 1 {
		t.Fatalf("second nested chapter = %#v, want chapter two with one paragraph", book.Chapters[1])
	}
}

func TestParseFB2RealBookSmoke(t *testing.T) {
	path := os.Getenv("CANONKEEPER_REAL_FB2")
	if path == "" {
		t.Skip("set CANONKEEPER_REAL_FB2 to run real FB2 smoke test")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	book, err := ParseFB2(file)
	if err != nil {
		t.Fatal(err)
	}
	paragraphCount := 0
	for _, chapter := range book.Chapters {
		paragraphCount += len(chapter.Paragraphs)
	}
	if len(book.Chapters) < 10 || paragraphCount < 100 {
		t.Fatalf("parsed real FB2 counts = chapters:%d paragraphs:%d, want substantial book content", len(book.Chapters), paragraphCount)
	}
}

func TestParseFB2RejectsEmptyBook(t *testing.T) {
	_, err := ParseFB2(strings.NewReader(`<FictionBook><body><section><title><p>Only title</p></title></section></body></FictionBook>`))
	if !errors.Is(err, ErrImportNoContent) {
		t.Fatalf("expected no content, got %v", err)
	}
}

// TestParseFB2KeepsSectionOrderAndFallbackTitles: a section's own text is its chapter before the
// chapters of its subsections, even when part of that text follows them, and untitled chapters
// are numbered by their place in the book.
func TestParseFB2KeepsSectionOrderAndFallbackTitles(t *testing.T) {
	book, err := ParseFB2(strings.NewReader(`
<FictionBook>
  <body>
    <section>
      <title><p>Part</p></title>
      <p>Intro.</p>
      <section><p>Untitled.</p></section>
      <section><title><p> </p><p>Child</p></title><p>Child text.</p><epigraph><p>Skipped.</p></epigraph></section>
      <p>Outro.</p>
    </section>
    <section></section>
    <section><p>Last.</p></section>
  </body>
  <body name="notes"><section><title><p>Note</p></title><p>Note text.</p></section></body>
</FictionBook>`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ChapterImport{
		{Title: "Part", Paragraphs: paragraphsOf("Intro.", "Outro.")},
		{Title: "Глава 2", Paragraphs: paragraphsOf("Untitled.")},
		{Title: "Child", Paragraphs: paragraphsOf("Child text.")},
		{Title: "Глава 4", Paragraphs: paragraphsOf("Last.")},
		{Title: "Note", Paragraphs: paragraphsOf("Note text.")},
	}
	if len(book.Chapters) != len(want) {
		t.Fatalf("chapters = %#v, want %#v", book.Chapters, want)
	}
	for index := range want {
		got := book.Chapters[index]
		if got.Title != want[index].Title || strings.Join(textsOf(got.Paragraphs), "|") != strings.Join(textsOf(want[index].Paragraphs), "|") {
			t.Fatalf("chapter %d = %#v, want %#v", index+1, got, want[index])
		}
	}
}

// TestParseFB2AcceptsABookAtTheChapterLimit: empty sections are not chapters and do not count.
func TestParseFB2AcceptsABookAtTheChapterLimit(t *testing.T) {
	data := fb2WithSectionsForTest(maxImportedChapters, `<section><p>а</p></section><section></section>`)

	book, err := ParseFB2(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseFB2() error = %v", err)
	}
	if len(book.Chapters) != maxImportedChapters {
		t.Fatalf("chapters = %d, want %d", len(book.Chapters), maxImportedChapters)
	}
}

// TestParseFB2StopsAtTheChapterLimit (SEC-05, R2.2): the audit file of 200 000 one-letter sections
// stops at chapter 2001 instead of becoming 200 000 chapters in memory.
func TestParseFB2StopsAtTheChapterLimit(t *testing.T) {
	data := fb2WithSectionsForTest(200_000, `<section><p>а</p></section>`)
	reader := &countingReader{reader: bytes.NewReader(data)}

	_, err := ParseFB2(reader)
	if !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("ParseFB2() error = %v, want ErrImportTooManyChapters", err)
	}
	if reader.read > len(data)/10 {
		t.Fatalf("ParseFB2() read %d of %d bytes, want a stop right after the limit", reader.read, len(data))
	}
}

// TestParseFB2StopsOnAFileOfEmptySections (R2.2): empty sections are no chapters, yet a file of
// 200 000 of them stops early instead of being walked whole.
func TestParseFB2StopsOnAFileOfEmptySections(t *testing.T) {
	data := fb2WithSectionsForTest(200_000, `<section></section>`)
	reader := &countingReader{reader: bytes.NewReader(data)}

	_, err := ParseFB2(reader)
	if !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("ParseFB2() error = %v, want ErrImportTooManyChapters", err)
	}
	if reader.read > len(data)/5 {
		t.Fatalf("ParseFB2() read %d of %d bytes, want a stop right after the section cap", reader.read, len(data))
	}
}

// TestParseFB2StopsAtTheTextLimit (R2.2): the parser stops at the paragraph over 10 000 000
// characters; the broken tail after it is never read.
func TestParseFB2StopsAtTheTextLimit(t *testing.T) {
	paragraph := "<p>" + strings.Repeat("я", 1_000_000) + "</p>"
	data := "<FictionBook><body><section>" + strings.Repeat(paragraph, 11) + "<broken"

	_, err := ParseFB2(strings.NewReader(data))
	if !errors.Is(err, ErrImportTooLarge) {
		t.Fatalf("ParseFB2() error = %v, want ErrImportTooLarge", err)
	}
}

// TestParseFB2RejectsDeepNesting: encoding/xml keeps every open element, so nesting is bounded.
func TestParseFB2RejectsDeepNesting(t *testing.T) {
	data := "<FictionBook><body><section><p>" + strings.Repeat("<b>", maxImportXMLDepth) + "я"

	_, err := ParseFB2(strings.NewReader(data))
	if !errors.Is(err, errImportXMLTooDeep) {
		t.Fatalf("ParseFB2() error = %v, want errImportXMLTooDeep", err)
	}
}

func fb2WithSectionsForTest(count int, section string) []byte {
	return []byte("<FictionBook><body>" + strings.Repeat(section, count) + "</body></FictionBook>")
}

// cyrillicFB2ForTest is a book whose text a Russian library would ship in windows-1251 or KOI8-R:
// Cyrillic with ё, a title and two chapters, and only ASCII punctuation, so every Cyrillic
// single-byte encoding holds it.
const cyrillicFB2ForTest = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description><title-info><book-title>Пламя Севера</book-title></title-info></description>
  <body>
    <section>
      <title><p>Глава первая. Ледяной берег</p></title>
      <p>Ветер нёс снег над заливом, и Ёрм смотрел на север.</p>
      <p>"Мы дойдём", - сказал он.</p>
    </section>
    <section>
      <title><p>Глава вторая. Огонь</p></title>
      <p>Костёр горел до утра.</p>
    </section>
  </body>
</FictionBook>`

// TestParseFB2ReadsDeclaredSingleByteEncodings (UC3.3, R3.3.1): an FB2 that declares windows-1251,
// cp1251 or KOI8-R in any letter case, written as a real file in that encoding, is read as the
// same book as its UTF-8 copy.
func TestParseFB2ReadsDeclaredSingleByteEncodings(t *testing.T) {
	want, err := ParseFB2(strings.NewReader(cyrillicFB2ForTest))
	if err != nil {
		t.Fatalf("ParseFB2(UTF-8) error = %v", err)
	}
	for _, test := range []struct {
		label    string
		encoding encoding.Encoding
	}{
		{label: "windows-1251", encoding: charmap.Windows1251},
		{label: "WINDOWS-1251", encoding: charmap.Windows1251},
		{label: "cp1251", encoding: charmap.Windows1251},
		{label: "koi8-r", encoding: charmap.KOI8R},
	} {
		t.Run(test.label, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "book.fb2")
			if err := os.WriteFile(path, fb2InEncodingForTest(t, cyrillicFB2ForTest, test.label, test.encoding), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			got, err := ParseFB2(file)
			if err != nil {
				t.Fatalf("ParseFB2(%s) error = %v", test.label, err)
			}
			if got.Title != want.Title || len(got.Chapters) != len(want.Chapters) {
				t.Fatalf("ParseFB2(%s) = %#v, want %#v", test.label, got, want)
			}
			for index := range want.Chapters {
				if got.Chapters[index].Title != want.Chapters[index].Title ||
					strings.Join(textsOf(got.Chapters[index].Paragraphs), "|") != strings.Join(textsOf(want.Chapters[index].Paragraphs), "|") {
					t.Fatalf("chapter %d in %s = %#v, want %#v", index+1, test.label, got.Chapters[index], want.Chapters[index])
				}
			}
		})
	}
	if want.Title != "Пламя Севера" || want.Chapters[1].Title != "Глава вторая. Огонь" {
		t.Fatalf("UTF-8 book = %#v, want the Cyrillic title and chapters", want)
	}
}

// TestParseFB2ReadsTheWindows1251Fixture (R3.3.1, R7.2): the windows-1251 book the TEST check
// imports by hand stays a real windows-1251 file and parses into its three Cyrillic chapters.
func TestParseFB2ReadsTheWindows1251Fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/cp1251.fb2")
	if err != nil {
		t.Fatal(err)
	}
	if utf8.Valid(data) || !bytes.HasPrefix(data, []byte(`<?xml version="1.0" encoding="windows-1251"?>`)) {
		t.Fatal("testdata/cp1251.fb2 is no longer a windows-1251 file that declares its encoding")
	}

	book, err := ParseFB2(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseFB2(cp1251.fb2) error = %v", err)
	}
	titles := make([]string, 0, len(book.Chapters))
	for _, chapter := range book.Chapters {
		titles = append(titles, chapter.Title)
		if len(chapter.Paragraphs) < 3 {
			t.Fatalf("chapter %q has %d paragraphs, want at least 3", chapter.Title, len(chapter.Paragraphs))
		}
	}
	want := []string{"Глава 1. Туман над гаванью", "Глава 2. Письмо без подписи", "Глава 3. Ёлки у старой мельницы"}
	if book.Title != "Соль и янтарь" || strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("ParseFB2(cp1251.fb2) = %q, chapters %q; want «Соль и янтарь», chapters %q", book.Title, titles, want)
	}
}

// TestParseFB2RefusesAnUnknownEncoding (R3.3.2): a label no WHATWG encoding has is its own error,
// not a broken document.
func TestParseFB2RefusesAnUnknownEncoding(t *testing.T) {
	data := strings.Replace(cyrillicFB2ForTest, `encoding="utf-8"`, `encoding="x-unknown"`, 1)

	_, err := ParseFB2(strings.NewReader(data))
	if !errors.Is(err, errImportUnsupportedEncoding) {
		t.Fatalf("ParseFB2(x-unknown) error = %v, want errImportUnsupportedEncoding", err)
	}
}

// TestParseFB2CountsTheChapterLimitInADeclaredEncoding (R3.3.3): the book limits count the
// decoded text, so a windows-1251 file stops at chapter 2001 like a UTF-8 one.
func TestParseFB2CountsTheChapterLimitInADeclaredEncoding(t *testing.T) {
	utf8 := `<?xml version="1.0" encoding="utf-8"?>` + string(fb2WithSectionsForTest(maxImportedChapters+1, `<section><p>Глава</p></section>`))
	data := fb2InEncodingForTest(t, utf8, "windows-1251", charmap.Windows1251)

	_, err := ParseFB2(bytes.NewReader(data))
	if !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("ParseFB2(windows-1251) error = %v, want ErrImportTooManyChapters", err)
	}
}

// fb2InEncodingForTest declares label in a UTF-8 FB2 and writes its bytes in that encoding.
func fb2InEncodingForTest(t *testing.T, utf8FB2, label string, target encoding.Encoding) []byte {
	t.Helper()
	declared := strings.Replace(utf8FB2, `encoding="utf-8"`, `encoding="`+label+`"`, 1)
	if declared == utf8FB2 {
		t.Fatal("FB2 has no UTF-8 declaration to replace")
	}
	encoded, err := target.NewEncoder().Bytes([]byte(declared))
	if err != nil {
		t.Fatalf("encode FB2 as %s: %v", label, err)
	}
	if bytes.Equal(encoded, []byte(declared)) {
		t.Fatalf("FB2 in %s has the same bytes as UTF-8", label)
	}
	return encoded
}
