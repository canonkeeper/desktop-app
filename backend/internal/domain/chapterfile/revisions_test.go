package chapterfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"testing"
)

// wordRevisions counts the corrected places of a Word file as its author sees them: a stretch of revisions — inserted,
// deleted, of formatting, in any mix — without a run of the original or a paragraph mark as it stood between is one
// place; a paragraph mark inserted or deleted belongs to the stretch it touches. A word replaced is one place, though
// Word lists its deletion and its insertion apart. It reads the file, not the writer.
func wordRevisions(t *testing.T, file []byte) int {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(bodyOf(t, file)))
	const plainKind, inserted, deleted, formatted = 0, 1, 2, 3
	count, previous := 0, plainKind
	step := func(kind int) {
		if kind != plainKind && previous == plainKind {
			count++
		}
		previous = kind
	}
	depth, inPPr, mark := 0, false, plainKind
	// The kind of the element a child of the paragraph stands for, and whether text was seen in it.
	child, childText := plainKind, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			switch {
			case element.Name.Local == "p":
				depth, mark = 1, plainKind
			case element.Name.Local == "pPr" && depth == 2:
				inPPr = true
			case inPPr && element.Name.Local == "ins":
				mark = inserted
			case inPPr && element.Name.Local == "del":
				mark = deleted
			case depth == 2 && element.Name.Local == "ins":
				child, childText = inserted, false
			case depth == 2 && element.Name.Local == "del":
				child, childText = deleted, false
			case depth == 2 && element.Name.Local == "r":
				child, childText = plainKind, false
			case element.Name.Local == "rPrChange" && !inPPr:
				child = formatted
			case element.Name.Local == "t" || element.Name.Local == "delText":
				childText = true
			}
		case xml.EndElement:
			switch {
			case element.Name.Local == "p":
				step(mark)
			case element.Name.Local == "pPr" && depth == 2:
				inPPr = false
			case depth == 2 && (element.Name.Local == "ins" || element.Name.Local == "del" || element.Name.Local == "r"):
				if childText {
					step(child)
				}
			}
			depth--
		}
	}
	return count
}

func TestTheRevisionsOfTheFileAreItsCorrectedPlaces(t *testing.T) {
	title := Paragraph{Runs: []Run{{Text: "Глава 7. Переправа"}}}
	original := Document{Format: FormatDOCX, Title: &title, Paragraphs: append([]Paragraph{
		{Runs: []Run{{Text: "Кайл шол к реке, и вода была "}, {Text: "холодной", Italic: true}, {Text: "."}}},
		{},
	}, plain("Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац.")...)}
	same := now("Кайл шол к реке, и вода была ⟨холодной⟩.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац.")
	for name, check := range map[string]struct {
		title string
		lines []Paragraph
		want  int
	}{
		"nothing changed":           {"", same, 0},
		"a letter of a word":        {"", now("Кайл шёл к реке, и вода была ⟨холодной⟩.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац."), 1},
		"a comma put in":            {"", now("Кайл шол к реке, и вода была ⟨холодной⟩.", "Он сказал, что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац."), 1},
		"two faces changed":         {"", now("Кайл шол к реке, и вода была холодной.", "Он сказал что пора ⟨идти⟩.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац."), 2},
		"a paragraph out":           {"", now("Кайл шол к реке, и вода была ⟨холодной⟩.", "Он сказал что пора идти.", "Ольм молчал.", "Последний абзац."), 1},
		"two paragraphs at the end": {"", append(same, now("Ещё один.", "И ещё.")...), 1},
		"the title renamed":         {"Глава 7. Брод", same, 1},
		"words, a comma, one in one out": {"", now(
			"Кайл шёл к реке, и вода была ⟨холодной⟩.", "Он сказал, что пора идти.", "Ольм молчал.", "Новый абзац автора.", "Последний абзац.",
		), 4},
		"everything rewritten": {"", plain("Совсем другой текст.", "И второй абзац."), 7},
	} {
		file := trackedFile(t, original, check.title, check.lines)
		inFile := wordRevisions(t, file)
		if inFile != check.want {
			t.Fatalf("%s: the file has %d revisions, want %d", name, inFile, check.want)
		}
		if got := Revisions(original, check.title, check.lines); got != inFile {
			t.Fatalf("%s: counted %d revisions, the file has %d", name, got, inFile)
		}
	}
}
