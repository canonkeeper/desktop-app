package bookexport

import (
	"encoding/base64"
	"io"
	"strings"
)

func writeFB2(writer io.Writer, book *bookFile) error {
	// The binaries of FB2 follow its body: a picture is read before the body, to know it is there, and again for its
	// binary, so no more than one is in memory (specs/2026-11-12-ux10-book-pictures.md BP2.2, BP2.4).
	if err := book.readPictures(nil); err != nil {
		return err
	}
	out := &output{writer: writer}
	out.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">`)
	writeFB2Description(out, book)
	out.raw(`<body>`)
	for chapter, title := range book.Chapters {
		blocks, err := book.chapter(chapter)
		if err != nil {
			return err
		}
		out.raw(`<section><title><p>`)
		out.text(title)
		out.raw(`</p></title>`)
		for _, block := range blocks {
			if picture := block.picture; picture != nil {
				// A drawing stands between the paragraphs of its section: the import of this very file takes no text of it (BP2.3).
				out.raw(`<image l:href="#` + picture.fileName() + `" alt="`)
				out.text(picture.name())
				out.raw(`"/>`)
				continue
			}
			out.raw(`<p>`)
			writeFaced(out, block.paragraph, `<emphasis>`, `<strong>`)
			out.raw(`</p>`)
		}
		out.raw(`</section>`)
	}
	out.raw(`</body>`)
	if book.Cover != nil {
		if err := writeFB2Binary(out, coverFileName(*book.Cover), book.Cover.MediaType, book.Cover.Content); err != nil {
			return err
		}
	}
	for _, picture := range book.pictured() {
		// A picture gone since it was read leaves its place empty: the book is what the author came for.
		if image, err := picture.Read(); err == nil && image.readable() {
			if err := writeFB2Binary(out, picture.fileName(), picture.mediaType, image.Content); err != nil {
				return err
			}
		}
	}
	out.raw(`</FictionBook>`)
	return out.err
}

// writeFB2Binary writes a picture in base64 as it goes.
func writeFB2Binary(out *output, id, mediaType string, content []byte) error {
	out.raw(`<binary id="` + id + `" content-type="` + mediaType + `">`)
	if out.err == nil {
		encoder := base64.NewEncoder(base64.StdEncoding, out.writer)
		if _, err := encoder.Write(content); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
	}
	out.raw(`</binary>`)
	return out.err
}

func writeFB2Description(out *output, book *bookFile) {
	// FB2 wants a genre from its list and knows nothing of a book without one; the product does not ask an author for it.
	out.raw(`<description><title-info><genre>prose_contemporary</genre>`)
	writeFB2Author(out, book.Author)
	out.raw(`<book-title>`)
	out.text(book.Title)
	out.raw(`</book-title>`)
	if book.Cover != nil {
		out.raw(`<coverpage><image l:href="#` + coverFileName(*book.Cover) + `"/></coverpage>`)
	}
	date := book.Date.UTC().Format("2006-01-02")
	out.raw(`<lang>ru</lang></title-info><document-info>`)
	writeFB2Author(out, book.Author)
	out.raw(`<date value="` + date + `">` + date + `</date><id>`)
	out.text(book.ID)
	out.raw(`</id><version>1.0</version></document-info></description>`)
}

// writeFB2Author splits a name as FB2 wants it: a name of one word is a nickname there.
func writeFB2Author(out *output, name string) {
	words := strings.Fields(name)
	out.raw(`<author>`)
	if len(words) < 2 {
		out.raw(`<nickname>`)
		out.text(name)
		out.raw(`</nickname></author>`)
		return
	}
	out.raw(`<first-name>`)
	out.text(words[0])
	out.raw(`</first-name>`)
	if len(words) > 2 {
		out.raw(`<middle-name>`)
		out.text(strings.Join(words[1:len(words)-1], " "))
		out.raw(`</middle-name>`)
	}
	out.raw(`<last-name>`)
	out.text(words[len(words)-1])
	out.raw(`</last-name></author>`)
}
