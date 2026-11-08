package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// TestImportLimitsAllowABookUpToTheLimits (R2.1): 2000 chapters and 10 000 000 characters
// (runes, not bytes) still import; one more chapter or character is over the limit.
func TestImportLimitsAllowABookUpToTheLimits(t *testing.T) {
	var chapters importLimits
	for range maxImportedChapters {
		if err := chapters.addChapter(); err != nil {
			t.Fatalf("addChapter() within the limit error = %v", err)
		}
	}
	if err := chapters.addChapter(); !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("addChapter() over the limit error = %v, want ErrImportTooManyChapters", err)
	}

	var text importLimits
	if err := text.addParagraph(strings.Repeat("я", maxImportedTextRunes)); err != nil {
		t.Fatalf("addParagraph() of %d Cyrillic runes error = %v", maxImportedTextRunes, err)
	}
	if err := text.addParagraph("я"); !errors.Is(err, ErrImportTooLarge) {
		t.Fatalf("addParagraph() over the limit error = %v, want ErrImportTooLarge", err)
	}
}

// countingReader counts the bytes a parser took from its input, to see where it stopped.
type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}

// The import queue checks every file before it starts, so a book over the limits is refused
// there with the code the author's text is shown for, not as a broken document.
func TestImportQueuePreflightNamesTheBookLimit(t *testing.T) {
	content := fb2WithSectionsForTest(maxImportedChapters+1, "<section><title><p>Глава</p></title><p>Текст.</p></section>")
	digest := sha256.Sum256(content)
	status, failure, err := preflightImportQueueItem("big.fb2", "application/x-fictionbook+xml", content, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("preflightImportQueueItem() error = %v", err)
	}
	if status == ImportQueueItemReady || !strings.Contains(string(failure), `"code":"import.too_many_chapters"`) {
		t.Fatalf("preflight = %s %s, want import.too_many_chapters", status, failure)
	}
}

// TestImportQueuePreflightReadsTheFileAsTheImportJob (UC3.3, UC3.4, R3.4.1): the preflight accepts
// a windows-1251 FB2 and refuses an unreadable file with the code the import Job gives it.
func TestImportQueuePreflightReadsTheFileAsTheImportJob(t *testing.T) {
	for _, test := range []struct {
		name     string
		fileName string
		mimeType string
		content  []byte
		code     string
	}{
		{name: "windows-1251", fileName: "Пламя Севера.fb2", mimeType: "application/x-fictionbook+xml", content: fb2InEncodingForTest(t, cyrillicFB2ForTest, "windows-1251", charmap.Windows1251)},
		{name: "unknown encoding", fileName: "book.fb2", mimeType: "application/x-fictionbook+xml", content: []byte(strings.Replace(cyrillicFB2ForTest, `encoding="utf-8"`, `encoding="x-unknown"`, 1)), code: "import.unsupported_encoding"},
		{name: "broken markup", fileName: "book.fb2", mimeType: "application/x-fictionbook+xml", content: []byte(`<?xml version="1.0"?><FictionBook><description>`), code: "invalid_fb2_document"},
		{name: "broken docx", fileName: "book.docx", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", content: []byte("PK\x03\x04 not a zip archive"), code: "invalid_docx_document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			digest := sha256.Sum256(test.content)
			status, failure, err := preflightImportQueueItem(test.fileName, test.mimeType, test.content, hex.EncodeToString(digest[:]))
			if err != nil {
				t.Fatalf("preflightImportQueueItem() error = %v", err)
			}
			if test.code == "" {
				if status != ImportQueueItemReady || len(failure) != 0 {
					t.Fatalf("preflight = %s %s, want ready", status, failure)
				}
				return
			}
			if status == ImportQueueItemReady || !strings.Contains(string(failure), `"code":"`+test.code+`"`) {
				t.Fatalf("preflight = %s %s, want %s", status, failure, test.code)
			}
		})
	}
}
