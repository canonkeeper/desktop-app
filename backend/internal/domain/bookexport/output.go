package bookexport

import (
	"archive/zip"
	"encoding/xml"
	"hash/crc32"
	"io"
	"time"
)

// output writes a part of a file and keeps its first error, so a writer checks it once, at the end of the part.
type output struct {
	writer io.Writer
	err    error
}

// raw writes the markup of a part.
func (out *output) raw(markup string) {
	if out.err == nil {
		_, out.err = io.WriteString(out.writer, markup)
	}
}

// text writes the words of an author as XML text or as a value of an attribute. A sign XML has no place for
// becomes the replacement sign: one stray sign must not make a whole book unreadable.
func (out *output) text(words string) {
	if out.err == nil {
		out.err = xml.EscapeText(out.writer, []byte(words))
	}
}

// pack is a ZIP file written as it goes: Word and EPUB are both such files.
type pack struct {
	zip  *zip.Writer
	date time.Time
}

func newPack(writer io.Writer, date time.Time) *pack {
	return &pack{zip: zip.NewWriter(writer), date: date}
}

// part writes one compressed file of a pack.
func (pack *pack) part(name string, write func(out *output) error) error {
	file, err := pack.zip.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: pack.date})
	if err != nil {
		return err
	}
	out := &output{writer: file}
	if err := write(out); err != nil {
		return err
	}
	return out.err
}

// markup writes one compressed file of a pack that is known whole.
func (pack *pack) markup(name, content string) error {
	return pack.part(name, func(out *output) error {
		out.raw(content)
		return nil
	})
}

// stored writes a file as it is, with its sizes before its bytes: EPUB wants that of its mimetype, and a picture
// is already compressed.
func (pack *pack) stored(name string, content []byte) error {
	size := uint64(len(content))
	file, err := pack.zip.CreateRaw(&zip.FileHeader{
		Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(content),
		CompressedSize64: size, UncompressedSize64: size,
	})
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	return err
}
