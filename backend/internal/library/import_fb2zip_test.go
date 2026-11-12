package library

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

type zipEntryForTest struct {
	name string
	body []byte
	// declared is the size the archive tells for the entry when it is not its true size; flags are set as they are.
	declared uint64
	flags    uint16
}

// zipForTest packs entries as an archive does: deflated, with the size and the sum of their bytes — or the declared
// size and the flags an entry asks for, written raw.
func zipForTest(t *testing.T, entries ...zipEntryForTest) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		if entry.declared == 0 && entry.flags == 0 {
			file, err := writer.Create(entry.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write(entry.body); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var packed bytes.Buffer
		deflater, err := flate.NewWriter(&packed, flate.BestCompression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deflater.Write(entry.body); err != nil {
			t.Fatal(err)
		}
		if err := deflater.Close(); err != nil {
			t.Fatal(err)
		}
		size := entry.declared
		if size == 0 {
			size = uint64(len(entry.body))
		}
		file, err := writer.CreateRaw(&zip.FileHeader{
			Name: entry.name, Method: zip.Deflate, Flags: entry.flags, CRC32: crc32.ChecksumIEEE(entry.body),
			CompressedSize64: uint64(packed.Len()), UncompressedSize64: size,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(packed.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// K5.1: the one FB2 of an archive is read as an FB2; the files a Mac adds are not books.
func TestParseFB2ZipReadsTheOneBookOfTheArchive(t *testing.T) {
	data := zipForTest(t,
		zipEntryForTest{name: "__MACOSX/._Пламя Севера.fb2", body: []byte("resource fork")},
		zipEntryForTest{name: "Пламя Севера/"},
		zipEntryForTest{name: "Пламя Севера/Пламя Севера.FB2", body: []byte(cyrillicFB2ForTest)},
		zipEntryForTest{name: "Пламя Севера/обложка.jpg", body: []byte("jpeg")},
	)
	book, err := ParseFB2Zip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ParseFB2Zip() error = %v", err)
	}
	if book.Title != "Пламя Севера" || len(book.Chapters) != 2 || book.Chapters[1].Title != "Глава вторая. Огонь" ||
		book.Chapters[1].Paragraphs[0].Text() != "Костёр горел до утра." {
		t.Fatalf("book = %+v, want the book of the FB2 inside", book)
	}
	parsed, err := parseImportBook(data, ".fb2.zip")
	if err != nil || parsed.Title != "Пламя Севера" {
		t.Fatalf("parseImportBook(.fb2.zip) = %q, %v", parsed.Title, err)
	}
}

// K5.1, K5.2: an archive of no FB2 or of two, a broken or a locked one, and a book over the limit — even one whose
// archive tells a smaller size — are refused with their reasons; an FB2 that cannot be read is named as an FB2.
func TestParseFB2ZipRefusesWhatIsNotOneReadableBook(t *testing.T) {
	book := []byte(cyrillicFB2ForTest)
	large := bytes.Repeat([]byte("я"), 2048)
	for _, test := range []struct {
		name  string
		data  []byte
		limit int64
		code  string
	}{
		{name: "no fb2", data: zipForTest(t, zipEntryForTest{name: "Пламя Севера.txt", body: book}), code: "invalid_fb2zip_document"},
		{name: "two fb2", data: zipForTest(t, zipEntryForTest{name: "tom-1.fb2", body: book}, zipEntryForTest{name: "tom-2.fb2", body: book}), code: "invalid_fb2zip_document"},
		{name: "broken archive", data: append([]byte("PK\x03\x04"), bytes.Repeat([]byte{7}, 64)...), code: "invalid_fb2zip_document"},
		{name: "locked", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: book, flags: 0x1}), code: "invalid_fb2zip_document"},
		{name: "over the limit", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: large}), limit: 1024, code: "import.fb2zip_too_large"},
		// An archive that tells a smaller size than its book unpacks to is read by the bytes it unpacks to and stops at
		// the size it told: a broken archive, never more than the limit in memory.
		{name: "a smaller size told", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: large, declared: 100}), limit: 1024, code: "invalid_fb2zip_document"},
		{name: "a smaller size told, under the limit", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: book, declared: 100}), code: "invalid_fb2zip_document"},
		{name: "broken fb2 inside", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: []byte(`<FictionBook><body><section><p>Обрыв`)}), code: "invalid_fb2_document"},
		{name: "empty fb2 inside", data: zipForTest(t, zipEntryForTest{name: "book.fb2", body: []byte(`<FictionBook><body/></FictionBook>`)}), code: "empty_import_content"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit := test.limit
			if limit == 0 {
				limit = MaxImportFileSizeBytes
			}
			_, err := parseFB2Zip(bytes.NewReader(test.data), int64(len(test.data)), limit)
			if err == nil {
				t.Fatal("parseFB2Zip() error = nil")
			}
			if failure := importParseFailure(err, ".fb2.zip"); failure.Code != test.code {
				t.Fatalf("failure = %q (%v), want %q", failure.Code, err, test.code)
			}
		})
	}
}

// K5.1: the limit of the book in the archive is the limit of a book file; a size over it is refused before it is read.
func TestParseFB2ZipHoldsTheLimitOfABookFile(t *testing.T) {
	data := zipForTest(t, zipEntryForTest{name: "book.fb2", body: []byte(cyrillicFB2ForTest), declared: uint64(MaxImportFileSizeBytes) + 1})
	if _, err := ParseFB2Zip(bytes.NewReader(data), int64(len(data))); !errors.Is(err, errFB2ZipTooLarge) {
		t.Fatalf("ParseFB2Zip() error = %v, want the book in the archive too large", err)
	}
	if failure := importParseFailure(errFB2ZipTooLarge, ".fb2.zip"); !strings.Contains(failure.Message, "52428800") {
		t.Fatalf("failure message = %q, want the limit of the server", failure.Message)
	}
}
