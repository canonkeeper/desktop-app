package chapterfile

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// decodeText reads the bytes of a text file (PR1.3): UTF-8 with or without its mark, UTF-16 with its mark, otherwise
// Windows-1251 — what a Russian text saved by an old editor is.
func decodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		decoded, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder(), data)
		if err != nil {
			return "", ErrUnreadable
		}
		return string(decoded), nil
	}
	if utf8.Valid(data) {
		return string(data), nil
	}
	decoded, _, err := transform.Bytes(charmap.Windows1251.NewDecoder(), data)
	if err != nil {
		return "", ErrUnreadable
	}
	return string(decoded), nil
}

// parsePlain reads a text file: every line with words is a paragraph.
func parsePlain(data []byte, format Format) (Document, error) {
	text, err := decodeText(data)
	if err != nil {
		return Document{}, err
	}
	return plainDocument(text, format), nil
}

func plainDocument(text string, format Format) Document {
	document := Document{Format: format}
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			document.Paragraphs = append(document.Paragraphs, Paragraph{Runs: []Run{{Text: line}}})
		}
	}
	return document
}

// parseMarkdown reads a Markdown file (PR1.3): a block between blank lines is a paragraph and its lines are joined by a
// space; a heading line «#» is a block of its own, and the first one, before any text, is the title; *…* and _…_ are
// italic, **…** and __…__ bold, a backslash keeps the sign after it. A line of stars or dashes alone («* * *») is text.
func parseMarkdown(data []byte) (Document, error) {
	text, err := decodeText(data)
	if err != nil {
		return Document{}, err
	}
	document := Document{Format: FormatMarkdown}
	var block []string
	flush := func() {
		if len(block) > 0 {
			document.Paragraphs = append(document.Paragraphs, Paragraph{Runs: inline(strings.Join(block, " "))})
			block = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case strings.Trim(trimmed, "*-_ ") == "":
			flush()
			document.Paragraphs = append(document.Paragraphs, Paragraph{Runs: []Run{{Text: trimmed}}})
		case strings.HasPrefix(trimmed, "#"):
			flush()
			heading := Paragraph{Runs: inline(strings.TrimSpace(strings.Trim(strings.TrimLeft(trimmed, "#"), "# ")))}
			if document.Title == nil && len(document.Lines()) == 0 {
				document.Title = &heading
				continue
			}
			document.Paragraphs = append(document.Paragraphs, heading)
		default:
			block = append(block, trimmed)
		}
	}
	flush()
	return document, nil
}

// inline reads the faces of a line of Markdown. A sign without its pair stays text.
func inline(line string) []Run {
	type piece struct {
		text string
		sign string // "**", "__", "*", "_" or ""
	}
	var pieces []piece
	var text strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && i+1 < len(runes) && strings.ContainsRune(`\*_#[]()-.!>`, runes[i+1]):
			text.WriteRune(runes[i+1])
			i++
		case r == '*' || r == '_':
			sign := string(r)
			if i+1 < len(runes) && runes[i+1] == r {
				sign += string(r)
				i++
			}
			// An underscore inside a word is the word's: snake_case, not italic.
			if r == '_' && i+1 < len(runes) && i-len(sign) >= 0 && isWordRune(runes[i-len(sign)]) && isWordRune(runes[i+1]) {
				text.WriteString(sign)
				continue
			}
			pieces = append(pieces, piece{text: text.String()})
			text.Reset()
			pieces = append(pieces, piece{sign: sign})
		default:
			text.WriteRune(r)
		}
	}
	pieces = append(pieces, piece{text: text.String()})
	// Pair the signs: an opening one with the next of the same kind.
	open := map[string]int{}
	pairs := map[int]int{}
	for i, item := range pieces {
		if item.sign == "" {
			continue
		}
		if start, ok := open[item.sign]; ok {
			pairs[start], pairs[i] = i, start
			delete(open, item.sign)
			continue
		}
		open[item.sign] = i
	}
	var runs []Run
	italic, bold := 0, 0
	for i, item := range pieces {
		if item.sign == "" {
			if item.text != "" {
				runs = append(runs, Run{Text: item.text, Italic: italic > 0, Bold: bold > 0})
			}
			continue
		}
		other, paired := pairs[i]
		if !paired {
			runs = append(runs, Run{Text: item.sign, Italic: italic > 0, Bold: bold > 0})
			continue
		}
		step := 1
		if other < i {
			step = -1
		}
		if len(item.sign) == 2 {
			bold += step
		} else {
			italic += step
		}
	}
	return runs
}

func isWordRune(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 'а' && r <= 'я' || r >= 'А' && r <= 'Я' || r == 'ё' || r == 'Ё'
}
