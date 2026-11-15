// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/obsidian"
)

// volumes read a glued book with the code of the queue, as the build does.
type volumes struct{}

func (volumes) Check(fileName string, content []byte) service.Volume {
	volume, err := library.CheckQueueVolume(fileName, content)
	if err != nil {
		return service.Volume{Refusal: "request.validation"}
	}
	if volume.Refusal != "" {
		return service.Volume{Refusal: volume.Refusal}
	}
	var chapters []service.VolumeChapter
	for _, chapter := range volume.Book.Chapters {
		characters := 0
		for _, paragraph := range chapter.Paragraphs {
			characters += utf8.RuneCountInString(paragraph)
		}
		chapters = append(chapters, service.VolumeChapter{
			Title: chapter.Title, Characters: characters, Paragraphs: chapter.Paragraphs, Markdown: chapter.Markdown,
		})
	}
	return service.Volume{Title: volume.Book.Title, Chapters: chapters}
}

// saga is the saga of a test: the names its book knows and its reading.
type saga struct {
	book map[string]string
}

func (saga saga) BookNames(_ context.Context, _ string, keys []string) (map[string]string, error) {
	names := map[string]string{}
	for _, key := range keys {
		if name, ok := saga.book[key]; ok {
			names[key] = name
		}
	}
	return names, nil
}

func (saga saga) Reading(context.Context, string) (service.Reading, error) {
	return service.Reading{Helper: service.HelperOwn, Consent: true}, nil
}

// links are the links of the books a test names and the records of its saga with the links of their notes.
type links struct {
	books []service.BookFolder
	notes obsidian.NoteFacts
}

func (links links) Books(context.Context, string) ([]service.BookFolder, error) {
	return links.books, nil
}
func (links links) Notes(context.Context, string, []string, []string) (obsidian.NoteFacts, error) {
	return links.notes, nil
}

// plans say a book changes when the test names it: the plan of «Обновить из файла» of the build.
type plans map[string]bool

func (plans plans) Changes(_ context.Context, _, bookID string, chapters []service.VolumeChapter) (bool, error) {
	if len(chapters) == 0 {
		return false, errors.New("no chapters to plan by")
	}
	return plans[bookID], nil
}

func previewOf(vault string, links service.Links, files service.Files) service.PreviewImport {
	return service.PreviewImport{
		Vaults: &vaults{paths: map[string]string{"saga": vault}}, Files: files, Links: links, Plans: plans{},
		Saga:    saga{book: map[string]string{"ольга": "Ольга", "мирон": "Мирон"}},
		Volumes: volumes{}, Plain: library.MarkdownPlainLines, Gate: service.NewGate(), Limits: service.DefaultLimits,
	}
}

func run(t *testing.T, preview service.PreviewImport, choice obsidian.Choice) (service.Preview, error) {
	t.Helper()
	return preview.Execute(context.Background(), service.PreviewInput{ActorUserID: "author", ProjectID: "saga", Choice: choice})
}

func titlesOf(book service.BookPreview) []string {
	var titles []string
	for _, chapter := range book.Chapters {
		titles = append(titles, chapter.Title)
	}
	return titles
}

func warning(book service.BookPreview, code obsidian.Warning) *service.BookWarning {
	for index := range book.Warnings {
		if book.Warnings[index].Code == code {
			return &book.Warnings[index]
		}
	}
	return nil
}

