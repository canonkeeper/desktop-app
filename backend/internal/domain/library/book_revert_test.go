package library

import (
	"slices"
	"testing"
	"time"
)

var revertAt = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// A book of five chapters the update changed: «a» other words, «b» renamed and moved, «c» removed, «d» added, «e» left as
// it was. The book now is what the update left.
func updatedBook() ([]SnapshotChapter, []ChapterNow) {
	snapshot := []SnapshotChapter{
		{ID: "a", Change: UpdateWords, Revision: 2, Generation: 1, PreviousRevision: 1, PreviousTitle: "Глава 1", PreviousOrder: 1},
		{ID: "b", Change: UpdatePlaced, Revision: 1, Generation: 1, PreviousRevision: 1, PreviousTitle: "Глава 2", PreviousOrder: 2},
		{ID: "c", Change: UpdateRemoved, Revision: 1, Generation: 2, PreviousRevision: 1, PreviousTitle: "Глава 3", PreviousOrder: 3},
		{ID: "e", Change: UpdateSame, Revision: 1, Generation: 1, PreviousRevision: 1, PreviousTitle: "Глава 4", PreviousOrder: 4},
		{ID: "d", Change: UpdateAdded, Revision: 1, Generation: 1},
	}
	book := []ChapterNow{
		{ID: "a", Title: "Глава 1", Order: 1, Revision: 2, Generation: 1, Live: true},
		{ID: "d", Title: "Новая", Order: 2, Revision: 1, Generation: 1, Live: true},
		{ID: "e", Title: "Глава 4", Order: 3, Revision: 1, Generation: 1, Live: true},
		{ID: "b", Title: "Глава вторая", Order: 4, Revision: 1, Generation: 1, Live: true},
		{ID: "c", Title: "Глава 3", Order: 3, Revision: 1, Generation: 2, Restorable: true},
	}
	return snapshot, book
}

func actions(plan BookRevertPlan) map[string]RevertAction {
	got := map[string]RevertAction{}
	for _, step := range plan.Steps {
		got[step.Chapter.ID] = step.Action
	}
	return got
}

func stays(plan BookRevertPlan) map[string]RevertStay {
	got := map[string]RevertStay{}
	for _, kept := range plan.Kept {
		got[kept.Chapter.ID] = kept.Stay
	}
	return got
}

