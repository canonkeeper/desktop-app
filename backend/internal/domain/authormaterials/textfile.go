package authormaterials

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// The text files of an import (specs/2026-11-10-ux8-memory-records.md MR2.4): a .txt or a .md is read as a pasted text, a .csv
// as a table of one sheet. Both come in UTF-8 or Windows-1251, read here without anything but the standard library.

// ParseText reads a pasted text: each line with something on it is a paragraph.
func ParseText(text string) (Parsed, error) {
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return Parsed{}, ErrTooLarge
	}
	return paragraphsOf(text)
}

// ParseTextFile reads a .txt or a .md as a pasted text; the file is bound by the limits of a file, not of a paste.
func ParseTextFile(data []byte) (Parsed, error) {
	return paragraphsOf(decodeText(data))
}

func paragraphsOf(text string) (Parsed, error) {
	var section Section
	for _, line := range strings.Split(strings.ToValidUTF8(text, ""), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			section.Paragraphs = append(section.Paragraphs, Paragraph{Text: line})
		}
	}
	if len(section.Paragraphs) == 0 {
		return Parsed{}, ErrEmpty
	}
	if len(section.Paragraphs) > maxParagraphs {
		return Parsed{}, ErrTooLarge
	}
	return Parsed{Sections: []Section{section}}, nil
}

// maxCSVColumns bounds a row of a table, as a sheet bounds it.
const maxCSVColumns = 512

// ParseCSV reads a table of one sheet: its separator — a comma, a semicolon or a tab — is the one its first line holds most
// of. Its sheet has no name, so it is «Лист 1».
func ParseCSV(data []byte) (Parsed, error) {
	text := decodeText(data)
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma, reader.LazyQuotes, reader.FieldsPerRecord = separatorOf(text), true, -1
	var rows []sheetRow
	previous := 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Parsed{}, ErrUnreadable
		}
		line, _ := reader.FieldPos(0)
		if len(record) > maxCSVColumns {
			return Parsed{}, ErrTooLarge
		}
		cells := make([]Cell, len(record))
		filled := false
		for column, value := range record {
			cells[column] = Cell{Text: strings.TrimSpace(value)}
			filled = filled || cells[column].Text != ""
		}
		if !filled {
			continue
		}
		rows = append(rows, sheetRow{number: line, gap: len(rows) > 0 && line > previous+1, cells: cells})
		if len(rows) > maxRows {
			return Parsed{}, ErrTooLarge
		}
		previous = line
	}
	sections := sheetSections(sheetTitle("", 0), rows)
	if len(sections) == 0 {
		return Parsed{}, ErrEmpty
	}
	return Parsed{Sections: sections}, nil
}

// separatorOf is the separator of a table by its first line, outside the quotes; a comma when the line has none.
func separatorOf(text string) rune {
	first, _, _ := strings.Cut(text, "\n")
	counts := map[rune]int{}
	quoted := false
	for _, char := range first {
		switch {
		case char == '"':
			quoted = !quoted
		case !quoted && (char == ',' || char == ';' || char == '\t'):
			counts[char]++
		}
	}
	separator := ','
	for _, candidate := range []rune{';', '\t'} {
		if counts[candidate] > counts[separator] {
			separator = candidate
		}
	}
	return separator
}

// sheetTitle is the name of a sheet, or «Лист N» by its place among the sheets when it has none: a section is never named by
// the names of its columns alone (MR3.4).
func sheetTitle(name string, place int) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return fmt.Sprintf("Лист %d", place+1)
}

// decodeText is a text file as UTF-8: without its byte order mark, and from Windows-1251 when it is not UTF-8.
func decodeText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if utf8.Valid(data) {
		return string(data)
	}
	var out strings.Builder
	out.Grow(len(data) * 2)
	for _, char := range data {
		switch {
		case char < 0x80:
			out.WriteByte(char)
		case char >= 0xC0:
			out.WriteRune(rune(0x0410 + int(char) - 0xC0))
		default:
			out.WriteRune(windows1251[char-0x80])
		}
	}
	return out.String()
}

// windows1251 is the code page from 0x80 to 0xBF; from 0xC0 its letters run А to я.
var windows1251 = [64]rune{
	0x0402, 0x0403, 0x201A, 0x0453, 0x201E, 0x2026, 0x2020, 0x2021, 0x20AC, 0x2030, 0x0409, 0x2039, 0x040A, 0x040C, 0x040B, 0x040F,
	0x0452, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0xFFFD, 0x2122, 0x0459, 0x203A, 0x045A, 0x045C, 0x045B, 0x045F,
	0x00A0, 0x040E, 0x045E, 0x0408, 0x00A4, 0x0490, 0x00A6, 0x00A7, 0x0401, 0x00A9, 0x0404, 0x00AB, 0x00AC, 0x00AD, 0x00AE, 0x0407,
	0x00B0, 0x00B1, 0x0406, 0x0456, 0x0491, 0x00B5, 0x00B6, 0x00B7, 0x0451, 0x2116, 0x0454, 0x00BB, 0x0458, 0x0405, 0x0455, 0x0457,
}
