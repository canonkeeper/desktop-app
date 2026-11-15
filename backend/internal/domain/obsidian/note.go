// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// Note is a note read by itself (OB3.1): its path in the vault, its name without .md, its header and its text after the
// header — decoded, without a mark of the byte order, its lines ended by "\n", without zero and control signs.
type Note struct {
	Path  string
	Name  string
	Front Front
	Body  string
}

// ReadNote reads the bytes of a note (OB3.1). A note over MaxNoteBytes, a note that does not read without signs of
// replacement and a note of a plugin — a drawing of Excalidraw, a board of Kanban, a note with the inserts of a template —
// are skipped by their reasons.
func ReadNote(notePath string, data []byte) (Note, Skip) {
	notePath = NFC(notePath)
	if len(data) > MaxNoteBytes {
		return Note{}, SkipTooBig
	}
	if isServiceName(Base(notePath)) {
		return Note{}, SkipService
	}
	text, ok := decodeNote(data)
	if !ok {
		return Note{}, SkipEncoding
	}
	lines, body, found := splitFront(text)
	note := Note{Path: notePath, Name: NoteStem(Base(notePath)), Body: body}
	if found {
		note.Front = readFront(lines)
		if _, ok := note.Front.Get("excalidraw-plugin"); ok {
			return Note{}, SkipService
		}
		if _, ok := note.Front.Get("kanban-plugin"); ok {
			return Note{}, SkipService
		}
	}
	if open := strings.Index(body, "<%"); open >= 0 && strings.Contains(body[open:], "%>") {
		return Note{}, SkipService
	}
	return note, ""
}

// decodeNote decodes a note by the rule of a text file of a chapter (chapterfile.DecodeText: UTF-8, UTF-16 by its mark,
// otherwise Windows-1251) and cleans it: the marks of the byte order go, the lines end with "\n", the zero and the control
// signs go, a page or a line feed inside a line is a space. A note in UTF-8 with a broken byte is not a note in
// Windows-1251: its valid signs of UTF-8 outnumber its broken bytes, and it does not read.
func decodeNote(data []byte) (string, bool) {
	text, err := chapterfile.DecodeText(data)
	if err != nil {
		return "", false
	}
	raw := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(raw) && !bytes.HasPrefix(data, []byte{0xFF, 0xFE}) && !bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		if strings.ContainsRune(text, utf8.RuneError) || brokenUTF8(raw) {
			return "", false
		}
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\f' || r == '\v':
			return ' '
		case r == '\uFEFF' || unicode.IsControl(r):
			return -1
		}
		return r
	}, text), true
}

// brokenUTF8 says bytes that are not valid UTF-8 are a text of UTF-8 with broken bytes rather than a text of another
// encoding: they hold more signs of UTF-8 of two bytes and more than broken ones.
func brokenUTF8(data []byte) bool {
	valid, broken := 0, 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		switch {
		case r == utf8.RuneError && size <= 1:
			broken++
		case size > 1:
			valid++
		}
		data = data[size:]
	}
	return valid > broken
}

// cleanTitle is a name for a heading (OB3.3): the control signs and the ends of lines are spaces, the spaces one, NFC.
func cleanTitle(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\uFEFF' {
			return ' '
		}
		return r
	}, NFC(value))
	return strings.Join(strings.Fields(value), " ")
}

// cutTitle cuts a title to MaxTitleRunes runes at the end of a word, and says whether it cut.
func cutTitle(value string) (string, bool) {
	return cutRunes(value, MaxTitleRunes)
}

// cutRunes cuts a text to limit runes at the end of a word when there is one, and says whether it cut.
func cutRunes(value string, limit int) (string, bool) {
	if utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	runes := []rune(value)[:limit]
	cut := string(runes)
	if space := strings.LastIndexFunc(cut, unicode.IsSpace); space > len(cut)/2 {
		cut = cut[:space]
	}
	return strings.TrimSpace(cut), true
}