// BR2.1, BR2.2: the text, the title and place and the removed chapter come back, the added chapter goes, and the book
// stands in its order before the update.
func TestARevertBringsTheBookBackAsItWas(t *testing.T) {
	snapshot, book := updatedBook()
	plan := PlanBookRevert(revertAt, snapshot, book)
	want := map[string]RevertAction{"a": RevertText, "b": RevertPlace, "c": RevertReturn, "d": RevertRemove}
	if got := actions(plan); len(got) != len(want) || got["a"] != want["a"] || got["b"] != want["b"] || got["c"] != want["c"] || got["d"] != want["d"] {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	if len(plan.Kept) != 0 {
		t.Fatalf("kept = %v, want none", plan.Kept)
	}
	if !slices.Equal(plan.Order, []string{"a", "b", "c", "e"}) {
		t.Fatalf("order = %v, want the order before the update", plan.Order)
	}
}

// BR2.1: a chapter changed after the update stays and is named; one deleted after it stays deleted; a removed one the
// author returned himself is returned already. They keep their place in the order of the book.
func TestARevertLeavesWhatTheAuthorChangedAfterTheUpdate(t *testing.T) {
	snapshot, book := updatedBook()
	// «a» and «d» edited after the update, «b» deleted, «c» returned by hand.
	book[0].Revision = 3
	book[3] = ChapterNow{ID: "b", Title: "Глава вторая", Order: 4, Revision: 1, Generation: 2, Restorable: true}
	book[4] = ChapterNow{ID: "c", Title: "Глава 3", Order: 3, Revision: 1, Generation: 2, Live: true}
	book[1].Revision = 2
	plan := PlanBookRevert(revertAt, snapshot, book)
	if got := actions(plan); len(got) != 0 {
		t.Fatalf("steps = %v, want none", got)
	}
	got := stays(plan)
	if got["a"] != StayEdited || got["b"] != StayDeleted || got["c"] != StayReturned || got["d"] != StayEdited || len(got) != 4 {
		t.Fatalf("kept = %v", got)
	}
	// «d», kept, has no place before the update: it stands after the chapter it follows now.
	if !slices.Equal(plan.Order, []string{"a", "d", "c", "e"}) {
		t.Fatalf("order = %v", plan.Order)
	}
}

// A removed chapter the author returned and deleted again, or whose days are over, stays deleted; an added one the
// author deleted already is not named.
func TestARevertDoesNotReturnAChapterDeletedAgain(t *testing.T) {
	snapshot, book := updatedBook()
	book[4].Generation = 3
	book[1] = ChapterNow{ID: "d", Title: "Новая", Order: 2, Revision: 1, Generation: 2, Restorable: true}
	plan := PlanBookRevert(revertAt, snapshot, book)
	if got := stays(plan); got["c"] != StayDeleted || len(got) != 1 {
		t.Fatalf("kept = %v", got)
	}
	if _, removed := actions(plan)["d"]; removed {
		t.Fatalf("an added chapter deleted already is removed again: %v", plan.Steps)
	}
	if !slices.Equal(plan.Order, []string{"a", "b", "e"}) {
		t.Fatalf("order = %v", plan.Order)
	}
}

// A chapter added after the update stays where it stands; the fingerprint changes with any change of the book.
func TestARevertKeepsAChapterAddedAfterTheUpdateAndKnowsTheBookChanged(t *testing.T) {
	snapshot, book := updatedBook()
	before := PlanBookRevert(revertAt, snapshot, book).Fingerprint
	if again := PlanBookRevert(revertAt, snapshot, slices.Clone(book)).Fingerprint; again != before {
		t.Fatalf("the same book has another fingerprint")
	}
	book = append(book, ChapterNow{ID: "z", Title: "Из «Письма»", Order: 5, Revision: 1, Generation: 1, Live: true})
	plan := PlanBookRevert(revertAt, snapshot, book)
	if plan.Fingerprint == before {
		t.Fatalf("a new chapter keeps the fingerprint")
	}
	if !slices.Equal(plan.Order, []string{"a", "b", "z", "c", "e"}) {
		t.Fatalf("order = %v, want the new chapter after «b», which it follows now", plan.Order)
	}
	book[0].Title = "Глава первая"
	if PlanBookRevert(revertAt, snapshot, book).Fingerprint == plan.Fingerprint {
		t.Fatalf("a new title keeps the fingerprint")
	}
}

func TestUpdateChangeOfAChapter(t *testing.T) {
	cases := []struct {
		pairing         ChapterPairing
		moved, restyled bool
		want            UpdateChange
	}{
		{ChapterPairing{Book: -1}, false, false, UpdateAdded},
		{ChapterPairing{Book: 0, Change: TextWords, Renamed: true}, true, false, UpdateWords},
		{ChapterPairing{Book: 0, Change: TextPunctuation}, false, false, UpdatePunctuation},
		{ChapterPairing{Book: 0, Change: TextSame, Renamed: true}, false, false, UpdatePlaced},
		{ChapterPairing{Book: 0, Change: TextSame}, true, false, UpdatePlaced},
		{ChapterPairing{Book: 0, Change: TextSame}, false, true, UpdatePlaced},
		{ChapterPairing{Book: 0, Change: TextSame}, false, false, UpdateSame},
	}
	for _, test := range cases {
		if got := UpdateChangeOf(test.pairing, test.moved, test.restyled); got != test.want {
			t.Errorf("UpdateChangeOf(%+v, %v, %v) = %s, want %s", test.pairing, test.moved, test.restyled, got, test.want)
		}
	}
}

// BR2.1: the preview names each chapter by its title now, with the title it gets back, and why a kept one stays.
func TestTheRevertPreviewNamesTheChapters(t *testing.T) {
	snapshot, book := updatedBook()
	book[0].Revision = 3
	preview := PlanBookRevert(revertAt, snapshot, book).PreviewOf("book", "Том", revertAt, revertAt.Add(7*24*time.Hour))
	if len(preview.Text) != 0 || len(preview.Place) != 1 || preview.Place[0] != (RevertChapter{ID: "b", Title: "Глава вторая", PreviousTitle: "Глава 2"}) {
		t.Fatalf("text %v, place %v", preview.Text, preview.Place)
	}
	if len(preview.Returned) != 1 || preview.Returned[0].Title != "Глава 3" || len(preview.Removed) != 1 || preview.Removed[0].Title != "Новая" {
		t.Fatalf("returned %v, removed %v", preview.Returned, preview.Removed)
	}
	if len(preview.Kept) != 1 || preview.Kept[0] != (RevertChapter{ID: "a", Title: "Глава 1", Stay: StayEdited}) {
		t.Fatalf("kept %v", preview.Kept)
	}
	if preview.Fingerprint == "" || preview.BookTitle != "Том" {
		t.Fatalf("preview = %+v", preview)
	}
}
