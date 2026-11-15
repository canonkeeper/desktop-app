package library

import "testing"

// specs/2026-11-15-reader-chapter-number.md RN1.1–RN1.3: the reader names a chapter by the one rule of the number of a
// chapter — the number its title writes, its place among the living chapters of its book when the title writes none — and
// gives its title without that number beside it; the stored order is not the number. The list, the chapter and its
// neighbours answer alike.
func TestTheReaderNamesAChapterByItsNumberAndItsTitleWithoutIt(t *testing.T) {
	tests := []struct {
		name         string
		order, place int
		title        string
		number       string
		displayTitle string
	}{
		{name: "a heading with a title", order: 1, place: 1, title: "Глава 581. Возвращение", number: "581", displayTitle: "Возвращение"},
		{name: "a heading only", order: 37, place: 30, title: "Глава 617.", number: "617", displayTitle: ""},
		{name: "a number and a title", order: 9, place: 8, title: "12. Белый порт", number: "12", displayTitle: "Белый порт"},
		{name: "a roman number", order: 14, place: 14, title: "Глава XIV — Письмо", number: "XIV", displayTitle: "Письмо"},
		{name: "a title after a deleted chapter", order: 4, place: 3, title: "Сигнальные огни", number: "3", displayTitle: "Сигнальные огни"},
		{name: "a book stored from zero", order: 0, place: 1, title: "Пролог", number: "1", displayTitle: "Пролог"},
		{name: "spaces around", order: 4, place: 4, title: " Глава 581.  Возвращение ", number: "581", displayTitle: "Возвращение"},
		{name: "a fractional number", order: 5, place: 5, title: "Глава 581.1: Переход", number: "581.1", displayTitle: "Переход"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chapter := Chapter{Order: test.order, Place: test.place, Title: test.title}
			if got := chapterSummaryResponseFromChapter(ChapterSummary{Chapter: chapter}); got.DisplayNumber != test.number || got.DisplayTitle != test.displayTitle || got.Order != test.order {
				t.Errorf("the list names it %q «%s» (order %d), want %q «%s»", got.DisplayNumber, got.DisplayTitle, got.Order, test.number, test.displayTitle)
			}
			neighbour := &ChapterNavigationItem{Title: test.title, Place: test.place}
			got := chapterResponseFromContent(ChapterContent{Chapter: chapter, Navigation: ChapterNavigation{Previous: neighbour, Next: neighbour}}).Navigation
			if got.DisplayNumber != test.number || got.DisplayTitle != test.displayTitle {
				t.Errorf("the chapter names it %q «%s», want %q «%s»", got.DisplayNumber, got.DisplayTitle, test.number, test.displayTitle)
			}
			for _, item := range []*chapterNavigationItemResponse{got.Previous, got.Next} {
				if item.DisplayNumber != test.number || item.DisplayTitle != test.displayTitle || item.Title != test.title {
					t.Errorf("a neighbour is named %+v, want %q «%s»", item, test.number, test.displayTitle)
				}
			}
		})
	}
}
