package sagadraft

import (
	"slices"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/textface"
)

// The beginning a chapter without a title is named by (specs/2026-11-11-ux9-writing-a.md WA1.1): a few words, up to the
// end of the first sentence.
const (
	openingWords = 6
	openingRunes = 48
)

// Opening is the name of a chapter without a title in the list and in its head: the first words of the idea, else of the
// plain text — «Кира ждёт у ворот…» — up to the end of the first sentence; an ellipsis says more follows. Empty when the
// draft has neither (WA1.1).
func Opening(idea, text string) string {
	source := strings.Fields(idea)
	if len(source) == 0 {
		source = strings.Fields(WithoutMarks(text))
	}
	// A reply begins with its dash: the name is its words.
	for len(source) > 0 && strings.Trim(source[0], "—–-«»\"") == "" {
		source = source[1:]
	}
	if len(source) == 0 {
		return ""
	}
	var kept []string
	runes := 0
	for _, word := range source {
		size := utf8.RuneCountInString(word)
		if len(kept) == openingWords || len(kept) > 0 && runes+1+size > openingRunes {
			break
		}
		kept, runes = append(kept, word), runes+1+size
		if last, _ := utf8.DecodeLastRuneInString(word); strings.ContainsRune(".!?…", last) {
			break
		}
	}
	opening := strings.TrimLeft(strings.Join(kept, " "), "—–-«\" ")
	if cut := []rune(opening); len(cut) > openingRunes {
		opening = string(cut[:openingRunes])
	}
	opening = strings.TrimRight(opening, " .,;:!?…—–-»\"")
	if len(kept) < len(source) || utf8.RuneCountInString(strings.Join(kept, " ")) > openingRunes {
		opening += "…"
	}
	return opening
}

// HeldByBook says the book holds a draft that edits one of its chapters (WA4.1): its working text is the chapter's
// paragraph for paragraph, with the faces the book keeps and without the marks of the helper's text, and its title — a
// title left empty is the chapter's.
func HeldByBook(text, title string, paragraphs []string, bookTitle string) bool {
	if own := strings.TrimSpace(title); own != "" && own != strings.TrimSpace(bookTitle) {
		return false
	}
	return slices.Equal(textface.Book(text), paragraphs)
}

// EditState is where an edit stands against its chapter of the book, as the list and the strip name it (WA1.3, WA4.1).
type EditState string

const (
	// EditSame: the book holds the text and the title of the draft.
	EditSame EditState = "same"
	// EditEdited: the author changed the text or the title: «Заменить главу в книге» puts them there.
	EditEdited EditState = "edited"
	// EditChanged: the chapter changed in the book since the draft took it: the draft does not replace it.
	EditChanged EditState = "changed"
	// EditGone: the chapter is deleted: the draft goes in as a new chapter.
	EditGone EditState = "gone"
)

// State is where the edit stands.
func (edit Edit) State() EditState {
	switch {
	case edit.Gone:
		return EditGone
	case edit.Changed():
		return EditChanged
	case edit.Same:
		return EditSame
	default:
		return EditEdited
	}
}
