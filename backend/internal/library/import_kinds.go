package library

import (
	"bytes"
	"mime"
	"slices"
	"strings"
)

// importFileKind is a kind of a book file the import reads (specs/2026-11-10-book-file-kinds.md K1): the end of its
// name, the media types a browser names a file of it by — the first is the one kept for a text named by none — and what
// its first bytes look like.
type importFileKind struct {
	suffix    string
	mimeTypes []string
	// unnamed says a browser may name a file of the kind by no media type or by application/octet-stream: a text.
	unnamed   bool
	looksLike func([]byte) bool
}

const docxMIMEType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// importFileKinds are the kinds of a book file, in the order the constraints name them (K1.2).
var importFileKinds = []importFileKind{
	{suffix: ".fb2", mimeTypes: []string{"application/x-fictionbook+xml", "text/xml", "application/xml"}, looksLike: isXMLLike},
	{suffix: ".epub", mimeTypes: []string{"application/epub+zip"}, looksLike: isZipLike},
	{suffix: ".docx", mimeTypes: []string{docxMIMEType}, looksLike: isZipLike},
	{suffix: ".txt", mimeTypes: []string{"text/plain"}, unnamed: true, looksLike: isPlainText},
	{suffix: ".md", mimeTypes: []string{"text/markdown", "text/x-markdown", "text/plain"}, unnamed: true, looksLike: isPlainText},
	{suffix: ".rtf", mimeTypes: []string{"application/rtf", "text/rtf"}, looksLike: isRTFLike},
	{suffix: ".odt", mimeTypes: []string{"application/vnd.oasis.opendocument.text"}, looksLike: isZipLike},
	{suffix: ".fb2.zip", mimeTypes: []string{"application/zip", "application/x-zip-compressed"}, looksLike: isZipLike},
}

// ImportFileKind names the kind of a book file by the end of its name (K1.1), in lower case: ".fb2", ".epub", ".docx",
// ".txt", ".md", ".rtf", ".odt" or ".fb2.zip" — the longest kind the name ends in; "" when it ends in none, as «.zip»
// without «.fb2» before it does. The upload of a file, the preflight of a queue item and the start of a queue name the
// kind by it, and the import Job reads the file by the kind they kept.
func ImportFileKind(fileName string) string {
	name := strings.ToLower(strings.TrimSpace(fileName))
	found := ""
	for _, kind := range importFileKinds {
		if strings.HasSuffix(name, kind.suffix) && len(kind.suffix) > len(found) {
			found = kind.suffix
		}
	}
	return found
}

func importFileKindOf(suffix string) (importFileKind, bool) {
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	for _, kind := range importFileKinds {
		if kind.suffix == suffix {
			return kind, true
		}
	}
	return importFileKind{}, false
}

func importFileKindSuffixes() []string {
	suffixes := make([]string, 0, len(importFileKinds))
	for _, kind := range importFileKinds {
		suffixes = append(suffixes, kind.suffix)
	}
	return suffixes
}

func importFileKindMIMETypes() []string {
	var types []string
	for _, kind := range importFileKinds {
		for _, mimeType := range kind.mimeTypes {
			if !slices.Contains(types, mimeType) {
				types = append(types, mimeType)
			}
		}
	}
	return types
}

// validatedImportMIMEType is the media type a file of the kind keeps: the type its browser named, when the import knows
// it, or the first type of the kind for a text its browser named by none or by application/octet-stream (K1.2). A type
// the import does not know is ErrImportUnsupportedMediaType; a known type of another kind is left to
// validateImportExtensionMIMEContent.
func validatedImportMIMEType(extension, value string) (string, error) {
	value = strings.TrimSpace(value)
	kind, _ := importFileKindOf(extension)
	if kind.unnamed && (value == "" || strings.EqualFold(value, "application/octet-stream")) {
		return kind.mimeTypes[0], nil
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", ErrImportUnsupportedMediaType
	}
	mediaType = strings.ToLower(mediaType)
	if kind.unnamed && mediaType == "application/octet-stream" {
		return kind.mimeTypes[0], nil
	}
	if !allowedImportMIMETypeSet[mediaType] {
		return "", ErrImportUnsupportedMediaType
	}
	return mediaType, nil
}

// validateImportExtensionMIMEContent checks that the media type is one of the kind and the first bytes look like it:
// an archive, a FictionBook, an RTF or a text that is not a binary file. A mismatch is ErrImportContentMismatch.
func validateImportExtensionMIMEContent(extension, mimeType string, data []byte) error {
	kind, ok := importFileKindOf(extension)
	if !ok {
		return ErrValidation
	}
	if !slices.Contains(kind.mimeTypes, mimeType) || !kind.looksLike(data) {
		return ErrImportContentMismatch
	}
	return nil
}

// InitialPublicationForImportExtension is how a book of the kind enters the saga: every kind publishes the text it
// imports at once.
func InitialPublicationForImportExtension(extension string) (InitialPublication, error) {
	if _, ok := importFileKindOf(extension); !ok {
		return "", ErrImportUnsupportedMediaType
	}
	return InitialPublicationPublishImported, nil
}

func isZipLike(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) ||
		bytes.HasPrefix(data, []byte("PK\x05\x06")) ||
		bytes.HasPrefix(data, []byte("PK\x07\x08"))
}

func isXMLLike(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("<")) {
		return false
	}
	if bytes.HasPrefix(data, []byte("<FictionBook")) {
		return true
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	return bytes.Contains(sample, []byte("<FictionBook"))
}

// plainTextSampleBytes is how much of a text is looked at for the zero byte of a binary file.
const plainTextSampleBytes = 8 * 1024

// isPlainText says a file is a text, not a binary file renamed: UTF-16 by its mark, or no zero byte in its first 8 KB.
func isPlainText(data []byte) bool {
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		return true
	}
	return bytes.IndexByte(data[:min(len(data), plainTextSampleBytes)], 0) < 0
}

// isRTFLike says a file starts as every RTF document does.
func isRTFLike(data []byte) bool {
	return bytes.HasPrefix(data, []byte(`{\rtf`))
}
