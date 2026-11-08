package bookexport

import (
	"encoding/base64"
	"io"
	"strings"
)

func writeFB2(writer io.Writer, book Book) error {
	out := &output{writer: writer}
	out.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">`)
	writeFB2Description(out, book)
	out.raw(`<body>`)
	for chapter, title := range book.Chapters {
		paragraphs, err := book.Text(chapter)
		if err != nil {
			return err
		}
		out.raw(`<section><title><p>`)
		out.text(title)
		out.raw(`</p></title>`)
		for _, paragraph := range paragraphs {
			out.raw(`<p>`)
			out.text(paragraph)
			out.raw(`</p>`)
		}
		out.raw(`</section>`)
	}
	out.raw(`</body>`)
	if book.Cover != nil {
		out.raw(`<binary id="` + book.Cover.fileName() + `" content-type="` + book.Cover.MediaType + `">`)
		if out.err == nil {
			encoder := base64.NewEncoder(base64.StdEncoding, writer)
			if _, err := encoder.Write(book.Cover.Content); err != nil {
				return err
			}
			if err := encoder.Close(); err != nil {
				return err
			}
		}
		out.raw(`</binary>`)
	}
	out.raw(`</FictionBook>`)
	return out.err
}

func writeFB2Description(out *output, book Book) {
	// FB2 wants a genre from its list and knows nothing of a book without one; the product does not ask an author for it.
	out.raw(`<description><title-info><genre>prose_contemporary</genre>`)
	writeFB2Author(out, book.Author)
	out.raw(`<book-title>`)
	out.text(book.Title)
	out.raw(`</book-title>`)
	if book.Cover != nil {
		out.raw(`<coverpage><image l:href="#` + book.Cover.fileName() + `"/></coverpage>`)
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
