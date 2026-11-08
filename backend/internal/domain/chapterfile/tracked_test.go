package chapterfile

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// revisionsOf reads a Word file the way Word applies «Принять все» (accept) or «Отклонить все»: the text of each
// paragraph and paragraphs joined where their mark was deleted (accepted) or inserted (rejected). Empty paragraphs go.
func revisionsOf(t *testing.T, file []byte, accept bool) (paragraphs []string, revisions int, italic []string) {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, part := range archive.File {
		stream, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		// Every part is well-formed XML.
		decoder := xml.NewDecoder(bytes.NewReader(content))
		for {
			if _, err := decoder.Token(); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("%s: %v", part.Name, err)
			}
		}
		if part.Name == "word/document.xml" {
			body = content
		}
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var current strings.Builder
	inDel, inIns, inPPr, inRun, runItalic := false, false, false, false, false
	join := false
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
			switch element.Name.Local {
			case "p":
				join = false
			case "pPr":
				inPPr = true
			case "del":
				revisions++
				if inPPr {
					join = join || accept
				} else {
					inDel = true
				}
			case "ins":
				revisions++
				if inPPr {
					join = join || !accept
				} else {
					inIns = true
				}
			case "r":
				inRun, runItalic = true, false
			case "i":
				runItalic = inRun
			case "t", "delText":
				var text string
				if err := decoder.DecodeElement(&text, &element); err != nil {
					t.Fatal(err)
				}
				keep := accept && !inDel || !accept && !inIns
				if keep {
					current.WriteString(text)
					if runItalic {
						italic = append(italic, text)
					}
				}
				if (element.Name.Local == "delText") != inDel {
					t.Fatalf("text %q in the wrong element", text)
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "pPr":
				inPPr = false
			case "del":
				inDel = false
			case "ins":
				inIns = false
			case "r":
				inRun = false
			case "p":
				if !join {
					if text := current.String(); text != "" {
						paragraphs = append(paragraphs, text)
					}
					current.Reset()
				}
			}
		}
	}
	return paragraphs, revisions, italic
}

func trackedFile(t *testing.T, original Document, title string, lines []string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := Tracked(&buffer, original, title, lines, "CanonKeeper", time.Date(2026, 11, 7, 12, 30, 15, 500, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func plain(texts ...string) []Paragraph {
	paragraphs := make([]Paragraph, len(texts))
	for i, text := range texts {
		if text != "" {
			paragraphs[i] = Paragraph{Runs: []Run{{Text: text}}}
		}
	}
	return paragraphs
}

func TestAcceptingAllRevisionsGivesTheTextNowAndRejectingThemTheOriginal(t *testing.T) {
	title := Paragraph{Runs: []Run{{Text: "Глава 7. Переправа"}}}
	original := Document{Format: FormatDOCX, Title: &title, Paragraphs: append([]Paragraph{
		{Runs: []Run{{Text: "Кайл шол к реке, и вода была "}, {Text: "холодной", Italic: true}, {Text: "."}}},
		{},
	}, plain("Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац.")...)}
	for name, check := range map[string]struct {
		title string
		lines []string
	}{
		"words, a comma, a paragraph out and one in": {"", []string{
			"Кайл шёл к реке, и вода была холодной.", "Он сказал, что пора идти.", "Ольм молчал.", "Новый абзац автора.", "Последний абзац.",
		}},
		"the last paragraph out": {"", []string{
			"Кайл шол к реке, и вода была холодной.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.",
		}},
		"two paragraphs added at the end and the title renamed": {"Глава 7. Брод", []string{
			"Кайл шол к реке, и вода была холодной.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац.",
			"Ещё один.", "И ещё.",
		}},
		"everything rewritten": {"", []string{"Совсем другой текст.", "И второй абзац."}},
	} {
		file := trackedFile(t, original, check.title, check.lines)
		accepted, revisions, italic := revisionsOf(t, file, true)
		wantTitle := original.TitleText()
		if check.title != "" {
			wantTitle = check.title
		}
		if want := append([]string{wantTitle}, check.lines...); !reflect.DeepEqual(accepted, want) {
			t.Fatalf("%s: accepted\n%q\nwant\n%q", name, accepted, want)
		}
		rejected, _, _ := revisionsOf(t, file, false)
		if want := append([]string{original.TitleText()}, original.Lines()...); !reflect.DeepEqual(rejected, want) {
			t.Fatalf("%s: rejected\n%q\nwant\n%q", name, rejected, want)
		}
		if revisions == 0 {
			t.Fatalf("%s: no revisions", name)
		}
		if name == "words, a comma, a paragraph out and one in" && !reflect.DeepEqual(italic, []string{"холодной"}) {
			t.Fatalf("%s: italic %q", name, italic)
		}
	}
}

func TestEveryRevisionNamesItsAuthorAndTime(t *testing.T) {
	original := Document{Format: FormatText, Paragraphs: plain("Раз два три.")}
	file := trackedFile(t, original, "", []string{"Раз, два три!"})
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	var document, settings string
	for _, part := range archive.File {
		stream, _ := part.Open()
		content, _ := io.ReadAll(stream)
		stream.Close()
		switch part.Name {
		case "word/document.xml":
			document = string(content)
		case "word/settings.xml":
			settings = string(content)
		}
	}
	revisions := strings.Count(document, "<w:ins ") + strings.Count(document, "<w:del ")
	if revisions != 3 || strings.Count(document, `w:author="CanonKeeper" w:date="2026-11-07T12:30:15Z"`) != revisions {
		t.Fatalf("revisions %d in %s", revisions, document)
	}
	if !strings.Contains(settings, "<w:trackRevisions/>") {
		t.Fatalf("the file does not open with revisions shown: %s", settings)
	}
}

func TestAnUnchangedChapterHasNoRevisions(t *testing.T) {
	original := Document{Format: FormatText, Paragraphs: plain("Первый.", "", "Второй.")}
	file := trackedFile(t, original, "", original.Lines())
	accepted, revisions, _ := revisionsOf(t, file, true)
	if revisions != 0 || !reflect.DeepEqual(accepted, original.Lines()) {
		t.Fatalf("revisions %d, text %q", revisions, accepted)
	}
}

func TestADifferenceTooLargeForOneTableIsAnchoredByUniqueLines(t *testing.T) {
	var a, b []string
	for i := range 3000 {
		line := strings.Repeat("x", i%7) + string(rune('А'+i%32)) + strings.Repeat("y", i/32)
		a = append(a, line)
		if i%100 == 0 {
			b = append(b, line+" правка")
			continue
		}
		b = append(b, line)
	}
	edits := difference(a, b)
	equal := 0
	for _, step := range edits {
		if step.kind == editEqual {
			equal++
		}
	}
	if equal != 2970 {
		t.Fatalf("equal %d of 3000", equal)
	}
}
