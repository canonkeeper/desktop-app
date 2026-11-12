package library

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// rtfHexForTest writes a text as RTF writes it in a code page: a sign over ASCII as \'hh.
func rtfHexForTest(t *testing.T, text string, page *charmap.Charmap) string {
	t.Helper()
	encoded, err := page.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, b := range encoded {
		if b >= 0x80 {
			fmt.Fprintf(&out, `\'%02x`, b)
		} else {
			out.WriteByte(b)
		}
	}
	return out.String()
}

// rtfUnicodeForTest writes a text as \uN with its fallback after each sign over ASCII, or a space that ends the word.
func rtfUnicodeForTest(text, fallback string) string {
	var out strings.Builder
	for _, r := range text {
		if r < 0x80 {
			out.WriteRune(r)
			continue
		}
		value := int(r)
		if value > 32767 {
			value -= 65536
		}
		fmt.Fprintf(&out, `\u%d%s`, value, fallback)
		if fallback == "" {
			// Without a fallback the space after the number ends the word.
			out.WriteByte(' ')
		}
	}
	return out.String()
}

func parseRTFForTest(t *testing.T, document string) BookImport {
	t.Helper()
	book, err := ParseRTF([]byte(document))
	if err != nil {
		t.Fatalf("ParseRTF() error = %v", err)
	}
	return book
}

// K3.1: \'hh in the code page of the document — 1251 when it names none — or of the charset of its font, \uN with its
// fallback skipped by \ucN, a pair of halves as one sign.
func TestParseRTFReadsCyrillicInItsCodePages(t *testing.T) {
	win := func(text string) string { return rtfHexForTest(t, text, charmap.Windows1251) }
	document := `{\rtf1\ansi{\fonttbl{\f0\froman Times New Roman;}{\f1\fswiss\fcharset204 Arial;}}` +
		`\pard ` + win("Ветер нёс снег.") + `\par ` +
		`\uc1 ` + rtfUnicodeForTest("Ёрм смотрел на север.", "?") + `\par ` +
		`{\uc0 ` + rtfUnicodeForTest("Мы дойдём.", "") + `}\par ` +
		`\u-10179?\u-8704? ` + rtfUnicodeForTest("знак вне плоскости.", "?") + `\par}`
	assertOutline(t, parseRTFForTest(t, document),
		"# ", "## ", "Ветер нёс снег.", "Ёрм смотрел на север.", "Мы дойдём.", "\U0001F600 знак вне плоскости.", "(unsplit)")

	// A document of the code page 1252 whose Cyrillic is in a font of the charset 204.
	latin := `{\rtf1\ansi\ansicpg1252{\fonttbl{\f0 Calibri;}{\f1\fcharset204 Calibri;}}\pard\f0 Caf\'e9 {\f1 ` + win("и кофе") + `}.\par}`
	assertOutline(t, parseRTFForTest(t, latin), "# ", "## ", "Café и кофе.", "(unsplit)")
	koi := `{\rtf1\ansi\ansicpg20866 ` + rtfHexForTest(t, "Пламя", charmap.KOI8R) + `\par}`
	assertOutline(t, parseRTFForTest(t, koi), "# ", "## ", "Пламя", "(unsplit)")
}

// K3.1: italic and bold hold in their group and go with \b0, \i0 and \plain; the tables of the document, its
// information, a picture, a header, a footnote, the code of a field, hidden text and every {\*…} are not the book.
func TestParseRTFReadsFacesAndLeavesOutWhatIsNotTheBook(t *testing.T) {
	win := func(text string) string { return rtfHexForTest(t, text, charmap.Windows1251) }
	document := `{\rtf1\ansi\ansicpg1251\deff0{\fonttbl{\f0\fnil\fcharset204 Times New Roman;}}` +
		`{\colortbl;\red0\green0\blue0;}{\stylesheet{\s0 Normal;}}{\*\generator Riched20 10.0;}` +
		`{\info{\title ` + win("Заголовок свойств") + `}{\author ` + win("Автор") + `}}` +
		`{\header\pard ` + win("Колонтитул") + `\par}{\footer ` + win("Страница") + `\par}` +
		`\pard ` + win("Корабль ") + `{\i ` + win("вошёл") + `} ` + win("в ") + `{\b ` + win("гавань") + `\b0  ` + win("на рассвете") + `}.` +
		`{\footnote\pard ` + win("Сноска не в тексте.") + `\par}` +
		`{\pict\pngblip 89504e470d0a1a0a}{\*\shppict{\pict 00ff}}{\object{\*\objdata 0102}{\result ` + win("Объект") + `}}` +
		`{\v ` + win("скрытое") + `}` +
		`\par\pard\i ` + win("Курсив до ") + `\plain ` + win("простого.") + `\line ` + win("Перенос") + `\tab ` + win("и табуляция") + `\emdash !\par ` +
		`{\field{\*\fldinst HYPERLINK "https://example.test"}{\fldrslt ` + win("ссылка") + `}}\par` +
		`{\*\bkmkstart mark}{\*\bkmkend mark}\bin4 {}{} \par}`
	assertOutline(t, parseRTFForTest(t, document),
		"# ", "## ",
		"Корабль *вошёл* в **гавань** на рассвете.",
		"*Курсив до* простого.\nПеренос\tи табуляция—!",
		"ссылка",
		"(unsplit)")
}

