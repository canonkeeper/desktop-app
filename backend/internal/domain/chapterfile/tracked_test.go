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
	paragraphs, revisions, italic, _ = facesOf(t, body, accept)
	return paragraphs, revisions, italic
}

// facesOf reads the body of a Word file as revisionsOf does, with the italic and bold words of each run as Word shows
// them after «Принять все» (the properties of the run) or «Отклонить все» (the properties a revision of formatting kept).
func facesOf(t *testing.T, body []byte, accept bool) (paragraphs []string, revisions int, italic, bold []string) {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var current strings.Builder
	inDel, inIns, inPPr, inRun, inChange := false, false, false, false, false
	var now, before [2]bool // italic and bold of the run, and of the properties a revision of formatting kept
	changed := false
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
				inRun, inChange, changed, now, before = true, false, false, [2]bool{}, [2]bool{}
			case "rPrChange":
				revisions++
				inChange, changed = inRun, inRun
			case "i", "b":
				if inRun {
					face := 0
					if element.Name.Local == "b" {
						face = 1
					}
					value := true
					for _, attr := range element.Attr {
						if attr.Name.Local == "val" && (attr.Value == "0" || attr.Value == "false") {
							value = false
						}
					}
					if inChange {
						before[face] = value
					} else {
						now[face] = value
					}
				}
			case "t", "delText":
				var text string
				if err := decoder.DecodeElement(&text, &element); err != nil {
					t.Fatal(err)
				}
				keep := accept && !inDel || !accept && !inIns
				if keep {
					current.WriteString(text)
					faces := now
					if changed && !accept {
						faces = before
					}
					if faces[0] {
						italic = append(italic, text)
					}
					if faces[1] {
						bold = append(bold, text)
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
			case "rPrChange":
				inChange = false
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
	return paragraphs, revisions, italic, bold
}

// bodyOf is word/document.xml of a Word file.
func bodyOf(t *testing.T, file []byte) []byte {
	t.Helper()
	return []byte(partsOf(t, file)["word/document.xml"])
}

func trackedFile(t *testing.T, original Document, title string, lines []Paragraph) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := Tracked(&buffer, original, title, lines, "CanonKeeper", time.Date(2026, 11, 7, 12, 30, 15, 500, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// faced is a paragraph of the text now from its marks: ⟨…⟩ italic, ⟪…⟫ bold.
func faced(text string) Paragraph {
	var runs []Run
	var italic, bold bool
	for _, r := range text {
		switch r {
		case '⟨', '⟩':
			italic = r == '⟨'
		case '⟪', '⟫':
			bold = r == '⟪'
		default:
			if last := len(runs) - 1; last >= 0 && runs[last].Italic == italic && runs[last].Bold == bold {
				runs[last].Text += string(r)
			} else {
				runs = append(runs, Run{Text: string(r), Italic: italic, Bold: bold})
			}
		}
	}
	return Paragraph{Runs: runs}
}

// now is the text now, a paragraph each, from their marks.
func now(texts ...string) []Paragraph {
	paragraphs := make([]Paragraph, len(texts))
	for i, text := range texts {
		paragraphs[i] = faced(text)
	}
	return paragraphs
}

// texts are the words of paragraphs without their faces.
func texts(paragraphs []Paragraph) []string {
	out := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		out[i] = paragraph.Text()
	}
	return out
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
		lines []Paragraph
	}{
		"words, a comma, a paragraph out and one in": {"", now(
			"Кайл шёл к реке, и вода была ⟨холодной⟩.", "Он сказал, что пора идти.", "Ольм молчал.", "Новый абзац автора.", "Последний абзац.",
		)},
		"the last paragraph out": {"", now(
			"Кайл шол к реке, и вода была ⟨холодной⟩.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.",
		)},
		"two paragraphs added at the end and the title renamed": {"Глава 7. Брод", now(
			"Кайл шол к реке, и вода была ⟨холодной⟩.", "Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал.", "Последний абзац.",
			"Ещё один.", "И ещё.",
		)},
		"everything rewritten": {"", plain("Совсем другой текст.", "И второй абзац.")},
	} {
		file := trackedFile(t, original, check.title, check.lines)
		accepted, revisions, italic := revisionsOf(t, file, true)
		wantTitle := original.TitleText()
		if check.title != "" {
			wantTitle = check.title
		}
		if want := append([]string{wantTitle}, texts(check.lines)...); !reflect.DeepEqual(accepted, want) {
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
	file := trackedFile(t, original, "", plain("Раз, два три!"))
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
	file := trackedFile(t, original, "", plain(original.Lines()...))
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

// specs/2026-11-08-italic-bold-in-text.md TF7.2: the file with revisions writes the faces of the text now. An equal word
// whose face changed is a revision of its formatting — the face now, the properties of the original kept — so «Принять
// все» gives the faces now and «Отклонить все» those of the original; inserted words are in the face now.
func TestTheFileWithRevisionsWritesTheFacesNow(t *testing.T) {
	original := Document{Format: FormatText, Paragraphs: []Paragraph{
		{Runs: []Run{{Text: "Кайл шёл к реке, и вода была "}, {Text: "холодной", Italic: true}, {Text: "."}}},
		{Runs: []Run{{Text: "Ольм молчал."}}},
	}}
	file := trackedFile(t, original, "", now("Кайл ⟨шёл⟩ к ⟪реке⟫, и вода была холодной.", "Ольм ⟪долго⟫ молчал."))
	accepted, revisions, italic, bold := facesOf(t, bodyOf(t, file), true)
	if want := []string{"Кайл шёл к реке, и вода была холодной.", "Ольм долго молчал."}; !reflect.DeepEqual(accepted, want) || revisions == 0 {
		t.Fatalf("accepted %q, revisions %d", accepted, revisions)
	}
	if !reflect.DeepEqual(italic, []string{"шёл"}) || !reflect.DeepEqual(bold, []string{"реке", "долго"}) {
		t.Fatalf("accepted italic %q, bold %q", italic, bold)
	}
	rejected, _, italic, bold := facesOf(t, bodyOf(t, file), false)
	if !reflect.DeepEqual(rejected, original.Lines()) || !reflect.DeepEqual(italic, []string{"холодной"}) || len(bold) != 0 {
		t.Fatalf("rejected %q, italic %q, bold %q", rejected, italic, bold)
	}
	document := string(bodyOf(t, file))
	if strings.Count(document, "<w:rPrChange ") != 3 || !strings.Contains(document, `<w:rPrChange w:id="`) ||
		!strings.Contains(document, `<w:ins `) {
		t.Fatalf("revisions of formatting:\n%s", document)
	}
	unchanged := trackedFile(t, original, "", now("Кайл шёл к реке, и вода была ⟨холодной⟩.", "Ольм молчал."))
	if _, revisions, _, _ := facesOf(t, bodyOf(t, unchanged), true); revisions != 0 {
		t.Fatalf("the same faces made %d revisions", revisions)
	}
	// A working text without one face — from before its faces were kept, or from a text — keeps those of the original.
	faceless := trackedFile(t, original, "", now("Кайл шёл к реке, и вода была холодной.", "Ольм долго молчал."))
	if _, revisions, italic, _ := facesOf(t, bodyOf(t, faceless), true); revisions != 1 || !reflect.DeepEqual(italic, []string{"холодной"}) {
		t.Fatalf("a text without faces: %d revisions, italic %q", revisions, italic)
	}
}

// TF7.2 with the look of the author's file (PR6.7): the properties of the run now are those of the original with its
// italic or bold switched in their place, and the revision keeps the properties of the original as they stood.
func TestAChangedFaceKeepsTheLookOfTheAuthorsWordFile(t *testing.T) {
	original, err := Parse("Глава 3.docx", layoutFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if runs := original.Paragraphs[0].Runs; len(runs) != 3 || !runs[1].Italic || runs[1].Text != "медленно" {
		t.Fatalf("the italic of the character style is not read: %#v", runs)
	}
	file := trackedFile(t, original, "Глава 3. Брод", now("Кира ⟨шла⟩ медленно.", "Ольм ждал у ⟪ворот⟫."))
	document := string(bodyOf(t, file))
	for _, want := range []string{
		`<w:r><w:t xml:space="preserve">Кира </w:t></w:r>`,
		`<w:r><w:rPr><w:i/><w:rPrChange w:id="1" w:author="CanonKeeper" w:date="2026-11-07T12:30:15Z"><w:rPr></w:rPr></w:rPrChange></w:rPr><w:t xml:space="preserve">шла</w:t></w:r>`,
		`<w:r><w:rPr><w:rStyle w:val="Emph"/><w:i w:val="0"/><w14:ligatures w14:val="standard"/><w:rPrChange w:id="2" w:author="CanonKeeper" w:date="2026-11-07T12:30:15Z">` +
			`<w:rPr><w:rStyle w:val="Emph"/><w14:ligatures w14:val="standard"/></w:rPr></w:rPrChange></w:rPr><w:t xml:space="preserve">медленно</w:t></w:r>`,
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("document.xml has no %s:\n%s", want, document)
		}
	}
	accepted, _, italic, bold := facesOf(t, []byte(document), true)
	if !reflect.DeepEqual(accepted, []string{"Глава 3. Брод", "Кира шла медленно.", "Ольм ждал у ворот."}) ||
		!reflect.DeepEqual(italic, []string{"шла"}) || !reflect.DeepEqual(bold, []string{"ворот"}) {
		t.Fatalf("accepted %q, italic %q, bold %q", accepted, italic, bold)
	}
	if got := toggled(`<w:rStyle w:val="A"/><w:rFonts w:ascii="X"/><w:b/><w:sz w:val="24"/>`, "i", true); got != `<w:rStyle w:val="A"/><w:rFonts w:ascii="X"/><w:b/><w:i/><w:sz w:val="24"/>` {
		t.Fatalf("italic in its place = %s", got)
	}
	if got := toggled(`<w:i/><w:iCs/>`, "b", false); got != `<w:b w:val="0"/><w:i/><w:iCs/>` {
		t.Fatalf("bold off in its place = %s", got)
	}
	if got := toggled(`<w:b w:val="1"/><w:i w:val="0"/>`, "i", true); got != `<w:b w:val="1"/><w:i/>` {
		t.Fatalf("italic switched = %s", got)
	}
}
