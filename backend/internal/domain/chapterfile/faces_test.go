package chapterfile

import (
	"reflect"
	"testing"
)

// facesStyles are the styles of a Word file whose faces come from its styles: a character style «Emphasis» on a chain
// of basedOn, a character style that turns its base off, a paragraph style in italic on a chain, a default paragraph
// style and bold in the defaults of the document.
const facesStyles = `<w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="24"/></w:rPr></w:rPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="a"><w:name w:val="Normal"/></w:style>` +
	`<w:style w:type="character" w:styleId="Base"><w:name w:val="Base"/><w:rPr><w:i/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="Emph"><w:name w:val="Emphasis"/><w:basedOn w:val="Base"/><w:rPr><w:sz w:val="26"/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="Plain"><w:name w:val="Plain"/><w:basedOn w:val="Base"/><w:rPr><w:i w:val="0"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="QuoteBase"><w:name w:val="Quote Base"/><w:pPr><w:ind w:left="567"/></w:pPr><w:rPr><w:i/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="QuoteBase"/></w:style>` +
	`<w:style w:type="character" w:styleId="Loop1"><w:name w:val="Loop 1"/><w:basedOn w:val="Loop2"/></w:style>` +
	`<w:style w:type="character" w:styleId="Loop2"><w:name w:val="Loop 2"/><w:basedOn w:val="Loop1"/></w:style>`

// specs/2026-11-08-italic-bold-in-text.md TF4.2: the face of a run of a Word file is the one Word shows — its own w:i and
// w:b, else its character style with its chain of basedOn, else the style of its paragraph, else the defaults of the
// document; the nearest value set wins. The properties of a run stay as they stood (PR1.7).
func TestTheFaceOfAWordRunIsTheOneWordShows(t *testing.T) {
	body := `<w:p><w:r><w:t xml:space="preserve">Кира шла </w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Emph"/></w:rPr><w:t>медленно</w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Emph"/><w:i w:val="0"/></w:rPr><w:t xml:space="preserve"> и </w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Plain"/></w:rPr><w:t>тихо</w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Loop1"/></w:rPr><w:t>.</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr><w:r><w:t xml:space="preserve">Цитата </w:t></w:r>` +
		`<w:r><w:rPr><w:rStyle w:val="Plain"/></w:rPr><w:t>прямо</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve"> и жирно</w:t></w:r></w:p>`
	document, err := Parse("Глава.docx", docxOf(t, body, facesStyles))
	if err != nil {
		t.Fatal(err)
	}
	want := []Paragraph{
		{Runs: []Run{{Text: "Кира шла "}, {Text: "медленно", Italic: true, Props: `<w:rStyle w:val="Emph"/>`},
			{Text: " и ", Props: `<w:rStyle w:val="Emph"/><w:i w:val="0"/>`}, {Text: "тихо", Props: `<w:rStyle w:val="Plain"/>`},
			{Text: ".", Props: `<w:rStyle w:val="Loop1"/>`}}},
		{Runs: []Run{{Text: "Цитата ", Italic: true}, {Text: "прямо", Props: `<w:rStyle w:val="Plain"/>`},
			{Text: " и жирно", Italic: true, Bold: true, Props: `<w:b/>`}}, Props: `<w:pStyle w:val="Quote"/>`},
	}
	if !reflect.DeepEqual(document.Paragraphs, want) {
		t.Fatalf("paragraphs:\n%#v\nwant\n%#v", document.Paragraphs, want)
	}

	defaults := `<w:docDefaults><w:rPrDefault><w:rPr><w:b/></w:rPr></w:rPrDefault></w:docDefaults>`
	bold, err := Parse("Глава.docx", docxOf(t, `<w:p><w:r><w:t xml:space="preserve">Весь </w:t></w:r><w:r><w:rPr><w:b w:val="false"/></w:rPr><w:t>текст</w:t></w:r></w:p>`, defaults))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Run{{Text: "Весь ", Bold: true}, {Text: "текст", Props: `<w:b w:val="false"/>`}}; !reflect.DeepEqual(bold.Paragraphs[0].Runs, want) {
		t.Fatalf("the defaults of the document: %#v", bold.Paragraphs[0].Runs)
	}
}
