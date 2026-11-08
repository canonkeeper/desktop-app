package researchchat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// v3Cards are v3 cards of chapters 1 and 3 of testSaga: the hero is «Мирон» in chapter 1 and
// «Сватов», also called Мирон, in chapter 3; chapter 2 has no card.
func v3Cards() map[int]*sagamemory.Card {
	return map[int]*sagamemory.Card{
		0: {
			Summary:    "Мирон приходит в город.",
			When:       &sagamemory.ChapterTime{Lasts: &sagamemory.Span{N: 6, Unit: "час", Approx: true}, Date: "весна"},
			Events:     []sagamemory.Event{{Event: "Мирон входит в город", Who: []string{"Мирон"}, P: 1}, {Event: "Вьюжница ждёт у ворот", P: 2}},
			Characters: []sagamemory.Mention{{Name: "Мирон", P: 1}},
			Facts: []sagamemory.Fact{
				{Subject: "Мирон", Attribute: "возраст", Value: "24 года", P: 1},
				{Subject: "Мирон", Attribute: "навык", Value: "Взлом", P: 1},
				{Subject: "Вьюжница", Attribute: "местонахождение", Value: "у ворот", P: 2},
			},
		},
		2: {
			Summary:    "Вьюжница возвращается.",
			When:       &sagamemory.ChapterTime{After: &sagamemory.Span{N: 3, Unit: "день"}, Lasts: &sagamemory.Span{N: 1, Unit: "день"}, Opens: "Через три дня"},
			Events:     []sagamemory.Event{{Event: "Мирон узнаёт вьюжницу", Who: []string{"Сватов"}, P: 2}},
			Characters: []sagamemory.Mention{{Name: "Сватов", Aliases: []string{"Мирон"}, P: 2}},
			Facts: []sagamemory.Fact{
				{Subject: "Сватов", Attribute: "возраст", Value: "25 лет", P: 2},
				{Subject: "Сватов", Attribute: "возраст", Value: "7 лет", P: 1, Past: true},
			},
		},
	}
}

func newLayerTools(scope researchdomain.Scope, bookID string) (*ReadTools, *readRepositoryStub) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{
		ProjectID: "project-1", SourceView: "draft", Scope: scope, BookID: bookID,
	}}
	return NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, v3Cards()), &scanModelStub{}, nil, ScanSettings{}, noThreadLayer{}), repository
}

