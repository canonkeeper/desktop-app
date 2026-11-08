package sagamemory_test

import (
	"context"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// specs/2026-10-29-chapter-cards-quality.md R4.1: the facts of a chapter off the main line (its
// time note declares another storyline or a recollection) are history, never a current value.
func TestFactsOfAChapterOffTheMainLineArePast(t *testing.T) {
	version := domain.NewVersion("p", "draft", "fp", []domain.Chapter{
		{ID: "c1", BookID: "b", BookOrder: 1, Order: 1, Title: "Глава 1", Paragraphs: []domain.Paragraph{{Text: "Мирон."}}},
		{ID: "c2", BookID: "b", BookOrder: 1, Order: 2, Title: "Глава 2", Paragraphs: []domain.Paragraph{{Text: "Молли."}}},
	})
	cards := map[int]*domain.Card{
		0: {Summary: "s", Facts: []domain.Fact{{Subject: "Мирон", Attribute: "уровень", Value: "9", P: 1}}},
		1: {Summary: "s", When: &domain.ChapterTime{Note: "Другая сюжетная линия: за неделю до боя"},
			Facts: []domain.Fact{{Subject: "Мирон", Attribute: "уровень", Value: "7", P: 1}}},
	}
	saga := sagamemorytest.NewSaga(version, nil, cards)
	outline, err := saga.Outline(context.Background(), "p", "draft")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := saga.Facts(context.Background(), outline, -1, []string{"мирон"})
	if err != nil || len(facts) != 2 || facts[0].Fact.Past || !facts[1].Fact.Past {
		t.Fatalf("facts = %+v, %v; want the fact of the other storyline marked past", facts, err)
	}
}
