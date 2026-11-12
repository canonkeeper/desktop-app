package library

import (
	"strings"
	"testing"
)

// specs/2026-11-10-book-file-kinds.md K2.3: one rule of a line that names a chapter, for Word without headings, a text,
// an RTF and an ODT.
func TestChapterTitleLine(t *testing.T) {
	for line, want := range map[string]bool{
		"Глава 12":                             true,
		"Глава XII. Имя":                       true,
		"ГЛАВА 618. Колизей":                   true,
		"глава iv":                             true,
		"Глава 3: Ночь":                        true,
		"Глава 7 — Дорога":                     true,
		"Пролог":                               true,
		"Эпилог: десять лет спустя":            true,
		"Интерлюдия":                           true,
		"Глава гильдии кивнул.":                false,
		"Главами книги стали дни.":             false,
		"Прологом к войне стал пир.":           false,
		"Он открыл главу 3 и читал.":           false,
		"Глава 3\nс переносом":                 false,
		"Глава\t3":                             false,
		"Глава 1. " + strings.Repeat("я", 120): false,
		"": false,
	} {
		if got := chapterTitleLine(line); got != want {
			t.Errorf("chapterTitleLine(%q) = %v, want %v", line, got, want)
		}
	}
}
