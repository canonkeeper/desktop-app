// SPDX-License-Identifier: AGPL-3.0-only

package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// specs/2026-11-13-desk-obsidian.md OB4.2: a volume is checked as the queue checks it when its file comes — the same
// refusal code the queue keeps — and a ready one is read into the book the import Job makes of it.
func TestAVolumeIsCheckedAndReadAsTheQueueReadsIt(t *testing.T) {
	text := "# Северный предел\n\n# Застава\n\nСторожевая башня стояла на холме.\n\n# Переправа\n\nПаром ждал у берега.\n"
	volume, err := CheckQueueVolume("Северный предел.md", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if volume.Refusal != "" || volume.Book.Title != "Северный предел" {
		t.Fatalf("volume = %+v", volume)
	}
	titles := make([]string, 0, len(volume.Book.Chapters))
	for _, chapter := range volume.Book.Chapters {
		titles = append(titles, chapter.Title)
	}
	if !slices.Equal(titles, []string{"Застава", "Переправа"}) || !slices.Equal(volume.Book.Chapters[1].Paragraphs, []string{"Паром ждал у берега."}) {
		t.Fatalf("chapters = %+v", volume.Book.Chapters)
	}

	// A refused volume names the code the queue keeps for the same file.
	var many strings.Builder
	for chapter := 1; chapter <= maxImportedChapters+1; chapter++ {
		fmt.Fprintf(&many, "# Глава %d\n\nТекст.\n\n", chapter)
	}
	refused, err := CheckQueueVolume("Много.md", []byte(many.String()))
	if err != nil {
		t.Fatal(err)
	}
	status, failure, err := preflightImportQueueItem("Много.md", "", []byte(many.String()), sha256Hex([]byte(many.String())))
	if err != nil || status != ImportQueueItemInvalid {
		t.Fatalf("preflight = %q %v", status, err)
	}
	var kept struct{ Code string }
	if err := json.Unmarshal(failure, &kept); err != nil || refused.Refusal != kept.Code || refused.Refusal != "import.too_many_chapters" {
		t.Fatalf("refusal = %q, the queue keeps %q", refused.Refusal, kept.Code)
	}
	if empty, err := CheckQueueVolume("Пусто.md", []byte("\n\n")); err != nil || empty.Refusal != "empty_import_content" {
		t.Fatalf("empty = %+v %v", empty, err)
	}
	if _, err := CheckQueueVolume("Большая.md", bytes.Repeat([]byte("а"), int(MaxImportFileSizeBytes)+1)); !errors.Is(err, ErrImportFileTooLarge) {
		t.Fatalf("too large = %v", err)
	}
}

// specs/2026-11-13-desk-obsidian.md OB3.4: a line of Markdown is its plain words as the import of a book reads them.
func TestALineOfMarkdownIsItsPlainWords(t *testing.T) {
	text := "## Внешность\n> **Мирон** — [[Мирон Сватов|Мирон]] ![[мирон.png]] и *Ольга*\n\n- [[Застава]]\n```\nкод\n```\n[a]: https://example.test\n"
	got := MarkdownPlainLines(text)
	want := []string{"Внешность", "Мирон — Мирон и Ольга", "", "Застава", "код"}
	if !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}
