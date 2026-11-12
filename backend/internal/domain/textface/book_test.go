package textface

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// specs/2026-11-08-italic-bold-in-text.md TF3.2: the book takes a working text a line with words a paragraph, its faces in
// the Markdown subset, the marks of the model's text gone, a face over a line break closed at its end; the working text
// of a chapter of the book is its paragraphs as marks. The same table drives the mock of the web.
func TestAWorkingTextGoesIntoTheBookAndBack(t *testing.T) {
	examples := readTable(t).Book
	if len(examples) == 0 {
		t.Fatal("the table has no examples of the book")
	}
	for _, example := range examples {
		if got := Book(example.Text); !reflect.DeepEqual(got, example.Book) {
			t.Fatalf("Book(%q) = %q, want %q", example.Text, got, example.Book)
		}
		var plain []string
		for _, paragraph := range example.Book {
			plain = append(plain, MarkdownPlain(paragraph))
		}
		if want := linesOf(Plain(example.Text).Text); !reflect.DeepEqual(plain, want) {
			t.Fatalf("the plain text of Book(%q) = %q, want the lines of its plain text %q", example.Text, plain, want)
		}
		back := FromBook(example.Book)
		if back != example.Back || !Valid(back) {
			t.Fatalf("FromBook(%q) = %q, want %q", example.Book, back, example.Back)
		}
		if again := Book(back); !reflect.DeepEqual(again, example.Book) {
			t.Fatalf("Book(FromBook(%q)) = %q", example.Book, again)
		}
	}
}

// linesOf is the rule of the book before the faces (sagadraft.Lines): every line with words, trimmed.
func linesOf(text string) []string {
	var lines []string
	start := 0
	for at := 0; at <= len(text); at++ {
		if at == len(text) || text[at] == '\n' {
			line := text[start:at]
			for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
			for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
				line = line[:len(line)-1]
			}
			if line != "" {
				lines = append(lines, line)
			}
			start = at + 1
		}
	}
	return lines
}

// TF4.3: the working text of a chapter from the author's file is its paragraphs with words, each in the marks of its runs.
func TestTheWorkingTextOfAFileHoldsItsFaces(t *testing.T) {
	document := chapterfile.Document{Paragraphs: []chapterfile.Paragraph{
		{Runs: []chapterfile.Run{{Text: "Кира шла "}, {Text: "медленно", Italic: true, Props: `<w:rStyle w:val="Emph"/>`}, {Text: "."}}},
		{},
		{Runs: []chapterfile.Run{{Text: "Стой", Bold: true}, {Text: " ― сказал он."}}},
	}}
	if got := OfDocument(document); got != "Кира шла ⟨медленно⟩.\n\n⟪Стой⟫ ― сказал он." {
		t.Fatalf("OfDocument = %q", got)
	}
	if Plain(OfDocument(document)).Text != document.Text() {
		t.Fatalf("the plain text of the working text is not the text of the file")
	}
}

// TF3.4: the export of the author's data writes a working text in the Markdown subset line by line, the marks of the
// model's text kept as signs and a star of the text escaped.
func TestAWorkingTextInMarkdown(t *testing.T) {
	for text, want := range map[string]string{
		"Он ⟨пришёл⟩.\n\n⟦Туман ⟪стоял⟫⟧ и *звезда*": "Он *пришёл*.\n\n⟦Туман **стоял**⟧ и \\*звезда\\*",
		"⟨а ⟦б⟧ в⟩":       "*а ⟦б⟧ в*",
		"⟨раз\nдва⟩":      "*раз*\n*два*",
		"Без начертания.": "Без начертания.",
	} {
		if got := MarkdownText(text); got != want {
			t.Fatalf("MarkdownText(%q) = %q, want %q", text, got, want)
		}
	}
}

// The import of books takes every run of spaces as one space and none at the edges; a space takes the faces both signs
// around it have, so a face does not hold the space after it.
func TestSpacedRunsKeepTheirFaces(t *testing.T) {
	runs := []chapterfile.Run{{Text: "  Он \n"}, {Text: " пришёл ", Italic: true}, {Text: "  домой", Bold: true}, {Text: " и  ", Bold: true}, {Text: "  лёг.  "}}
	want := []chapterfile.Run{{Text: "Он "}, {Text: "пришёл", Italic: true}, {Text: " "}, {Text: "домой и", Bold: true}, {Text: " лёг."}}
	if got := Spaced(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("Spaced = %#v, want %#v", got, want)
	}
	if got := Spaced([]chapterfile.Run{{Text: " \t "}}); len(got) != 0 {
		t.Fatalf("Spaced of spaces = %#v", got)
	}
	trimmed := Trimmed([]chapterfile.Run{{Text: " \n"}, {Text: " а\tб ", Italic: true}, {Text: "  "}})
	if !reflect.DeepEqual(trimmed, []chapterfile.Run{{Text: "а\tб", Italic: true}}) {
		t.Fatalf("Trimmed = %#v", trimmed)
	}
}

// TF3.3: the faces of a file go onto the text of the book with the same words between other spaces; another word is no.
func TestTheFacesGoOntoTheSameWords(t *testing.T) {
	runs := []chapterfile.Run{{Text: "Он "}, {Text: "пришёл", Italic: true}, {Text: " домой."}}
	got, ok := Onto(runs, "Он  пришёл домой.")
	want := []chapterfile.Run{{Text: "Он  "}, {Text: "пришёл", Italic: true}, {Text: " домой."}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Onto = %#v, %v", got, ok)
	}
	inside, ok := Onto([]chapterfile.Run{{Text: "раз два", Bold: true}}, "раз  два")
	if !ok || !reflect.DeepEqual(inside, []chapterfile.Run{{Text: "раз  два", Bold: true}}) {
		t.Fatalf("Onto inside a face = %#v, %v", inside, ok)
	}
	if _, ok := Onto(runs, "Он ушёл домой."); ok {
		t.Fatal("Onto took another word")
	}
	outside := SpacesOutside([]chapterfile.Run{{Text: " Кира "}, {Text: "шла ", Italic: true}, {Text: "\t", Bold: true}, {Text: "к ", Italic: true, Bold: true}, {Text: "реке", Italic: true}, {Text: " ", Bold: true}})
	want = []chapterfile.Run{{Text: " Кира "}, {Text: "шла \t", Italic: true}, {Text: "к", Italic: true, Bold: true}, {Text: " реке", Italic: true}, {Text: " "}}
	if !reflect.DeepEqual(outside, want) {
		t.Fatalf("SpacesOutside = %#v, want %#v", outside, want)
	}
}

// A chapter is as long as a working text may be (400 000 signs): every way of the text through its faces takes it in
// linear time, not a copy of the text for each of its signs.
func TestALongChapterGoesThroughItsFacesAtOnce(t *testing.T) {
	long := strings.Repeat("слово ", 66000)
	text := "⟨" + long + "⟩\n\n" + long
	started := time.Now()
	runs := Runs(text)
	lines := Lines(text)
	markdown := MarkdownText(text)
	spaced := Spaced(runs)
	onto, ok := Onto(runs, Plain(text).Text)
	back := FromMarkdown(Markdown(runs))
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("a long chapter took %v", elapsed)
	}
	if len(runs) != 2 || len(lines) != 2 || !strings.HasPrefix(markdown, "*слово") || len(spaced) != 2 || !ok || len(onto) != 2 || len(back) != 2 {
		t.Fatalf("runs %d, lines %d, spaced %d, onto %d %v, back %d", len(runs), len(lines), len(spaced), len(onto), ok, len(back))
	}
}
