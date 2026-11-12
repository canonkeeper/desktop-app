package library

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

var (
	// errFB2ZipNotOneBook is an archive that holds no FB2 or more than one (specs/2026-11-10-book-file-kinds.md K5.2).
	errFB2ZipNotOneBook = errors.New("library: an fb2.zip archive holds no single FB2 book")
	// errFB2ZipTooLarge is the FB2 of an archive over the limit of a book file.
	errFB2ZipTooLarge = errors.New("library: the book of an fb2.zip archive is larger than a book file may be")
	// errFB2Document is an FB2 whose markup cannot be read, packed or not.
	errFB2Document = errors.New("library: the fb2 document is invalid")
)

// ParseFB2Zip reads the one FB2 book packed in a zip archive as an FB2 is read (K5.1). The book is unpacked no further
// than the limit of a book file, by the bytes it really unpacks to, not by the size the archive tells.
func ParseFB2Zip(reader io.ReaderAt, size int64) (BookImport, error) {
	return parseFB2Zip(reader, size, MaxImportFileSizeBytes)
}

func parseFB2Zip(reader io.ReaderAt, size, limit int64) (BookImport, error) {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return BookImport{}, fmt.Errorf("parse fb2.zip archive: %w", err)
	}
	var book *zip.File
	for _, file := range archive.File {
		if !isFB2ZipBook(file) {
			continue
		}
		if book != nil {
			return BookImport{}, errFB2ZipNotOneBook
		}
		book = file
	}
	if book == nil {
		return BookImport{}, errFB2ZipNotOneBook
	}
	// Bit 0 of the flags is an entry locked by a password.
	if book.Flags&0x1 != 0 {
		return BookImport{}, fmt.Errorf("parse fb2.zip: %s is encrypted", book.Name)
	}
	if book.UncompressedSize64 > uint64(limit) {
		return BookImport{}, errFB2ZipTooLarge
	}
	stream, err := book.Open()
	if err != nil {
		return BookImport{}, fmt.Errorf("open fb2.zip book: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(stream, limit+1))
	_ = stream.Close()
	if int64(len(data)) > limit {
		return BookImport{}, errFB2ZipTooLarge
	}
	if err != nil {
		return BookImport{}, fmt.Errorf("read fb2.zip book: %w", err)
	}
	parsed, err := ParseFB2(bytes.NewReader(data))
	switch {
	case err == nil, errors.Is(err, ErrImportNoContent), errors.Is(err, ErrImportTooManyChapters),
		errors.Is(err, ErrImportTooLarge), errors.Is(err, errImportUnsupportedEncoding):
		return parsed, err
	default:
		return BookImport{}, fmt.Errorf("%w: %w", errFB2Document, err)
	}
}

// isFB2ZipBook says an entry of an archive is a book: a file whose name ends in .fb2, not the copy of its attributes a
// Mac packs beside it.
func isFB2ZipBook(file *zip.File) bool {
	name := strings.ReplaceAll(file.Name, "\\", "/")
	base := path.Base(name)
	return !strings.HasSuffix(name, "/") && strings.EqualFold(path.Ext(base), ".fb2") &&
		!strings.HasPrefix(name, "__MACOSX/") && !strings.HasPrefix(base, "._")
}
