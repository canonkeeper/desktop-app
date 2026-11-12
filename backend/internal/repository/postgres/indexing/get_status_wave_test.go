package indexing

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

// waveSnapshot is a saga of two books whose published text has cards of 15 chapters out of 20 while the cards Job runs.
func waveSnapshot(wave *statusWave) statusSnapshot {
	return statusSnapshot{
		role: projects.RoleEditor,
		views: map[governance.SourceView]statusViewSnapshot{
			governance.SourceViewPublished: {revision: 3, books: []statusBookSnapshot{
				{id: "book-1", chapters: 12, indexed: 12, carded: 12},
				{id: "book-2", chapters: 8, indexed: 8, carded: 3},
			}},
		},
		jobs: []jobs.Job{statusCardsJob("cards-job", jobs.StatusRunning, governance.SourceViewPublished, statusTestTime)},
		wave: wave,
	}
}

func readySnapshot(wave *statusWave) statusSnapshot {
	snapshot := waveSnapshot(wave)
	snapshot.views[governance.SourceViewPublished].books[1].carded = 8
	snapshot.jobs = nil
	return snapshot
}

func statusOf(t *testing.T, snapshot statusSnapshot) canon.IndexingStatusResponse {
	t.Helper()
	response, err := buildStatusResponse(snapshot, statusScope{})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// specs/2026-11-09-first-session.md, FS5.1, FS5.2: while the wave is open and the text of the site is prepared, the
// status says how long the chapters without cards will take by the pace of the latest cards — 5 cards in 4 minutes,
// 5 chapters left: 240 seconds — and says no number without a pace or while a new book is still to be imported.
func TestTheStatusEstimatesWhatIsLeftOfAnOpenWave(t *testing.T) {
	now := statusTestTime.Add(4 * time.Minute)
	response := statusOf(t, waveSnapshot(&statusWave{startedAt: statusTestTime, now: now, recentCards: 5}))
	if response.Preparation == nil || response.Preparation.RemainingSeconds == nil || *response.Preparation.RemainingSeconds != 240 ||
		response.SagaReady != nil {
		t.Fatalf("an open wave with a pace = %+v, %+v", response.Preparation, response.SagaReady)
	}

	// A wave that goes on for an hour is paced by its last window only.
	long := statusOf(t, waveSnapshot(&statusWave{startedAt: statusTestTime.Add(-time.Hour), now: now, recentCards: 10}))
	want := int(indexingdomain.PaceWindow.Seconds()) / 10 * 5
	if long.Preparation == nil || long.Preparation.RemainingSeconds == nil || *long.Preparation.RemainingSeconds != want {
		t.Fatalf("a long wave = %+v, want %d seconds", long.Preparation, want)
	}

	for name, wave := range map[string]*statusWave{
		"two cards are no pace":         {startedAt: statusTestTime, now: now, recentCards: 2},
		"a new book is still to import": {startedAt: statusTestTime, now: now, recentCards: 5, importing: true},
		"the wave has just started":     {startedAt: now, now: now, recentCards: 5},
	} {
		response := statusOf(t, waveSnapshot(wave))
		if response.Preparation == nil || response.Preparation.RemainingSeconds != nil {
			t.Fatalf("%s: preparation = %+v, want one without a number", name, response.Preparation)
		}
	}

	// No open wave has nothing to estimate; a text that is not prepared now has no preparation.
	if response := statusOf(t, waveSnapshot(nil)); response.Preparation == nil || response.Preparation.RemainingSeconds != nil || response.SagaReady != nil {
		t.Fatalf("no wave = %+v, %+v", response.Preparation, response.SagaReady)
	}
	if response := statusOf(t, readySnapshot(&statusWave{startedAt: statusTestTime, now: now, recentCards: 5})); response.Preparation != nil {
		t.Fatalf("a ready text with an open wave = %+v", response.Preparation)
	}
	// Between the volumes of an import queue the text there is is ready, and the saga is still prepared.
	between := statusOf(t, readySnapshot(&statusWave{startedAt: statusTestTime, now: now, recentCards: 5, importing: true, recipient: true}))
	if between.Preparation == nil || between.Preparation.RemainingSeconds != nil || !between.Preparation.LetterPromised || between.SagaReady != nil {
		t.Fatalf("between the volumes of a queue = %+v, %+v; want a preparation without a number", between.Preparation, between.SagaReady)
	}
	partial := waveSnapshot(&statusWave{startedAt: statusTestTime, now: now, recentCards: 5})
	partial.jobs = nil
	if response := statusOf(t, partial); response.Preparation != nil {
		t.Fatalf("a text nothing works on = %+v", response.Preparation)
	}
}

// The walk of TEST 11.11: one chapter added from «Письмо» was read under «обычно 10–30 минут» — the words of a whole
// book. A few chapters without cards have their time said without a pace and without a wave: a minute a chapter.
func TestTheStatusSaysTheTimeOfAFewChaptersWithoutAPace(t *testing.T) {
	now := statusTestTime.Add(4 * time.Minute)
	few := func(wave *statusWave, carded int) *canon.IndexingPreparation {
		snapshot := waveSnapshot(wave)
		snapshot.views[governance.SourceViewPublished].books[1].carded = carded
		return statusOf(t, snapshot).Preparation
	}
	for name, test := range map[string]struct {
		wave   *statusWave
		carded int
		want   int
	}{
		"one chapter, no wave":                {nil, 7, 60},
		"three chapters, a wave without pace": {&statusWave{startedAt: statusTestTime, now: now, recentCards: 2}, 5, 180},
		"two chapters, a wave just started":   {&statusWave{startedAt: now, now: now}, 6, 120},
	} {
		if reading := few(test.wave, test.carded); reading == nil || reading.RemainingSeconds == nil || *reading.RemainingSeconds != test.want {
			t.Fatalf("%s = %+v, want %d seconds", name, reading, test.want)
		}
	}
	// Four chapters are a book's worth: no number without a pace. A new book still to import has chapters nobody counted.
	if reading := few(nil, 4); reading == nil || reading.RemainingSeconds != nil {
		t.Fatalf("four chapters without a pace = %+v, want no number", reading)
	}
	if reading := few(&statusWave{startedAt: statusTestTime, now: now, importing: true}, 7); reading == nil || reading.RemainingSeconds != nil {
		t.Fatalf("one chapter while a new book is to import = %+v, want no number", reading)
	}
	// A measured pace is said as it is, for a few chapters too.
	if reading := few(&statusWave{startedAt: statusTestTime, now: now, recentCards: 4}, 7); reading == nil || reading.RemainingSeconds == nil || *reading.RemainingSeconds != 60 {
		t.Fatalf("one chapter by the pace of 4 cards in 4 minutes = %+v, want 60 seconds", reading)
	}
}

// specs/2026-11-10-ux3-saga-state.md SS1.1, SS1.4: the preparation counts the reading of the saga in the text the site
// reads by the counters the wave closes on — chapters with their retelling, books read through, the first book not read
// and whether the search already finds a chapter — not by its Jobs.
func TestTheStatusCountsTheReadingOfTheSaga(t *testing.T) {
	now := statusTestTime.Add(4 * time.Minute)
	reading := statusOf(t, waveSnapshot(&statusWave{startedAt: statusTestTime, now: now, recentCards: 5})).Preparation
	if reading == nil || reading.ChaptersRead != 15 || reading.ChaptersTotal != 20 || reading.BooksRead != 1 || reading.BooksTotal != 2 ||
		reading.CurrentBookID == nil || *reading.CurrentBookID != "book-2" || !reading.Askable {
		t.Fatalf("two books, 15 of 20 chapters read = %+v", reading)
	}
	wantBooks := []canon.IndexingPreparationBook{
		{BookID: "book-1", ChaptersRead: 12, ChaptersTotal: 12}, {BookID: "book-2", ChaptersRead: 3, ChaptersTotal: 8},
	}
	if len(reading.Books) != len(wantBooks) || reading.Books[0] != wantBooks[0] || reading.Books[1] != wantBooks[1] {
		t.Fatalf("books = %+v, want %+v", reading.Books, wantBooks)
	}

	// Nothing is searched yet: a question waits a couple of minutes.
	fresh := waveSnapshot(&statusWave{startedAt: statusTestTime, now: now})
	fresh.views[governance.SourceViewPublished] = statusViewSnapshot{revision: 3, books: []statusBookSnapshot{
		{id: "book-1", chapters: 12}, {id: "book-empty"},
	}}
	if preparation := statusOf(t, fresh).Preparation; preparation == nil || preparation.Askable || preparation.ChaptersRead != 0 ||
		preparation.ChaptersTotal != 12 || preparation.BooksTotal != 1 || len(preparation.Books) != 1 ||
		preparation.CurrentBookID == nil || *preparation.CurrentBookID != "book-1" {
		t.Fatalf("a book not searched yet = %+v", preparation)
	}

	// Between the volumes of a queue every book there is is read: the next file is still parsed, no book is current.
	between := statusOf(t, readySnapshot(&statusWave{startedAt: statusTestTime, now: now, importing: true})).Preparation
	if between == nil || between.CurrentBookID != nil || between.ChaptersRead != 20 || between.BooksRead != 2 || !between.Askable {
		t.Fatalf("a file parsed after the books read = %+v", between)
	}

	// A text read again without an open wave — «Обновить из файла», «Продолжить» after the wave closed — is counted the
	// same, without the estimate and the letter, which belong to a wave.
	readyAt := statusTestTime.Add(-time.Hour)
	for name, wave := range map[string]*statusWave{
		"no wave":       nil,
		"a closed wave": {startedAt: statusTestTime.Add(-2 * time.Hour), readyAt: &readyAt, now: now, recipient: true},
	} {
		again := statusOf(t, waveSnapshot(wave))
		if again.Preparation == nil || again.Preparation.RemainingSeconds != nil || again.Preparation.LetterPromised ||
			again.Preparation.ChaptersRead != 15 || again.SagaReady != nil {
			t.Fatalf("%s: a text read again = %+v, %+v", name, again.Preparation, again.SagaReady)
		}
	}
}

// FS5.6: the letter is promised to whom the wave will be told to, once the wave lasts long enough to be worth one.
func TestTheStatusPromisesTheLetterToItsRecipientAfterTwoMinutes(t *testing.T) {
	cases := []struct {
		name      string
		lasted    time.Duration
		recipient bool
		promised  bool
	}{
		{"the recipient of a long wave", indexingdomain.ReadyLetterAfter + time.Second, true, true},
		{"exactly two minutes", indexingdomain.ReadyLetterAfter, true, false},
		{"a young wave", time.Minute, true, false},
		{"another member of the saga", time.Hour, false, false},
	}
	for _, test := range cases {
		wave := &statusWave{startedAt: statusTestTime, now: statusTestTime.Add(test.lasted), recipient: test.recipient}
		response := statusOf(t, waveSnapshot(wave))
		if response.Preparation == nil || response.Preparation.LetterPromised != test.promised {
			t.Fatalf("%s: preparation = %+v, want promised %v", test.name, response.Preparation, test.promised)
		}
	}
}

// FS4.1: a closed wave is told for seven days while the text of the site is ready, with what is read now; a saga that
// is prepared again or has lost its chapters says nothing.
func TestTheStatusTellsOfAClosedWaveForSevenDays(t *testing.T) {
	readyAt := statusTestTime.Add(20 * time.Minute)
	closed := func(age time.Duration) *statusWave {
		return &statusWave{startedAt: statusTestTime, readyAt: &readyAt, now: readyAt.Add(age), recipient: true}
	}
	response := statusOf(t, readySnapshot(closed(time.Hour)))
	if response.SagaReady == nil || !response.SagaReady.At.Equal(readyAt) || response.SagaReady.Books != 2 || response.SagaReady.Chapters != 20 ||
		response.Preparation != nil {
		t.Fatalf("a closed wave = %+v, %+v", response.SagaReady, response.Preparation)
	}
	if response := statusOf(t, readySnapshot(closed(indexingdomain.ReadyShownFor))); response.SagaReady == nil {
		t.Fatal("a wave closed exactly seven days ago is not told")
	}
	if response := statusOf(t, readySnapshot(closed(indexingdomain.ReadyShownFor+time.Second))); response.SagaReady != nil {
		t.Fatalf("a wave closed more than seven days ago = %+v", response.SagaReady)
	}
	// The saga is prepared again — «Обновить из файла» — after the wave closed: its readiness is not told meanwhile, the
	// reading is, without the estimate and the letter of a wave (specs/2026-11-10-ux3-saga-state.md SS1.1).
	if response := statusOf(t, waveSnapshot(closed(time.Hour))); response.SagaReady != nil || response.Preparation == nil ||
		response.Preparation.LetterPromised || response.Preparation.RemainingSeconds != nil {
		t.Fatalf("a closed wave of a text in work = %+v, %+v", response.SagaReady, response.Preparation)
	}
	// A book without chapters is not read; a saga without chapters is not ready to be asked.
	empty := readySnapshot(closed(time.Hour))
	empty.views[governance.SourceViewPublished] = statusViewSnapshot{revision: 4, books: []statusBookSnapshot{{id: "book-3"}}}
	if response := statusOf(t, empty); response.SagaReady != nil {
		t.Fatalf("a saga without chapters = %+v", response.SagaReady)
	}
	withEmpty := readySnapshot(closed(time.Hour))
	view := withEmpty.views[governance.SourceViewPublished]
	view.books = append(view.books, statusBookSnapshot{id: "book-3"})
	withEmpty.views[governance.SourceViewPublished] = view
	if response := statusOf(t, withEmpty); response.SagaReady == nil || response.SagaReady.Books != 2 || response.SagaReady.Chapters != 20 {
		t.Fatalf("a saga with an empty book = %+v", response.SagaReady)
	}
}
