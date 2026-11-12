package sagareference

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

func firstTexts(t *testing.T, useCase *GetFirstQuestions) []string {
	t.Helper()
	texts, _ := firstAnswer(t, useCase)
	return texts
}

// firstAnswer is the questions and whether the map is still being built (FQ1.9).
func firstAnswer(t *testing.T, useCase *GetFirstQuestions) ([]string, bool) {
	t.Helper()
	result, err := useCase.Execute(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Questions == nil {
		t.Fatal("questions = nil, want a list")
	}
	texts := make([]string, len(result.Questions))
	for i, question := range result.Questions {
		texts[i] = string(question.Kind) + ": " + question.Text
	}
	return texts, result.Pending
}

// specs/2026-11-10-first-questions.md FQ1.1, FQ1.4: the questions come from the prepared row of the map — Мила is the hero, Сава
// runs with her twice, and the saga has no place, so the third is of the dagger; no card of a chapter is read and nothing is queued.
func TestFirstQuestionsComeFromThePreparedMap(t *testing.T) {
	cards, saga, maps, _ := mapSaga(t)
	useCase := NewGetFirstQuestions(fakeAccess{}, saga, maps)
	want := []string{
		"hero: Мила: кто это и что важного происходит с этим персонажем?",
		"pair: Мила и Сава: что их связывает?",
		"place: Где впервые упоминается Кинжал?",
	}
	if got, pending := firstAnswer(t, useCase); !reflect.DeepEqual(got, want) || pending {
		t.Fatalf("questions = %q, pending %v\nwant %q", got, pending, want)
	}
	if cards.reads != 0 || maps.Queued != 0 || maps.Reads != 1 {
		t.Fatalf("card reads %d, queued %d, row reads %d; want one read of the row", cards.reads, maps.Queued, maps.Reads)
	}
	// A map behind its cards answers by what is ready and asks for no build: the read of the map does that.
	cards.RewriteCards()
	if got, pending := firstAnswer(t, useCase); !reflect.DeepEqual(got, want) || pending || maps.Queued != 0 {
		t.Fatalf("behind its cards = %q, pending %v, queued %d", got, pending, maps.Queued)
	}
}

// Invariant «нет имени, которое автор с карты убрал»: the decisions of the author apply before the questions are asked.
func TestFirstQuestionsLeaveOutWhatTheAuthorTookOff(t *testing.T) {
	_, saga, maps, _ := mapSaga(t)
	useCase := NewGetFirstQuestions(fakeAccess{}, saga, maps)
	maps.Hidden = []sagamemory.MapHiddenItem{{ID: "1", NodeKey: "сава", NodeName: "Сава"}}
	if got := firstTexts(t, useCase); len(got) != 2 || got[1] != "place: Где впервые упоминается Кинжал?" {
		t.Fatalf("Сава taken off = %q", got)
	}
	maps.Hidden = []sagamemory.MapHiddenItem{{ID: "2", NodeKey: "мила", NodeName: "Мила"}}
	if got, pending := firstAnswer(t, useCase); len(got) != 0 || pending {
		t.Fatalf("the hero taken off = %q, pending %v; a map without a hero is no map being built", got, pending)
	}
}

// FQ1.4, FQ1.9: no prepared row — no questions and no build asked for. A saga whose text has chapters and no row of the map yet
// says the map is still to come — its cards are being written (TEST, 11.11: a chat opened right after the upload never asked
// again) or written and the map not yet built; a saga without a chapter does not. Access goes as for the map.
func TestFirstQuestionsWithoutAMap(t *testing.T) {
	_, saga, maps, _ := mapSaga(t)
	maps.Forget("project", "draft")
	if got, pending := firstAnswer(t, NewGetFirstQuestions(fakeAccess{}, saga, maps)); len(got) != 0 || !pending || maps.Queued != 0 {
		t.Fatalf("cards without a row = %q, pending %v, queued %d", got, pending, maps.Queued)
	}

	bare := sagamemorytest.NewText(sagamemory.NewVersion("project", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
	}), nil, map[int]*sagamemory.Card{})
	waiting := sagamemorytest.NewMaps()
	if got, pending := firstAnswer(t, NewGetFirstQuestions(fakeAccess{}, service.NewSaga(bare, bare), waiting)); len(got) != 0 || !pending || waiting.Queued != 0 || waiting.Reads != 0 {
		t.Fatalf("chapters without cards = %q, pending %v, queued %d, row reads %d; want the map still to come and no read of a row", got, pending, waiting.Queued, waiting.Reads)
	}

	blank := sagamemorytest.NewText(sagamemory.NewVersion("project", "draft", "fp", nil), nil, map[int]*sagamemory.Card{})
	empty := sagamemorytest.NewMaps()
	if got, pending := firstAnswer(t, NewGetFirstQuestions(fakeAccess{}, service.NewSaga(blank, blank), empty)); len(got) != 0 || pending || empty.Queued != 0 || empty.Reads != 0 {
		t.Fatalf("no chapters = %q, pending %v, queued %d, row reads %d", got, pending, empty.Queued, empty.Reads)
	}

	for _, denied := range []error{ErrDenied, ErrNotFound} {
		if _, err := NewGetFirstQuestions(fakeAccess{err: denied}, saga, maps).Execute(context.Background(), scope); !errors.Is(err, denied) {
			t.Fatalf("err = %v, want %v", err, denied)
		}
	}
	if _, err := NewGetFirstQuestions(fakeAccess{}, saga, maps).Execute(context.Background(), Scope{ActorUserID: "actor", ProjectID: "project", View: "other"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for an unknown view", err)
	}
}
