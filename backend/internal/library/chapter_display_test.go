package library

import "testing"

func TestChapterResponsesUseExactSourceOrdinalOrLocalOrder(t *testing.T) {
	tests := []struct {
		name  string
		order int
		title string
		want  string
	}{
		{name: "imported heading with title", order: 1, title: "Глава 581. Возвращение", want: "581"},
		{name: "imported heading only", order: 37, title: "Глава 617.", want: "617"},
		{name: "manual title", order: 2, title: "Пролог", want: "2"},
		{name: "near miss decimal", order: 3, title: "Глава 581.1: Переход", want: "3"},
		{name: "near miss leading whitespace", order: 4, title: " Глава 581. Возвращение", want: "4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chapter := Chapter{Order: test.order, Title: test.title}
			if got := chapterSummaryResponseFromChapter(ChapterSummary{Chapter: chapter}).DisplayNumber; got != test.want {
				t.Errorf("list display number = %q, want %q", got, test.want)
			}
			if got := chapterResponseFromContent(ChapterContent{Chapter: chapter}).Navigation.DisplayNumber; got != test.want {
				t.Errorf("detail display number = %q, want %q", got, test.want)
			}
		})
	}
}