// K3.2: chapters start at the lines that name them, or at the paragraphs of the first level of outline — by
// \outlinelevel0 or by a style named «heading 1» or «Заголовок 1» in the table of styles; the first short line names
// the book.
func TestParseRTFFindsChapters(t *testing.T) {
	win := func(text string) string { return rtfHexForTest(t, text, charmap.Windows1251) }
	byLines := `{\rtf1\ansi ` + win("Северный ветер") + `\par\par ` + win("Глава 1") + `\par ` + win("Корабль вошёл в гавань.") +
		`\par ` + win("Глава 2. Шторм") + `\par ` + win("Ночью начался шторм.") + `\par}`
	assertOutline(t, parseRTFForTest(t, byLines),
		"# Северный ветер", "## Глава 1", "Корабль вошёл в гавань.", "## Глава 2. Шторм", "Ночью начался шторм.")
	byStyles := `{\rtf1\ansi{\stylesheet{\s0 Normal;}{\s1\sbasedon0\snext0 ` + win("Заголовок 1") + `;}{\s2\outlinelevel0 Part;}` +
		`{\*\cs10 Default Paragraph Font;}}` +
		`\pard\plain\s1 ` + win("Утро") + `\par\pard\plain ` + win("Корабль вошёл в гавань.") + `\par` +
		`\pard\plain\s2 ` + win("Вечер") + `\par\pard\plain ` + win("Ночью начался шторм.") + `\par` +
		`\pard\plain\outlinelevel0 ` + win("Ночь") + `\par\pard\plain ` + win("Всё стихло.") + `\par}`
	assertOutline(t, parseRTFForTest(t, byStyles),
		"# ", "## Утро", "Корабль вошёл в гавань.", "## Вечер", "Ночью начался шторм.", "## Ночь", "Всё стихло.")
}

// K3.3: a document without {\rtf, with a group left open or nested too deep, or with a broken sign is invalid; the limits
// of the book hold.
func TestParseRTFRefusesABrokenDocument(t *testing.T) {
	for name, document := range map[string]string{
		"no rtf":        `{\pict 00}`,
		"left open":     `{\rtf1\ansi Text\par`,
		"too deep":      `{\rtf1 ` + strings.Repeat("{", maxImportXMLDepth+1) + "x" + strings.Repeat("}", maxImportXMLDepth+2),
		"bad sign":      `{\rtf1\ansi \'zz\par}`,
		"ends in slash": `{\rtf1\ansi \`,
	} {
		_, err := ParseRTF([]byte(document))
		if !errors.Is(err, errRTFDocument) {
			t.Fatalf("%s: ParseRTF() error = %v, want an invalid document", name, err)
		}
		if failure := importParseFailure(err, ".rtf"); failure.Code != "invalid_rtf_document" {
			t.Fatalf("%s: failure = %q", name, failure.Code)
		}
	}
	if _, err := ParseRTF([]byte(`{\rtf1\ansi{\fonttbl{\f0 Arial;}}}`)); !errors.Is(err, ErrImportNoContent) {
		t.Fatalf("a document without text error = %v", err)
	}
	var chapters strings.Builder
	chapters.WriteString(`{\rtf1\ansi `)
	for number := 1; number <= maxImportedChapters+1; number++ {
		fmt.Fprintf(&chapters, `\'c3\'eb\'e0\'e2\'e0 %d\par Text.\par `, number)
	}
	chapters.WriteString("}")
	if _, err := ParseRTF([]byte(chapters.String())); !errors.Is(err, ErrImportTooManyChapters) {
		t.Fatalf("too many chapters error = %v", err)
	}
}
