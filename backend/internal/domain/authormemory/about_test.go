package authormemory

import (
	"slices"
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// specs/2026-11-11-chat-corrections.md CR1.1, CR1.3: an answer about a hero brings the corrections and the plans about him, in
// their order; a rule of style and what the author rejected are no facts about him.
func TestOnlyTheCorrectionsAndThePlansAboutWhatIsNamedComeWithAnAnswer(t *testing.T) {
	records := []Record{
		{ID: "correction", Kind: KindCorrection, Name: "Варвара", Text: "Лампу остановила Варвара, а не Гриша."},
		{ID: "style", Kind: KindStyleRule, Name: "Варвара", Text: "Варвара говорит коротко."},
		{ID: "plan", Kind: KindPlan, Text: "Варвара уедет в столицу."},
		{ID: "rejected", Kind: KindRejected, Text: "Варвара не умрёт."},
		{ID: "another", Kind: KindCorrection, Name: "Гриша", Text: "Грише двадцать лет."},
	}
	var ids []string
	for _, record := range About(records, sagamemory.NamedNames("Варвара").Subject(nil)) {
		ids = append(ids, record.ID)
	}
	if !slices.Equal(ids, []string{"correction", "plan"}) {
		t.Fatalf("about Варвара: %v", ids)
	}
	if !slices.Equal(AboutKinds, []Kind{KindCorrection, KindPlan}) {
		t.Fatalf("the kinds read for an answer: %v", AboutKinds)
	}
	signed := Record{Kind: KindCorrection, ChapterID: "chapter", Name: "Варвара"}
	if got := signed.AboutSignature(map[string]string{"chapter": "12"}); got != "поправка автора: важнее карточек и пересказов; если текст книги говорит иначе — назови оба варианта; глава 12; имя: Варвара" {
		t.Fatalf("a correction is signed %q", got)
	}
	if got := (Record{Kind: KindPlan, ChapterID: "gone"}).AboutSignature(nil); got != "план автора: в книге этого ещё нет; глава удалена" {
		t.Fatalf("a plan is signed %q", got)
	}
}
