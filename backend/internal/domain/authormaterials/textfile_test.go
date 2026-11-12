package authormaterials

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// windows1251Of is a text as Windows-1251 writes it: the letters of Russian, the rest as it is.
func windows1251Of(text string) []byte {
	var out []byte
	for _, char := range text {
		switch {
		case char >= 'А' && char <= 'я':
			out = append(out, byte(0xC0+char-'А'))
		case char == 'Ё':
			out = append(out, 0xA8)
		case char == 'ё':
			out = append(out, 0xB8)
		default:
			out = append(out, byte(char))
		}
	}
	return out
}

// specs/2026-11-10-ux8-memory-records.md MR2.4: a .csv is a table of one sheet; its separator is the one of its first line, its
// text UTF-8 or Windows-1251; the sheet has no name and is «Лист 1».
func TestACSVIsATableOfOneSheet(t *testing.T) {
	want := [][]string{{"Имя", "Вид", "Описание"}, {"Ворон", "персонаж", "Наёмник, бывший страж"}, {"Лиса", "персонаж", "Воровка"}}
	for name, data := range map[string][]byte{
		"commas":            []byte("Имя,Вид,Описание\nВорон,персонаж,\"Наёмник, бывший страж\"\nЛиса,персонаж,Воровка\n"),
		"semicolons":        []byte("Имя;Вид;Описание\r\nВорон;персонаж;Наёмник, бывший страж\r\nЛиса;персонаж;Воровка\r\n"),
		"tabs":              []byte("Имя\tВид\tОписание\nВорон\tперсонаж\tНаёмник, бывший страж\n\nЛиса\tперсонаж\tВоровка\n"),
		"a byte order mark": append([]byte("\xEF\xBB\xBF"), []byte("Имя;Вид;Описание\nВорон;персонаж;Наёмник, бывший страж\nЛиса;персонаж;Воровка\n")...),
		"Windows-1251":      windows1251Of("Имя;Вид;Описание\r\nВорон;персонаж;Наёмник, бывший страж\r\nЛиса;персонаж;Воровка\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			parsed, kind, err := Parse("Навыки и места.CSV", data)
			if err != nil || kind != SourceCSV || len(parsed.Sections) != 1 {
				t.Fatalf("Parse = %q, %+v, %v", kind, parsed, err)
			}
			section := parsed.Sections[0]
			if section.Title != "Лист 1" || !reflect.DeepEqual(texts(section.Rows), want) {
				t.Fatalf("section %q, rows %q", section.Title, texts(section.Rows))
			}
		})
	}
}

// MR2.4: a .txt and a .md are read as a pasted text, but within the limits of a file: a file is longer than a paste may be.
func TestATextFileIsReadAsAPastedText(t *testing.T) {
	for name, file := range map[string]struct {
		name string
		data []byte
	}{
		"a .txt":              {"мир.txt", []byte("Ворон\n\nНаёмник из Каэр-Морна.\n")},
		"a .md":               {"мир.md", []byte("# Ворон\r\nНаёмник из Каэр-Морна.\r\n")},
		"a .txt of 1251":      {"мир.txt", windows1251Of("Ворон\nНаёмник из Каэр-Морна.\n")},
		"a name of no suffix": {"мир", []byte("Ворон\nНаёмник из Каэр-Морна.\n")},
	} {
		t.Run(name, func(t *testing.T) {
			parsed, kind, err := Parse(file.name, file.data)
			if err != nil || kind != SourceText || len(parsed.Sections) != 1 || len(parsed.Sections[0].Paragraphs) != 2 {
				t.Fatalf("Parse = %q, %+v, %v", kind, parsed, err)
			}
			if last := parsed.Sections[0].Paragraphs[1].Text; last != "Наёмник из Каэр-Морна." {
				t.Fatalf("the text = %q", last)
			}
		})
	}
	line := strings.Repeat("Ворон — наёмник из Каэр-Морна. ", 4) + "\n"
	long := []byte(strings.Repeat(line, MaxTextRunes/len([]rune(line))+10))
	if _, err := ParseText(string(long)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a paste over its limit: %v", err)
	}
	if parsed, kind, err := Parse("мир.txt", long); err != nil || kind != SourceText || len(parsed.Sections[0].Paragraphs) == 0 {
		t.Fatalf("a file of the same text: %q, %v", kind, err)
	}
}

// MR2.4: an empty text file has nothing to read, and a table over the rows of an import is too large for it.
func TestATextFileOfNothingOrOfTooMuch(t *testing.T) {
	for name, file := range map[string]struct {
		name string
		data []byte
		err  error
	}{
		"blank lines":       {"мир.txt", []byte("\n \r\n\t\n"), ErrEmpty},
		"a table of commas": {"мир.csv", []byte(",,,\n, ,\n"), ErrEmpty},
		"too many rows":     {"мир.csv", []byte(strings.Repeat("Ворон;персонаж\n", maxRows+1)), ErrTooLarge},
		"too many columns":  {"мир.csv", []byte(strings.Repeat("Ворон;", maxCSVColumns+1) + "\n"), ErrTooLarge},
		"too many lines":    {"мир.txt", []byte(strings.Repeat("Ворон\n", maxParagraphs+1)), ErrTooLarge},
		"over the file":     {"мир.txt", make([]byte, MaxFileBytes+1), ErrTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse(file.name, file.data); !errors.Is(err, file.err) {
				t.Fatalf("Parse = %v, want %v", err, file.err)
			}
		})
	}
}

// MR3.4: a sheet without a name is named by its place, never by the names of its columns alone.
func TestASheetWithoutANameIsNamedByItsPlace(t *testing.T) {
	if got := sheetTitle("  ", 4); got != "Лист 5" {
		t.Fatalf("a sheet without a name = %q", got)
	}
	if got := sheetTitle(" Навыки ", 0); got != "Навыки" {
		t.Fatalf("a named sheet = %q", got)
	}
}
