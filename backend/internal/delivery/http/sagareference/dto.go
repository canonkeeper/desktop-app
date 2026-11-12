package sagareference

import (
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagareference"
)

// The JSON shapes of contracts/openapi.json (Saga* schemas). Lists are never null.

type coverage struct {
	Chapters  int `json:"chapters"`
	WithCards int `json:"withCards"`
}

type chapterRef struct {
	Key string `json:"key"`
	// Label is the place of the chapter as the author reads it (specs/2026-11-10-ux1-names.md N1.3).
	Label     string `json:"label"`
	ChapterID string `json:"chapterId"`
	BookID    string `json:"bookId"`
	BookOrder int    `json:"bookOrder"`
	BookTitle string `json:"bookTitle"`
	Title     string `json:"title"`
	Number    int    `json:"number,omitempty"`
}

type typeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type entrySummary struct {
	Key          string     `json:"key"`
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Aliases      []string   `json:"aliases"`
	Note         string     `json:"note,omitempty"`
	ChapterCount int        `json:"chapterCount"`
	First        chapterRef `json:"first"`
}

type glossaryPage struct {
	Coverage coverage       `json:"coverage"`
	Types    []typeCount    `json:"types"`
	Total    int            `json:"total"`
	Offset   int            `json:"offset"`
	Entries  []entrySummary `json:"entries"`
}

type book struct {
	ID           string   `json:"id"`
	Order        int      `json:"order"`
	Title        string   `json:"title"`
	ChapterCount int      `json:"chapterCount"`
	Keys         []string `json:"keys"`
}

type contentsChapter struct {
	Chapter chapterRef `json:"chapter"`
	Summary *string    `json:"summary,omitempty"`
}

type contents struct {
	Coverage coverage          `json:"coverage"`
	Books    []book            `json:"books"`
	BookID   string            `json:"bookId,omitempty"`
	Chapters []contentsChapter `json:"chapters"`
	// Chapter is the chapter the asked key names, under its key of now (specs/2026-11-11-ux7-reorder.md RK1.5).
	Chapter *chapterRef `json:"chapter,omitempty"`
}

func coverageDTO(value sagamemory.Coverage) coverage {
	return coverage{Chapters: value.Chapters, WithCards: value.WithCards}
}

func chapterDTO(chapter *sagamemory.Chapter) chapterRef {
	return chapterRef{
		Key: chapter.Key, Label: chapter.Place(), ChapterID: chapter.ID, BookID: chapter.BookID, BookOrder: chapter.BookOrder,
		BookTitle: chapter.BookTitle, Title: chapter.Title, Number: chapter.Number,
	}
}

func aliases(entry *sagamemory.GlossaryEntry) []string {
	return append([]string{}, entry.Aliases...)
}

func glossaryPageDTO(result service.GlossaryResult) glossaryPage {
	page := glossaryPage{
		Coverage: coverageDTO(result.Coverage), Types: make([]typeCount, len(result.Types)), Total: result.Total,
		Offset: result.Offset, Entries: make([]entrySummary, len(result.Entries)),
	}
	for i, value := range result.Types {
		page.Types[i] = typeCount{Type: value.Type, Count: value.Count}
	}
	for i, item := range result.Entries {
		page.Entries[i] = entrySummary{
			Key: sagamemory.EntryKey(item.Entry), Name: item.Entry.Name, Type: item.Entry.Type, Aliases: aliases(item.Entry),
			Note: item.Entry.Note, ChapterCount: len(item.Entry.Chapters), First: chapterDTO(item.First),
		}
	}
	return page
}

func booksDTO(books []service.BookView) []book {
	out := make([]book, len(books))
	for i, value := range books {
		out[i] = book{
			ID: value.ID, Order: value.Order, Title: value.Title, ChapterCount: value.Last - value.First + 1, Keys: value.Keys,
		}
	}
	return out
}

func contentsDTO(result service.ContentsResult) contents {
	out := contents{
		Coverage: coverageDTO(result.Coverage), Books: booksDTO(result.Books),
		Chapters: make([]contentsChapter, len(result.Chapters)),
	}
	if result.Book != nil {
		out.BookID = result.Book.ID
	}
	if result.Found != nil {
		found := chapterDTO(result.Found)
		out.Chapter = &found
	}
	for i, item := range result.Chapters {
		value := contentsChapter{Chapter: chapterDTO(item.Chapter)}
		if item.HasCard {
			summary := item.Summary
			value.Summary = &summary
		}
		out.Chapters[i] = value
	}
	return out
}

func nonNil(values []int) []int {
	if values == nil {
		return []int{}
	}
	return values
}