// specs/2026-10-29-chapter-cards-quality.md R4.1, R4.4: one call gives the facts of the hero under
// both his names — the current age with its history, the skills — with chapter and paragraph,
// and retains the chapters it cites.
func TestEntityGathersFactsOfAllNames(t *testing.T) {
	tools, repository := newLayerTools(researchdomain.ScopeProject, "")
	output, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Сватов"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Мирон (персонаж; также: Сватов): фактов 4 в 2 главах во всей саге.",
		"- возраст: 25 лет — гл. 3 (¶2). История: 24 года (гл. 1 (¶1)); 25 лет (гл. 3 (¶2)); 7 лет [прошлое] (гл. 3 (¶1))",
		"- навык (1): Взлом (гл. 1 (¶1))",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("entity = %q\nwant %q", output, want)
		}
	}
	if strings.Contains(output, "у ворот") {
		t.Fatalf("entity of the hero shows a fact of another subject: %q", output)
	}
	if want := [][]ChapterRevision{{{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
	upTo, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон", At: "2", Attributes: []string{"возраст"}})
	if err != nil || !strings.Contains(upTo, "- возраст: 24 года — гл. 1 (¶1)") || strings.Contains(upTo, "навык") {
		t.Fatalf("entity at chapter 2 = %q, %v", upTo, err)
	}
	if missing, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Кутузов"}); err != nil || !strings.Contains(missing, "Такой записи в словаре нет") {
		t.Fatalf("entity of an unknown name = %q, %v", missing, err)
	}
	if _, err := tools.Entity(context.Background(), rootCall, EntityInput{}); err == nil {
		t.Fatal("entity without a name succeeded")
	}
}

// R5.1, R5.2: the time line adds the chapter times in story order, counts unknown and estimated
// spans, names the time between two chapters and shows the events of a short range.
func TestTimelineAddsChapterTimesAndShowsEvents(t *testing.T) {
	tools, repository := newLayerTools(researchdomain.ScopeProject, "")
	output, err := tools.Timeline(context.Background(), rootCall, TimelineInput{From: "1", To: "3"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"От начала гл. 1 до конца гл. 3: ≈ 4 дн. (неизвестных промежутков 2, оценённых 1).",
		"- гл. 1: день 0,0–0,2; длится 6 час (оценка); дата: весна [неизв. 0, оцен. 1]\n  - ¶1 Мирон входит в город\n",
		"- гл. 2: день 0,2–0,2; времени главы в карточке нет [неизв. 2, оцен. 1]\n",
		"- гл. 3: день 3,2–4,2; до главы 3 день; длится 1 день; «Через три дня» [неизв. 2, оцен. 1]\n  - ¶2 Мирон узнаёт вьюжницу\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("timeline = %q\nwant %q", output, want)
		}
	}
	if want := [][]ChapterRevision{{{ChapterID: "c1", Revision: 3}, {ChapterID: "c2", Revision: 5}, {ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
	filtered, err := tools.Timeline(context.Background(), rootCall, TimelineInput{Query: "вьюжницу"})
	if err != nil || strings.Contains(filtered, "гл. 1:") || !strings.Contains(filtered, "¶2 Мирон узнаёт вьюжницу") {
		t.Fatalf("timeline with a query = %q, %v", filtered, err)
	}
}

// AGENTS.md: a tool retains every chapter whose card data entered its answer — the facts behind a
// profile's counts and the times behind a story day — not only the chapters it cites.
func TestSagaLayerToolsRetainTheChaptersBehindTheAnswer(t *testing.T) {
	tools, repository := newLayerTools(researchdomain.ScopeProject, "")
	if _, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон", Attributes: []string{"навык"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Timeline(context.Background(), rootCall, TimelineInput{From: "3", To: "3"}); err != nil {
		t.Fatal(err)
	}
	want := [][]ChapterRevision{
		{{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}},
		{{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}},
	}
	if !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want the chapters whose facts and times built the answers", repository.retained)
	}
}

// A book-scoped chat counts its story time over its own book only and never names a chapter of
// another book, also when from is blank.
func TestTimelineInABookScopeSeesOnlyItsBook(t *testing.T) {
	tools, repository := newLayerTools(researchdomain.ScopeBook, "b2")
	output, err := tools.Timeline(context.Background(), rootCall, TimelineInput{From: "  ", To: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "гл. 1") || strings.Contains(output, "От начала") ||
		!strings.Contains(output, "- гл. 3: день 0,0–1,0; длится 1 день; «Через три дня» [неизв. 0, оцен. 0]") {
		t.Fatalf("book-scoped timeline = %q", output)
	}
	if want := [][]ChapterRevision{{{ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want only the book's chapter", repository.retained)
	}
}

// specs/2026-11-01-index-foundation.md R5.4: with a registry a role leads to the hero and his facts,
// also in a chat of one book, and a role is not shown as a name.
func TestEntityFindsTheHeroByARoleOfTheRegistry(t *testing.T) {
	cards := v3Cards()
	cards[2].Facts = append(cards[2].Facts, sagamemory.Fact{Subject: "Первый король", Attribute: "титул", Value: "король Карагоры", P: 1})
	for _, authority := range []struct {
		scope researchdomain.Scope
		book  string
	}{{researchdomain.ScopeProject, ""}, {researchdomain.ScopeBook, "b2"}} {
		text := sagamemorytest.NewText(testSaga(), testStems, cards)
		text.SetNames(&sagamemory.NameRegistry{Persons: []sagamemory.NamePerson{
			{Name: "мирон", Names: []string{"сватов"}, Roles: []string{"первый король"}},
		}})
		tools, _ := newLayerTools(authority.scope, authority.book)
		tools.saga = sagamemoryservice.NewSaga(text, text)
		output, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "первый король"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output, "король Карагоры") || strings.Contains(strings.SplitN(output, "\n", 2)[0], "первый король") {
			t.Fatalf("%s: profile = %q, want the fact under the role and the role not among the names", authority.scope, output)
		}
	}
}

// A chapter off the main line is shown apart and takes no story time.
func TestTimelineShowsAChapterOffTheMainLineApart(t *testing.T) {
	cards := v3Cards()
	cards[1] = &sagamemory.Card{Summary: "Молли.", When: &sagamemory.ChapterTime{
		Lasts: &sagamemory.Span{N: 5, Unit: "день"}, Note: "Другая сюжетная линия: Молли за неделю до боя",
	}}
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, cards), &scanModelStub{}, nil, ScanSettings{}, noThreadLayer{})
	output, err := tools.Timeline(context.Background(), rootCall, TimelineInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "- гл. 2: вне основной шкалы — Другая сюжетная линия: Молли за неделю до боя\n") ||
		!strings.Contains(output, "- гл. 3: день 3,2–4,2;") {
		t.Fatalf("timeline = %q, want chapter 2 apart and chapter 3 not moved by it", output)
	}
}

// specs/2026-11-01-index-foundation.md R4.2: without a query the time line shows the main events of a
// v4 card, with a query all of them; a card without marks shows what it has.
func TestTimelineShowsTheMainEventsOfACard(t *testing.T) {
	card := &sagamemory.Card{Events: []sagamemory.Event{{Event: "Бой", Key: true}, {Event: "Обед"}}}
	if main := shownEvents(card, ""); len(main) != 1 || main[0].Event != "Бой" {
		t.Fatalf("main events %+v", main)
	}
	if all := shownEvents(card, "обед"); len(all) != 2 {
		t.Fatalf("events for a query %+v", all)
	}
	unmarked := &sagamemory.Card{Events: []sagamemory.Event{{Event: "Бой"}}}
	if shown := shownEvents(unmarked, ""); len(shown) != 1 {
		t.Fatalf("a v3 card shows %+v", shown)
	}
}

// specs/2026-11-01-index-foundation.md R4.1: what a character knows is in the profile under all names,
// and `at` cuts off what they learn later.
func TestEntityShowsWhatTheCharacterKnows(t *testing.T) {
	cards := v3Cards()
	cards[0].Knows = []sagamemory.Knowledge{{Who: "Мирон", Kind: "скрывает", What: "своё имя", P: 1}}
	cards[2].Knows = []sagamemory.Knowledge{{Who: "Сватов", Kind: "узнал", What: "кто такая вьюжница", P: 2}}
	tools, _ := newLayerTools(researchdomain.ScopeProject, "")
	tools.saga = sagamemorytest.NewSaga(testSaga(), testStems, cards)
	all, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "скрывает") || !strings.Contains(all, "своё имя") || !strings.Contains(all, "кто такая вьюжница") {
		t.Fatalf("profile: %s", all)
	}
	early, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон", At: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(early, "своё имя") || strings.Contains(early, "кто такая вьюжница") {
		t.Fatalf("profile at chapter 1: %s", early)
	}
}