// specs/2026-11-13-desk-obsidian.md OB4.1–OB4.4: the preview of the vault of the example — each book as the import reads
// its glued text, with the warnings of the notes; a folder of materials inside a book and a folder left out are not in
// it; each record with its state; the counts of the reading and the consent of the saga.
func TestThePreviewOfTheVaultOfTheExample(t *testing.T) {
	vault := filepath.Join(t.TempDir(), "Северный предел")
	long := "Книга 4"
	for name, text := range map[string]string{
		"Книга 1/_book.md":             "---\nbook: Застава\n---\n[[02 Переправа]]\n[[01 Пролог]]\n",
		"Книга 1/01 Пролог.md":         "Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md":      "Мирон ждал у переправы.\n\n## Интерлюдия\n\nОльга смотрела с башни.",
		"Книга 1/Персонажи/Ольга.md":   "Старшая на заставе.",
		"Книга 1/Черновики/Отрывок.md": "Отрывок без места.",
		"Книга 1/Карта.excalidraw.md":  "рисунок",
		"Книга 2/Том.md":               "# Белый порт\n\nСудно вошло в порт.\n\n# Чёрный берег\n\nБерег был пуст.",
		"Книга 3/Весна.md":             "Весна пришла поздно.",
		"Книга 3/Осень.md":             "Осень пришла рано.",
		long + "/_book.md":             "---\nbook: " + strings.Repeat("Очень длинное название тома ", 6) + "\n---\n",
		long + "/Глава.md":             "Текст главы.",
		"Персонажи/Ольга Сватова.md":   "---\ntitle: Ольга\naliases: [Ольга Сватова]\n---\nСтаршая на заставе.",
		"Персонажи/Мирон Сватов.md":    "---\naliases: [Мирон]\n---\nСтарший брат.",
		"Персонажи/Новый.md":           "---\ntype: персонаж\n---\nНикто его не знает.",
		"Персонажи/Повтор.md":          "---\ntitle: Новый\n---\nТот же герой.",
		"Персонажи/Семья.md":           "## Мирон\n\nБрат.\n\n## Ольга\n\nСестра.",
		"Места/Перевал.md":             "Закрыт зимой.",
		"Места/Все места.md":           "- [[Перевал]]\n- [[Застава]]\n- [[Порт]]\n",
		"Места/Связанная.md":           "Уже загружена.",
		"Места/Правленная.md":          "Правлена в приложении.",
	} {
		write(t, vault, name, text)
	}
	preview := previewOf(vault, links{
		books: []service.BookFolder{{Folder: "Книга 3", BookID: "b-3", BookTitle: "Книга 3", InTrash: true}},
		notes: obsidian.NoteFacts{Records: []obsidian.HeldRecord{
			{ID: "r-1", Key: "перевал", Revision: 1},
			{ID: "r-2", Key: "связанная", Revision: 1, Note: "Места/Связанная.md", LinkRevision: 1},
			{ID: "r-3", Key: "правленная", Revision: 2, Note: "Места/Правленная.md", LinkRevision: 1},
		}, Total: 3},
	}, obsidianvault.Files{})
	result, err := run(t, preview, obsidian.Choice{
		Books:     []string{"Книга 1", "Книга 2", "Книга 3", long},
		Materials: []obsidian.MaterialFolder{{Path: "Персонажи", Kind: authormaterials.KindCharacter}, {Path: "Места", Kind: authormaterials.KindPlace}, {Path: "Книга 1/Персонажи", Kind: authormaterials.KindCharacter}},
		Exclude:   []string{"Книга 1/Черновики"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second, third, fourth := result.Books[0], result.Books[1], result.Books[2], result.Books[3]
	if first.Title != "Застава" || !slices.Equal(titlesOf(first), []string{"Переправа", "Интерлюдия", "Пролог"}) ||
		first.Notes[0].OrderBy != obsidian.OrderIndex || first.Refusal != "" || first.Skipped[obsidian.SkipService] != 1 {
		t.Fatalf("the first book = %+v", first)
	}
	if differ := warning(first, obsidian.WarningChaptersDiffer); differ == nil || !slices.Equal(differ.Notes, []string{"Книга 1/02 Переправа.md"}) {
		t.Fatalf("the split note = %+v", first.Warnings)
	}
	for _, note := range first.Notes {
		if strings.Contains(note.Path, "Черновики") || strings.Contains(note.Path, "Персонажи") {
			t.Fatalf("the book holds %s", note.Path)
		}
	}
	if !slices.Equal(titlesOf(second), []string{"Белый порт", "Чёрный берег"}) || warning(second, obsidian.WarningBookInNote) == nil {
		t.Fatalf("the book in a note = %+v", second)
	}
	// specs/2026-11-14-desk-obsidian-books.md OK0.4: a book in «Недавно удалённые» is not compared — its folder loads nothing.
	if warning(third, obsidian.WarningOrderByName) == nil || !third.State.Linked || third.State.Changed || third.State.BookID != "b-3" ||
		!third.State.InTrash || first.State.InTrash {
		t.Fatalf("the book ordered by names = %+v", third)
	}
	if warning(fourth, obsidian.WarningTitleCut) == nil || utf8.RuneCountInString(fourth.Title) > obsidian.MaxTitleRunes {
		t.Fatalf("the long title = %+v", fourth)
	}
	if result.NewChapters != len(first.Chapters)+len(second.Chapters)+len(fourth.Chapters) ||
		result.NewCharacters != first.Characters+second.Characters+fourth.Characters || result.NewCharacters == 0 {
		t.Fatalf("the reading = %d chapters %d signs", result.NewChapters, result.NewCharacters)
	}
	if result.Reading != (service.Reading{Helper: service.HelperOwn, Consent: true}) {
		t.Fatalf("the reading of the saga = %+v", result.Reading)
	}

	states := map[string]string{}
	warnings := map[string][]obsidian.Warning{}
	for _, record := range result.Materials.Records {
		states[record.Path] = record.State
		warnings[record.Path] = record.Warnings
	}
	want := map[string]string{
		"Персонажи/Ольга Сватова.md": obsidian.StateOtherSource, "Персонажи/Мирон Сватов.md": obsidian.StateInBookByAlias,
		"Персонажи/Новый.md": obsidian.StateNew, "Персонажи/Повтор.md": obsidian.StateOtherSource,
		"Персонажи/Семья.md": obsidian.StateNew, "Места/Перевал.md": obsidian.StateOtherSource, "Места/Все места.md": obsidian.StateNew,
		"Места/Связанная.md": obsidian.StateSameNote, "Места/Правленная.md": obsidian.StateEditedInApp,
		// specs/2026-11-15-desk-obsidian-notes.md ON1.2: the name «Ольга» goes to the first of its notes by the order of the
		// paths, whatever folder the choice names first.
		"Книга 1/Персонажи/Ольга.md": obsidian.StateInBook,
	}
	if fmt.Sprint(states) != fmt.Sprint(want) {
		t.Fatalf("the states = %v", states)
	}
	if !slices.Contains(warnings["Места/Все места.md"], obsidian.WarningListNote) || !slices.Contains(warnings["Персонажи/Семья.md"], obsidian.WarningSeveralSections) {
		t.Fatalf("the warnings of the records = %v", warnings)
	}
	again, err := run(t, preview, obsidian.Choice{
		Books:     []string{"Книга 1", "Книга 2", "Книга 3", long},
		Materials: []obsidian.MaterialFolder{{Path: "Книга 1/Персонажи", Kind: authormaterials.KindCharacter}, {Path: "Места", Kind: authormaterials.KindPlace}, {Path: "Персонажи", Kind: authormaterials.KindCharacter}},
		Exclude:   []string{"Книга 1/Черновики"},
	})
	prints := func(preview service.Preview) map[string]string {
		byFolder := map[string]string{}
		for _, folder := range preview.Materials.Folders {
			byFolder[folder.Folder] = folder.Fingerprint
		}
		return byFolder
	}
	if err != nil || again.Books[0].Fingerprint != first.Fingerprint || fmt.Sprint(prints(again)) != fmt.Sprint(prints(result)) {
		t.Fatalf("the fingerprints of another order of the choice: %v", err)
	}
	for _, record := range again.Materials.Records {
		if states[record.Path] != record.State {
			t.Fatalf("another order of the choice gives %s the state %s", record.Path, record.State)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.3, OB4.1: the choice is checked before a note is read — books one in another, a
// book in a folder linked to a book, a path of no folder of the vault or one through a link —; a choice of more notes than
// a preview reads is obsidian.too_large with its numbers; a note that does not answer in its time is obsidian.slow.
func TestTheChoiceOfAPreviewIsCheckedBeforeItIsRead(t *testing.T) {
	vault := t.TempDir()
	write(t, vault, "Сага/Книга 1/Глава.md", "Текст.")
	if err := os.Symlink(filepath.Join(vault, "Сага"), filepath.Join(vault, "Ссылка")); err != nil {
		t.Fatal(err)
	}
	preview := previewOf(vault, links{books: []service.BookFolder{{Folder: "Связанная/Книга", BookID: "b"}}}, obsidianvault.Files{})
	var overlap obsidian.Overlap
	for _, choice := range []obsidian.Choice{
		{Books: []string{"Сага", "Сага/Книга 1"}},
		{Books: []string{"Связанная"}},
		{Books: []string{"Связанная/Книга/Часть"}},
	} {
		if _, err := run(t, preview, choice); !errors.As(err, &overlap) {
			t.Fatalf("%v = %v", choice.Books, err)
		}
	}
	for _, folder := range []string{"../x", "/etc", "C:/x", ".trash/x", "Ссылка", "Нет"} {
		if _, err := run(t, preview, obsidian.Choice{Books: []string{folder}}); !errors.Is(err, obsidian.ErrPath) {
			t.Fatalf("%s = %v", folder, err)
		}
	}

	big := t.TempDir()
	for index := range obsidian.MaxNotes + 1 {
		write(t, big, fmt.Sprintf("Книга/%05d.md", index), "x")
	}
	var tooLarge obsidian.TooLarge
	if _, err := run(t, previewOf(big, service.NoLinks{}, obsidianvault.Files{}), obsidian.Choice{Books: []string{"Книга"}}); !errors.As(err, &tooLarge) ||
		tooLarge.Notes != obsidian.MaxNotes+1 {
		t.Fatalf("a choice too large = %v %+v", err, tooLarge)
	}

	// 200 MB of notes: the files are as large as a note may be, and empty on the disk.
	heavy := t.TempDir()
	for index := range obsidian.MaxSelectedBytes/obsidian.MaxNoteBytes + 1 {
		write(t, heavy, fmt.Sprintf("Книга/%02d.md", index), "")
		if err := os.Truncate(filepath.Join(heavy, "Книга", fmt.Sprintf("%02d.md", index)), obsidian.MaxNoteBytes); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, previewOf(heavy, service.NoLinks{}, obsidianvault.Files{}), obsidian.Choice{Books: []string{"Книга"}}); !errors.As(err, &tooLarge) ||
		tooLarge.Bytes <= obsidian.MaxSelectedBytes || tooLarge.Notes != obsidian.MaxSelectedBytes/obsidian.MaxNoteBytes+1 {
		t.Fatalf("a choice of too many bytes = %v %+v", err, tooLarge)
	}

	// More entries than a look walks: the preview is refused with the count, not a book cut short.
	crowded := previewOf("/vault/crowded", service.NoLinks{}, crowd{})
	if _, err := run(t, crowded, obsidian.Choice{Books: []string{"Книга"}}); !errors.As(err, &tooLarge) || tooLarge.Entries <= obsidian.MaxEntries {
		t.Fatalf("a choice of too many entries = %v %+v", err, tooLarge)
	}

	stuck := previewOf(vault, service.NoLinks{}, hangingNotes{Files: obsidianvault.Files{}, release: make(chan struct{})})
	defer close(stuck.Files.(hangingNotes).release)
	stuck.Limits.Note = 50 * time.Millisecond
	started := time.Now()
	if _, err := run(t, stuck, obsidian.Choice{Books: []string{"Сага"}}); !errors.Is(err, service.ErrSlow) || time.Since(started) > 5*time.Second {
		t.Fatalf("a note that does not answer = %v after %s", err, time.Since(started))
	}
}

// hangingNotes are the files of a vault whose notes never answer.
type hangingNotes struct {
	obsidianvault.Files
	release chan struct{}
}

func (files hangingNotes) Open(vaultPath string) (service.Vault, error) {
	vault, err := files.Files.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	return hangingVault{Vault: vault, release: files.release}, nil
}

type hangingVault struct {
	service.Vault
	release chan struct{}
}

func (vault hangingVault) Read(context.Context, string) ([]byte, obsidian.Skip) {
	<-vault.release
	return nil, obsidian.SkipUnreadable
}

// crowd is a vault whose chosen folder holds more entries than a look walks: pictures, no note.
type crowd struct{}

func (crowd) Open(string) (service.Vault, error)   { return crowd{}, nil }
func (crowd) Folder(context.Context, string) error { return nil }
func (crowd) Close() error                         { return nil }
func (crowd) Read(context.Context, string) ([]byte, obsidian.Skip) {
	return nil, obsidian.SkipUnreadable
}
func (crowd) Walk(_ context.Context, folder string, visit service.Visit) error {
	for index := range obsidian.MaxEntries + 10 {
		if _, stop := visit(folder, obsidian.Found{Name: fmt.Sprintf("%06d.png", index), Regular: true}); stop {
			return nil
		}
	}
	return nil
}

// specs/2026-11-13-desk-obsidian.md OB3.3, OB4.1, OB4.2: a note belongs to the deepest chosen folder that holds it
// (obsidian.Choice.Owner) — a book or materials chosen inside a folder left out are read, a folder left out inside them is
// not —; a folder named with a space at the edge of its name is that folder, not its neighbour; a note deeper than a look
// goes is read all the same.
func TestTheNotesOfAChoiceAreTheNotesItOwns(t *testing.T) {
	vault := filepath.Join(t.TempDir(), "Северный предел")
	deep := "Глубоко/" + strings.Repeat("Ещё/", obsidian.MaxDepth+8) + "02 Дно.md"
	for name, text := range map[string]string{
		"Черновики/Книга/01 Застава.md":     "Ветер с перевала нёс снег.",
		"Черновики/Книга/Старое/Отрывок.md": "Старый отрывок.",
		"Черновики/Персонажи/Ольга.md":      "Старшая на заставе.",
		"Черновики/Отрывок.md":              "Отрывок без места.",
		"Книга 1 /01 Застава.md":            "Застава с пробелом.",
		"Книга 1/01 Другое.md":              "Другая папка.",
		"Глубоко/01 Начало.md":              "Начало.",
		deep:                                "Дно.",
		"Книга `один`/01 Застава.md":        "Застава.",
	} {
		write(t, vault, name, text)
	}
	preview := previewOf(vault, service.NoLinks{}, obsidianvault.Files{})
	result, err := run(t, preview, obsidian.Choice{
		Books:     []string{"Черновики/Книга", "Книга 1 ", "Глубоко", "Книга `один`"},
		Materials: []obsidian.MaterialFolder{{Path: "Черновики/Персонажи", Kind: authormaterials.KindCharacter}},
		Exclude:   []string{"Черновики", "Черновики/Книга/Старое"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if book := result.Books[0]; !slices.Equal(titlesOf(book), []string{"Застава"}) || book.Refusal != "" {
		t.Fatalf("a book inside a folder left out = %+v", book)
	}
	if book := result.Books[1]; book.Folder != "Книга 1 " || !slices.Equal(titlesOf(book), []string{"Застава"}) {
		t.Fatalf("a folder with a space at the edge = %+v", book)
	}
	if book := result.Books[2]; !slices.Equal(titlesOf(book), []string{"Начало", "Дно"}) {
		t.Fatalf("a note deeper than a look = %q", titlesOf(book))
	}
	// The book is named as the import names it: a code of its name loses its backticks.
	if book := result.Books[3]; book.Title != "Книга один" {
		t.Fatalf("the title of a book = %q", book.Title)
	}
	if records := result.Materials.Records; len(records) != 1 || records[0].Draft.Name != "Ольга" {
		t.Fatalf("materials inside a folder left out = %+v", records)
	}
}

// heroes is a vault whose folder «Персонажи» holds count notes, each a hero of its own name.
type heroes struct{ count int }

func (vault heroes) Open(string) (service.Vault, error) { return vault, nil }
func (heroes) Folder(context.Context, string) error     { return nil }
func (heroes) Close() error                             { return nil }
func (heroes) Read(context.Context, string) ([]byte, obsidian.Skip) {
	return []byte("Герой саги."), ""
}
func (vault heroes) Walk(_ context.Context, folder string, visit service.Visit) error {
	for index := range vault.count {
		if _, stop := visit(folder, obsidian.Found{Name: fmt.Sprintf("Герой %05d.md", index), Regular: true, Size: 20}); stop {
			return nil
		}
	}
	return nil
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.7: a preview of more than 5 000 records, or of new records that would bring the
// saga past 5 000, is obsidian.too_large with its numbers.
func TestAPreviewPastTheRecordsOfASagaIsRefused(t *testing.T) {
	choice := obsidian.Choice{Materials: []obsidian.MaterialFolder{{Path: "Персонажи", Kind: authormaterials.KindCharacter}}}
	var tooMany obsidian.TooManyRecords
	if _, err := run(t, previewOf("/vault", links{}, heroes{count: obsidian.MaxRecords + 1}), choice); !errors.As(err, &tooMany) ||
		tooMany != (obsidian.TooManyRecords{Records: obsidian.MaxRecords + 1, Saga: obsidian.MaxRecords + 1}) {
		t.Fatalf("a preview of 5 001 records = %v", err)
	}
	full := links{notes: obsidian.NoteFacts{Total: obsidian.MaxRecords - 1}}
	if _, err := run(t, previewOf("/vault", full, heroes{count: 2}), choice); !errors.As(err, &tooMany) ||
		tooMany != (obsidian.TooManyRecords{Records: 2, Saga: obsidian.MaxRecords + 1}) {
		t.Fatalf("a preview past the records of the saga = %v", err)
	}
	if result, err := run(t, previewOf("/vault", full, heroes{count: 1}), choice); err != nil || len(result.Materials.Records) != 1 {
		t.Fatalf("a preview up to the records of the saga = %v", err)
	}
}
