package library

import (
	"errors"
	"slices"
	"testing"
)

// specs/2026-11-10-book-file-kinds.md K1.1: one function names the kind of a book file by the end of its name.
func TestImportFileKindIsTheEndOfTheName(t *testing.T) {
	for name, want := range map[string]string{
		"Пламя Севера.fb2":      ".fb2",
		"Пламя Севера.FB2":      ".fb2",
		"tom-1.epub":            ".epub",
		"Рукопись.DocX":         ".docx",
		"рукопись.txt":          ".txt",
		"  рукопись.txt  ":      ".txt",
		"глава.md":              ".md",
		"рукопись.rtf":          ".rtf",
		"рукопись.odt":          ".odt",
		"kniga.fb2.zip":         ".fb2.zip",
		"KNIGA.FB2.ZIP":         ".fb2.zip",
		"tom.2.fb2.zip":         ".fb2.zip",
		"kniga.zip":             "",
		"kniga.epub.zip":        "",
		"fb2.zip":               "",
		"rukopis.doc":           "",
		"rukopis.pdf":           "",
		"rukopis":               "",
		"rukopis.":              "",
		"kniga.fb2.zip.part":    "",
		"Том 3. Черновик.docx":  ".docx",
		"zametki.md.txt":        ".txt",
		"книга.fb2.zip.fb2":     ".fb2",
		"dir/inner/kniga.fb2":   ".fb2",
		"kniga.fb2 (1).zip":     "",
		"Том 4 (копия).fb2.zip": ".fb2.zip",
	} {
		if got := ImportFileKind(name); got != want {
			t.Errorf("ImportFileKind(%q) = %q, want %q", name, got, want)
		}
	}
}

// K1.2: the constraints name all eight kinds; each kind publishes what it imports.
func TestImportConstraintsNameTheEightKinds(t *testing.T) {
	want := []string{".fb2", ".epub", ".docx", ".txt", ".md", ".rtf", ".odt", ".fb2.zip"}
	if got := DefaultImportConstraints().AllowedExtensions; !slices.Equal(got, want) {
		t.Fatalf("AllowedExtensions = %q, want %q", got, want)
	}
	for _, kind := range want {
		if publication, err := InitialPublicationForImportExtension(kind); err != nil || publication != InitialPublicationPublishImported {
			t.Fatalf("InitialPublicationForImportExtension(%q) = %q, %v; want publish_imported", kind, publication, err)
		}
	}
}

// K1.2: the media type and the first bytes of each kind; a text a browser names by nothing or by octet-stream is a text.
func TestImportContentOfEachKind(t *testing.T) {
	zip := []byte("PK\x03\x04 the rest of an archive")
	utf16 := append([]byte{0xFF, 0xFE}, []byte("Г\x00л\x00")...)
	binary := append([]byte("Глава 1\n"), 0, 1, 2)
	for _, test := range []struct {
		kind, mimeType string
		data           []byte
		want           error
	}{
		{kind: ".fb2", mimeType: "text/xml", data: []byte(`<?xml version="1.0"?><FictionBook/>`)},
		{kind: ".fb2", mimeType: "text/xml", data: zip, want: ErrImportContentMismatch},
		{kind: ".epub", mimeType: "application/epub+zip", data: zip},
		{kind: ".docx", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data: zip},
		{kind: ".txt", mimeType: "text/plain", data: []byte("Глава 1\n\nТекст.")},
		{kind: ".txt", mimeType: "", data: []byte("Глава 1")},
		{kind: ".txt", mimeType: "application/octet-stream", data: []byte("Глава 1")},
		{kind: ".txt", mimeType: "text/plain; charset=windows-1251", data: []byte{0xc3, 0xeb, 0xe0, 0xe2, 0xe0}},
		{kind: ".txt", mimeType: "text/plain", data: utf16},
		{kind: ".txt", mimeType: "text/plain", data: binary, want: ErrImportContentMismatch},
		{kind: ".txt", mimeType: "application/epub+zip", data: []byte("Глава 1"), want: ErrImportContentMismatch},
		{kind: ".md", mimeType: "text/markdown", data: []byte("# Глава 1")},
		{kind: ".md", mimeType: "text/x-markdown", data: []byte("# Глава 1")},
		{kind: ".md", mimeType: "text/plain", data: []byte("# Глава 1")},
		{kind: ".md", mimeType: "", data: []byte("# Глава 1")},
		{kind: ".md", mimeType: "application/octet-stream", data: []byte("# Глава 1")},
		{kind: ".md", mimeType: "text/markdown", data: binary, want: ErrImportContentMismatch},
		{kind: ".rtf", mimeType: "application/rtf", data: []byte(`{\rtf1\ansi Текст}`)},
		{kind: ".rtf", mimeType: "text/rtf", data: []byte(`{\rtf1\ansi Текст}`)},
		{kind: ".rtf", mimeType: "application/rtf", data: []byte(`{\pict}`), want: ErrImportContentMismatch},
		{kind: ".rtf", mimeType: "application/rtf", data: zip, want: ErrImportContentMismatch},
		{kind: ".rtf", mimeType: "", data: []byte(`{\rtf1}`), want: ErrImportUnsupportedMediaType},
		{kind: ".odt", mimeType: "application/vnd.oasis.opendocument.text", data: zip},
		{kind: ".odt", mimeType: "application/vnd.oasis.opendocument.text", data: []byte("Глава 1"), want: ErrImportContentMismatch},
		{kind: ".odt", mimeType: "application/octet-stream", data: zip, want: ErrImportUnsupportedMediaType},
		{kind: ".fb2.zip", mimeType: "application/zip", data: zip},
		{kind: ".fb2.zip", mimeType: "application/x-zip-compressed", data: zip},
		{kind: ".fb2.zip", mimeType: "application/zip", data: []byte(`<FictionBook/>`), want: ErrImportContentMismatch},
		{kind: ".fb2.zip", mimeType: "application/x-fictionbook+xml", data: zip, want: ErrImportContentMismatch},
		{kind: ".fb2.zip", mimeType: "", data: zip, want: ErrImportUnsupportedMediaType},
	} {
		mimeType, err := validatedImportMIMEType(test.kind, test.mimeType)
		if err == nil {
			err = validateImportExtensionMIMEContent(test.kind, mimeType, test.data)
		}
		if !errors.Is(err, test.want) || (test.want == nil) != (err == nil) {
			t.Errorf("%s as %q = %v, want %v", test.kind, test.mimeType, err, test.want)
		}
	}
}

// K1.2: a text named by nothing keeps the media type of its kind.
func TestImportMIMETypeOfAnUnnamedText(t *testing.T) {
	for kind, want := range map[string]string{".txt": "text/plain", ".md": "text/markdown"} {
		for _, named := range []string{"", "application/octet-stream"} {
			if got, err := validatedImportMIMEType(kind, named); err != nil || got != want {
				t.Errorf("validatedImportMIMEType(%q, %q) = %q, %v; want %q", kind, named, got, err, want)
			}
		}
	}
}
