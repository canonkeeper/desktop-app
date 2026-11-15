// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/library"
)

func note(t *testing.T, notePath, text string) obsidian.Note {
	t.Helper()
	read, skip := obsidian.ReadNote(notePath, []byte(text))
	if skip != "" {
		t.Fatalf("%s skipped: %s", notePath, skip)
	}
	return read
}

// parsed glues a folder and reads the text as the queue reads a volume: the title of the book and the titles of its
// chapters as the import makes them.
func parsed(t *testing.T, folder, name string, notes ...obsidian.Note) (obsidian.Glued, library.ImportedBook) {
	t.Helper()
	glued := obsidian.Glue(folder, name, notes)
	volume, err := library.CheckQueueVolume(glued.FileName, []byte(glued.Text))
	if err != nil || volume.Refusal != "" {
		t.Fatalf("the volume does not read: %v %q\n%s", err, volume.Refusal, glued.Text)
	}
	return glued, volume.Book
}

func titles(book library.ImportedBook) []string {
	var titles []string
	for _, chapter := range book.Chapters {
		titles = append(titles, chapter.Title)
	}
	return titles
}

// specs/2026-11-13-desk-obsidian.md OB3.3: the text of a folder goes through the standard reading of .md, and its chapters
// are compared with its notes.
func TestAFolderIsOneBookTheImportReads(t *testing.T) {
	t.Run("a folder of one note", func(t *testing.T) {
		glued, book := parsed(t, "Северный предел", "Северный предел",
			note(t, "Северный предел/01 Застава.md", "Сторожевая башня стояла на холме.\n"))
		if book.Title != "Северный предел" || !slices.Equal(titles(book), []string{"Застава"}) || obsidian.ChaptersDiffer(glued.Notes, titles(book)) != nil {
			t.Fatalf("book %q chapters %q", book.Title, titles(book))
		}
	})
	t.Run("the first note starts with «Глава 1»", func(t *testing.T) {
		glued, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/01 Застава.md", "Глава 1\n\nСторожевая башня стояла на холме.\n"),
			note(t, "Книга/02 Переправа.md", "Паром ждал у берега.\n"))
		if book.Title != "Книга" {
			t.Fatalf("the title of the book = %q", book.Title)
		}
		if differ := obsidian.ChaptersDiffer(glued.Notes, titles(book)); !slices.Equal(differ, []string{"Книга/01 Застава.md"}) {
			t.Fatalf("chapters %q differ at %q", titles(book), differ)
		}
	})
	t.Run("a title longer than 120 signs is cut", func(t *testing.T) {
		long := strings.Repeat("Очень длинное название ", 8)
		glued, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/a.md", "---\ntitle: "+long+"\n---\nТекст главы.\n"), note(t, "Книга/b.md", "Ещё глава.\n"))
		if len(glued.Cut) != 1 || len(book.Chapters) != 2 || len([]rune(book.Chapters[0].Title)) > obsidian.MaxTitleRunes {
			t.Fatalf("cut %q chapters %q", glued.Cut, titles(book))
		}
	})
	t.Run("an interlude inside a note is a chapter the preview names", func(t *testing.T) {
		glued, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/01 Застава.md", "Утро.\n\n## Интерлюдия\n\nТем временем на юге.\n"),
			note(t, "Книга/02 Переправа.md", "Паром.\n"))
		if !slices.Equal(titles(book), []string{"Застава", "Интерлюдия", "Переправа"}) {
			t.Fatalf("chapters %q", titles(book))
		}
		if differ := obsidian.ChaptersDiffer(glued.Notes, titles(book)); !slices.Equal(differ, []string{"Книга/01 Застава.md"}) {
			t.Fatalf("differ %q", differ)
		}
	})
	t.Run("its own heading of the title at the start of a note goes", func(t *testing.T) {
		_, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/01 Застава.md", "# Застава\n\nСторожевая башня.\n\n## Ночь\n\nТемно.\n"), note(t, "Книга/02 Переправа.md", "Паром.\n"))
		if !slices.Equal(titles(book), []string{"Застава", "Переправа"}) || book.Chapters[0].Paragraphs[0] != "Сторожевая башня." {
			t.Fatalf("chapters %+v", book.Chapters)
		}
	})
	t.Run("a book in one note", func(t *testing.T) {
		glued, book := parsed(t, "Северный предел", "Северный предел",
			note(t, "Северный предел/Книга.md", "## Белый порт\n\nКорабли.\n\n### Причал\n\nДоски.\n\n## Чёрный лес\n\nТишина.\n"))
		if book.Title != "Северный предел" || !slices.Equal(titles(book), []string{"Белый порт", "Чёрный лес"}) || !glued.Notes[0].InNote {
			t.Fatalf("book %q chapters %q in note %v\n%s", book.Title, titles(book), glued.Notes[0].InNote, glued.Text)
		}
		if differ := obsidian.ChaptersDiffer(glued.Notes, titles(book)); differ != nil {
			t.Fatalf("differ %q", differ)
		}
		// Among several notes only headings of the first level make a book in a note: scenes of «##» stay in their chapter.
		_, scenes := parsed(t, "Книга", "Книга",
			note(t, "Книга/01.md", "## Сцена 1\n\nА.\n\n## Сцена 2\n\nБ.\n"), note(t, "Книга/02.md", "В.\n"))
		if len(scenes.Chapters) != 2 {
			t.Fatalf("scenes %q", titles(scenes))
		}
	})
	t.Run("the order by the header, by the index and by the name", func(t *testing.T) {
		glued, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/Б.md", "---\norder: 2\n---\nБ.\n"),
			note(t, "Книга/А.md", "---\norder: 10\n---\nА.\n"),
			note(t, "Книга/_book.md", "---\nbook: Северный предел\n---\n- [[Глава 2]]\n- [[Глава 1]]\n"),
			note(t, "Книга/Глава 1.md", "Один.\n"),
			note(t, "Книга/Глава 2.md", "Два.\n"),
			note(t, "Книга/Глава 10.md", "Десять.\n"),
			note(t, "Книга/Глава 3.md", "Три.\n"),
			note(t, "Книга/Части/Глава 0.md", "Ноль.\n"))
		want := []string{"Б", "А", "Глава 2", "Глава 1", "Глава 3", "Глава 10", "Глава 0"}
		if book.Title != "Северный предел" || !slices.Equal(titles(book), want) {
			t.Fatalf("book %q chapters %q", book.Title, titles(book))
		}
		var sources []obsidian.OrderSource
		for _, gluedNote := range glued.Notes {
			sources = append(sources, gluedNote.OrderBy)
		}
		if !slices.Equal(sources, []obsidian.OrderSource{"front", "front", "index", "index", "name", "name", "name"}) || glued.OrderByName {
			t.Fatalf("sources %q", sources)
		}
		_, byName := parsed(t, "Книга", "Книга", note(t, "Книга/Эпилог.md", "Конец.\n"), note(t, "Книга/Застава.md", "Начало.\n"))
		if named := obsidian.Glue("Книга", "Книга", []obsidian.Note{note(t, "Книга/Б.md", "Б.\n"), note(t, "Книга/А.md", "А.\n")}); !named.OrderByName {
			t.Fatal("names without numbers are not named")
		}
		if !slices.Equal(titles(byName), []string{"Застава", "Эпилог"}) {
			t.Fatalf("by name %q", titles(byName))
		}
	})
	t.Run("a folder that reads as a chapter is a book in quotes", func(t *testing.T) {
		_, book := parsed(t, "Пролог", "Пролог", note(t, "Пролог/а.md", "Текст.\n"), note(t, "Пролог/б.md", "Ещё.\n"))
		if book.Title != "«Пролог»" || len(book.Chapters) != 2 {
			t.Fatalf("book %q chapters %q", book.Title, titles(book))
		}
	})
	t.Run("the fingerprint does not depend on the order of the walk", func(t *testing.T) {
		notes := []obsidian.Note{
			note(t, "Книга/03.md", "Три.\n"), note(t, "Книга/01.md", "Один.\n"), note(t, "Книга/02.md", "Два.\n"), note(t, "Книга/Части/04.md", "Четыре.\n"),
		}
		first := obsidian.Glue("Книга", "Книга", notes)
		for range 10 {
			shuffled := slices.Clone(notes)
			rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			if again := obsidian.Glue("Книга", "Книга", shuffled); again.Fingerprint != first.Fingerprint {
				t.Fatalf("the fingerprint changed with the order:\n%s\n%s", first.Text, again.Text)
			}
		}
		// An NFD name gives the same text and fingerprint as its NFC one.
		nfd := obsidian.Glue("Книга", "Книга", []obsidian.Note{note(t, "Книга/Северный.md", "Текст.\n")})
		nfc := obsidian.Glue("Книга", "Книга", []obsidian.Note{note(t, "Книга/Северный.md", "Текст.\n")})
		if nfd.Fingerprint != nfc.Fingerprint {
			t.Fatal("NFD and NFC give two fingerprints")
		}
	})
	t.Run("the headings of a note go one level lower", func(t *testing.T) {
		glued, book := parsed(t, "Книга", "Книга",
			note(t, "Книга/01 Застава.md", "Утро.\n\n# Ночь\n\nТемно.\n"), note(t, "Книга/02 Переправа.md", "Паром.\n"))
		if !slices.Equal(titles(book), []string{"Застава", "Переправа"}) || !slices.Contains(book.Chapters[0].Paragraphs, "Ночь") ||
			obsidian.ChaptersDiffer(glued.Notes, titles(book)) != nil {
			t.Fatalf("chapters %+v\n%s", book.Chapters, glued.Text)
		}
	})
	t.Run("the fences of a note are closed as the import reads them", func(t *testing.T) {
		for name, first := range map[string]string{
			"a fence of «`» closed by «~»":   "Текст.\n```\nкод\n~~~\nещё\n",
			"a fence inside a fence":         "Текст.\n```\nпример:\n```js\nx\n```\nконец\n",
			"a fence open at the end":        "Текст.\n```\nкод до конца\n# Не глава\n",
			"a heading between two fences":   "Текст.\n```\nа\n```js\n# Шторм\n```\nб\n",
			"a fence of four inside nothing": "````\n```\nвнутри\n```\n````\nПосле.\n",
		} {
			glued, book := parsed(t, "К", "К", note(t, "К/01 Застава.md", first), note(t, "К/02 Переправа.md", "Паром.\n"), note(t, "К/03 Брод.md", "Брод.\n"))
			if !slices.Equal(titles(book), []string{"Застава", "Переправа", "Брод"}) || obsidian.ChaptersDiffer(glued.Notes, titles(book)) != nil {
				t.Fatalf("%s: chapters %q\n%s", name, titles(book), glued.Text)
			}
		}
	})
	t.Run("the signs of a title are the signs the import reads", func(t *testing.T) {
		for title, want := range map[string]string{
			"*Курсив*": "*Курсив*", "Про C#": "Про C#", "[[Не ссылка]]": "[[Не ссылка]]", "---": "«---»", "·•·": "«·•·»",
			"Сага \\ и путь": "Сага \\ и путь",
			// An address in angle brackets loses them in the import, a code its backticks: the same title all the same.
			"Сага <https://example.test>": "Сага https://example.test", "Книга `один`": "Книга один",
		} {
			glued, book := parsed(t, "К", "К",
				note(t, "К/01.md", "---\ntitle: \""+strings.ReplaceAll(title, `\`, `\\`)+"\"\n---\nТекст.\n"), note(t, "К/02 Переправа.md", "Паром.\n"))
			if !slices.Equal(titles(book), []string{want, "Переправа"}) || obsidian.ChaptersDiffer(glued.Notes, titles(book)) != nil {
				t.Fatalf("%s: chapters %q\n%s", title, titles(book), glued.Text)
			}
		}
		_, book := parsed(t, "Заметки о C#", "Заметки о C#", note(t, "Заметки о C#/a.md", "Текст.\n"), note(t, "Заметки о C#/b.md", "Ещё.\n"))
		if book.Title != "Заметки о C#" {
			t.Fatalf("the title of the book = %q", book.Title)
		}
	})
	t.Run("a chapter before the first chapter of a note is named", func(t *testing.T) {
		glued, book := parsed(t, "К", "К", note(t, "К/Том.md", "* * *\n\n# Белый порт\n\nКорабли.\n\n# Чёрный лес\n\nТишина.\n"))
		if differ := obsidian.ChaptersDiffer(glued.Notes, titles(book)); len(titles(book)) == 2 || !slices.Equal(differ, []string{"К/Том.md"}) {
			t.Fatalf("chapters %q differ %q", titles(book), differ)
		}
	})
	t.Run("a note without words is skipped and counted", func(t *testing.T) {
		glued, book := parsed(t, "Книга", "Книга", note(t, "Книга/a.md", "%% только комментарий %%\n#тег\n"), note(t, "Книга/b.md", "Текст.\n"))
		if glued.Skipped[obsidian.SkipEmpty] != 1 || len(book.Chapters) != 1 {
			t.Fatalf("skipped %v chapters %q", glued.Skipped, titles(book))
		}
	})
}
