package textface

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// table is testdata/faces.json: the examples src/shared/text/faces.test.ts reads too.
type table struct {
	Convert []struct {
		Marks    string            `json:"marks"`
		Markdown string            `json:"markdown"`
		Plain    string            `json:"plain"`
		Runs     []chapterfile.Run `json:"runs"`
	} `json:"convert"`
	Valid     []string `json:"valid"`
	Invalid   []string `json:"invalid"`
	Normalize []struct {
		Text string `json:"text"`
		Want string `json:"want"`
	} `json:"normalize"`
	Replace []struct {
		Text  string `json:"text"`
		Quote string `json:"quote"`
		With  string `json:"with"`
		Want  string `json:"want"`
	} `json:"replace"`
	Project []struct {
		Text     string `json:"text"`
		Plain    string `json:"plain"`
		Faceless string `json:"faceless"`
	} `json:"project"`
	Lenient []struct {
		Markdown string            `json:"markdown"`
		Runs     []chapterfile.Run `json:"runs"`
	} `json:"lenient"`
	Book []struct {
		Text string   `json:"text"`
		Book []string `json:"book"`
		Back string   `json:"back"`
	} `json:"book"`
}

func readTable(t *testing.T) table {
	t.Helper()
	raw, err := os.ReadFile("testdata/faces.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples table
	if err := json.Unmarshal(raw, &examples); err != nil || len(examples.Convert) == 0 {
		t.Fatalf("the table = %v", err)
	}
	return examples
}

// specs/2026-11-08-italic-bold-in-text.md TF1.1–TF1.3: a paragraph goes between the marks of the working text, the
// Markdown subset of the book and runs, there and back, and its plain text is its words without any mark.
func TestTheFacesGoBetweenMarksMarkdownAndRuns(t *testing.T) {
	for _, example := range readTable(t).Convert {
		if got := Runs(example.Marks); !reflect.DeepEqual(got, example.Runs) {
			t.Fatalf("Runs(%q) = %#v, want %#v", example.Marks, got, example.Runs)
		}
		if got := Marked(example.Runs); got != example.Marks {
			t.Fatalf("Marked(%#v) = %q, want %q", example.Runs, got, example.Marks)
		}
		if got := Markdown(example.Runs); got != example.Markdown {
			t.Fatalf("Markdown(%#v) = %q, want %q", example.Runs, got, example.Markdown)
		}
		if got := FromMarkdown(example.Markdown); !reflect.DeepEqual(got, example.Runs) {
			t.Fatalf("FromMarkdown(%q) = %#v, want %#v", example.Markdown, got, example.Runs)
		}
		if got := Plain(example.Marks).Text; got != example.Plain {
			t.Fatalf("Plain(%q) = %q, want %q", example.Marks, got, example.Plain)
		}
		if !Valid(example.Marks) {
			t.Fatalf("Valid(%q) = false", example.Marks)
		}
	}
}

// TF1.2: Markdown the book did not write itself — a star of a sum, a bold never closed — stays text.
func TestMarkdownWithoutItsPairStaysText(t *testing.T) {
	for _, example := range readTable(t).Lenient {
		if got := FromMarkdown(example.Markdown); !reflect.DeepEqual(got, example.Runs) {
			t.Fatalf("FromMarkdown(%q) = %#v, want %#v", example.Markdown, got, example.Runs)
		}
	}
}

// TF1.1: the marks of all three kinds make a tree; a face is not empty, not inside a face of its kind and not across a
// paragraph. A text without faces is valid as the marks ⟦…⟧ were.
func TestTheMarksAreATree(t *testing.T) {
	examples := readTable(t)
	for _, text := range examples.Valid {
		if !Valid(text) {
			t.Fatalf("Valid(%q) = false", text)
		}
	}
	for _, text := range examples.Invalid {
		if Valid(text) {
			t.Fatalf("Valid(%q) = true", text)
		}
	}
}

// TF1.1: the marks made whole — a face cut at the border of a block of the model's text, an empty one gone, two of one
// kind side by side one, a face across a paragraph ended with it; a text without faces stays to the byte.
func TestNormalizeMakesTheMarksWhole(t *testing.T) {
	for _, example := range readTable(t).Normalize {
		got := Normalize(example.Text)
		if got != example.Want {
			t.Fatalf("Normalize(%q) = %q, want %q", example.Text, got, example.Want)
		}
		if !Valid(got) {
			t.Fatalf("Normalize(%q) = %q is not valid", example.Text, got)
		}
		if Normalize(got) != got {
			t.Fatalf("Normalize(%q) moves again", got)
		}
	}
}

// TF2.2, TF2.3: a quote is found in the text without the marks of faces and its place stands in the text with the marks
// outside it: a place inside a face gives its face to the new words, a place across the border of a face shortens it,
// an empty face goes.
func TestAReplacedPlaceKeepsTheFacesAroundIt(t *testing.T) {
	for _, example := range readTable(t).Replace {
		view := Faceless(example.Text)
		at := strings.Index(view.Text, example.Quote)
		if at < 0 {
			t.Fatalf("%q is not in %q", example.Quote, view.Text)
		}
		start, end := view.Range(at, at+len(example.Quote))
		if got := Plain(example.Text[start:end]).Text; got != Plain(example.Quote).Text {
			t.Fatalf("the place of %q in %q is %q", example.Quote, example.Text, example.Text[start:end])
		}
		if got := Replace(example.Text, start, end, example.With); got != example.Want {
			t.Fatalf("Replace(%q, %q, %q) = %q, want %q", example.Text, example.Quote, example.With, got, example.Want)
		}
	}
}

// TF1.3, TF2.1: the plain text takes every mark off, the text without faces keeps ⟦…⟧; a text without the marks is its
// own projection.
func TestTheProjectionsOfAText(t *testing.T) {
	for _, example := range readTable(t).Project {
		if got := Plain(example.Text).Text; got != example.Plain {
			t.Fatalf("Plain(%q) = %q, want %q", example.Text, got, example.Plain)
		}
		if got := Faceless(example.Text).Text; got != example.Faceless {
			t.Fatalf("Faceless(%q) = %q, want %q", example.Text, got, example.Faceless)
		}
		view := Plain(example.Text)
		for at := 0; at < len(view.Text); at++ {
			if example.Text[view.At(at)] != view.Text[at] {
				t.Fatalf("Plain(%q).At(%d) = %d", example.Text, at, view.At(at))
			}
		}
		if view.At(len(view.Text)) != len(example.Text) {
			t.Fatalf("the end of Plain(%q) = %d", example.Text, view.At(len(view.Text)))
		}
	}
	if view := Faceless("без меток"); view.Text != "без меток" || view.At(3) != 3 {
		t.Fatalf("a text without faces = %#v", view)
	}
}

// TF1.5: the signs of the marks of faces that come as text — from a model, a file, a paste — are the brackets they look
// like; the marks of the model's text are left to their own rule.
func TestTheSignsOfFacesAsTextAreBrackets(t *testing.T) {
	if got := Literal("⟨а⟩ ⟪б⟫ ⟦в⟧"); got != "‹а› «б» ⟦в⟧" {
		t.Fatalf("Literal = %q", got)
	}
	if got := Marked([]chapterfile.Run{{Text: "⟨а⟩ ⟦б⟧", Italic: true}}); got != "⟨‹а› [б]⟩" {
		t.Fatalf("Marked of signs in a run = %q", got)
	}
}

// TF2.3: a piece of a text with the faces it cuts closed at its edges, a piece in the faces it stands in, and the marks
// it cuts named.
func TestAPieceIsWhole(t *testing.T) {
	if got := Whole("б⟩ в ⟪г"); got != "⟨б⟩ в ⟪г⟫" {
		t.Fatalf("Whole = %q", got)
	}
	closes, opens := Around("б⟩ в ⟪г")
	if closes != "⟩" || opens != "⟪" {
		t.Fatalf("Around = %q, %q", closes, opens)
	}
	if got := Piece("а ⟨б в⟩ г", len("а ⟨"), len("а ⟨б")); got != "⟨б⟩" {
		t.Fatalf("Piece = %q", got)
	}
	if got := Whole("без меток"); got != "без меток" {
		t.Fatalf("Whole of a plain text = %q", got)
	}
}

// TF1.1: whatever marks a text holds, its marks made whole are a tree of the same characters in the same faces, and
// they stay as they are when made whole again; the marks of the model's text do not move.
func TestNormalizeHoldsAnyText(t *testing.T) {
	alphabet := []string{"а", "б", " ", "\n", "⟨", "⟩", "⟪", "⟫", "⟦", "⟧"}
	seed := uint32(7)
	for range 20000 {
		var text strings.Builder
		written := false
		seed = seed*1103515245 + 12345
		for range int(seed>>16) % 14 {
			seed = seed*1103515245 + 12345
			piece := alphabet[(seed>>16)%uint32(len(alphabet))]
			// The marks of the model's text are valid on their own: Normalize keeps them as they are.
			switch {
			case piece == "⟦" && written, piece == "⟧" && !written:
				continue
			case piece == "⟦" || piece == "⟧":
				written = !written
			}
			text.WriteString(piece)
		}
		if written {
			text.WriteString("⟧")
		}
		got := Normalize(text.String())
		if !Valid(got) || Normalize(got) != got || Plain(got).Text != Plain(text.String()).Text {
			t.Fatalf("Normalize(%q) = %q", text.String(), got)
		}
		if strings.Map(keepWritten, got) != strings.Map(keepWritten, text.String()) {
			t.Fatalf("Normalize(%q) = %q moved a mark of the model's text", text.String(), got)
		}
		if runs := Runs(got); !reflect.DeepEqual(Runs(Marked(runs)), runs) {
			t.Fatalf("Runs(Marked(Runs(%q))) = %#v", got, Runs(Marked(runs)))
		}
		if runs := Runs(got); !reflect.DeepEqual(FromMarkdown(Markdown(runs)), runs) {
			t.Fatalf("FromMarkdown(Markdown(Runs(%q))) = %#v", got, FromMarkdown(Markdown(runs)))
		}
	}
}

// keepWritten keeps the marks of the model's text and the characters, without the marks of faces.
func keepWritten(r rune) rune {
	if IsFace(r) {
		return -1
	}
	return r
}
