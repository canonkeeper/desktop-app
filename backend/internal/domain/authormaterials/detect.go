package authormaterials

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

// What an import does not read, each said apart so the page tells the author what to do with the file.
var (
	// ErrProtected is a workbook or a document under a password.
	ErrProtected = errors.New("author materials: file under a password")
	// ErrOldFormat is a file of the old Office formats: XLS, DOC or RTF.
	ErrOldFormat = errors.New("author materials: old Office format")
	// ErrOpenDocument is a file of LibreOffice: ODS or ODT.
	ErrOpenDocument = errors.New("author materials: OpenDocument file")
	// ErrPDF is a PDF.
	ErrPDF = errors.New("author materials: PDF")
	// ErrPicture is a picture.
	ErrPicture = errors.New("author materials: picture")
	// ErrPlainText is a text file: CSV or TXT.
	ErrPlainText = errors.New("author materials: plain text file")
)

// Parse reads a file by what it holds, whatever its name says: a workbook named .docx or a macro workbook is read as a
// workbook (R9.2, R9.3).
func Parse(data []byte) (Parsed, SourceKind, error) {
	if len(data) == 0 {
		return Parsed{}, "", ErrEmpty
	}
	if len(data) > MaxFileBytes {
		return Parsed{}, "", ErrTooLarge
	}
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return parseArchive(data)
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}):
		// A compound file: the old XLS and DOC, and a workbook or a document under a password, which keeps them in EncryptedPackage.
		if bytes.Contains(data, utf16le("EncryptedPackage")) {
			return Parsed{}, "", ErrProtected
		}
		return Parsed{}, "", ErrOldFormat
	case bytes.HasPrefix(data, []byte(`{\rtf`)):
		return Parsed{}, "", ErrOldFormat
	case bytes.HasPrefix(data, []byte("%PDF")):
		return Parsed{}, "", ErrPDF
	case isPicture(data):
		return Parsed{}, "", ErrPicture
	case isText(data):
		return Parsed{}, "", ErrPlainText
	default:
		return Parsed{}, "", ErrUnreadable
	}
}

// parseArchive tells a workbook, a document and a file of LibreOffice apart by the entries of the zip.
func parseArchive(data []byte) (Parsed, SourceKind, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Parsed{}, "", ErrUnreadable
	}
	for _, file := range reader.File {
		switch file.Name {
		case "xl/workbook.xml":
			parsed, err := ParseXLSX(data)
			return parsed, SourceXLSX, err
		case "word/document.xml":
			parsed, err := ParseDOCX(data)
			return parsed, SourceDOCX, err
		case "mimetype":
			if kind, err := entryStart(file, 64); err == nil && strings.HasPrefix(kind, "application/vnd.oasis.opendocument") {
				return Parsed{}, "", ErrOpenDocument
			}
		}
	}
	return Parsed{}, "", ErrUnreadable
}

func entryStart(file *zip.File, size int64) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	start, err := io.ReadAll(io.LimitReader(reader, size))
	return string(start), err
}

func utf16le(text string) []byte {
	var out []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func isPicture(data []byte) bool {
	for _, magic := range [][]byte{[]byte("\x89PNG"), {0xFF, 0xD8, 0xFF}, []byte("GIF8"), []byte("BM")} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
	}
	return len(data) >= 12 && (string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" ||
		string(data[4:8]) == "ftyp" && strings.HasPrefix(string(data[8:12]), "hei"))
}

// isText says whether a file is text in any encoding: its first kilobytes hold no zero byte and few control characters.
func isText(data []byte) bool {
	start := data[:min(len(data), 8192)]
	control := 0
	for _, char := range start {
		switch {
		case char == 0:
			return false
		case char < 0x20 && char != '\n' && char != '\r' && char != '\t':
			control++
		}
	}
	return control*100 < len(start)
}
