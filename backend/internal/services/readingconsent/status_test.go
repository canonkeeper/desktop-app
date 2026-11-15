// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/canon"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

type sagaFake struct {
	toRead ToRead
	input  int64
}

func (saga sagaFake) ToRead(context.Context, string, bool) (ToRead, error) { return saga.toRead, nil }

func (saga sagaFake) Inputs(context.Context, string, []Chapter) (int64, error) {
	return saga.input, nil
}

type paceFake int

func (pace paceFake) CardsSince(context.Context, string, time.Time) (int, error) {
	return int(pace), nil
}

func chaptersOf(count int) []Chapter {
	chapters := make([]Chapter, count)
	for i := range chapters {
		chapters[i] = Chapter{ID: "c", Hash: "h", Characters: 10_000}
	}
	return chapters
}

// specs/2026-11-13-desk-consent.md CN3.8: the status says what the reading waits for and its state — waits for the consent
// with «провайдер сменился», goes with what was spent and the time by the pace since the last continuation (a pause is not
// in it), stands with its reason — «сумма» also when the chapters are read and the names and threads wait —, is done with
// the chapters that did not read; a sum in money whose currency changed stands at «цены изменились». A saga with nothing to
// read and nothing paused has no state.
func TestTheStatusSaysTheStateOfTheReading(t *testing.T) {
	resumed := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	now := resumed.Add(30 * time.Minute)
	settings := settingsOfTheTest{settings: pricedSettings(t), connected: true}
	stop, step := int64(1_000_000_000), int64(1_000_000_000)
	going := func() *domain.Consent {
		return &domain.Consent{
			Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.Unit(aisettings.CurrencyRUB),
			StopAt: &stop, Step: &step, Since: resumed.Add(-24 * time.Hour), ResumedAt: resumed, Spent: domain.Spent{Micros: 240_000_000, Estimated: true},
		}
	}
	counts := canon.IndexingPreparation{ChaptersRead: 212, ChaptersTotal: 612}
	state := func(consent *domain.Consent, toRead ToRead, pace int) (canon.PreparationWaiting, *canon.IndexingReading) {
		t.Helper()
		status := Status{Settings: settings, Consents: consentsOfTheTest{consent: consent}, Saga: sagaFake{toRead: toRead}, Pace: paceFake(pace),
			Now: func() time.Time { return now }}
		waiting, err := status.Waiting(context.Background(), "saga")
		if err != nil {
			t.Fatal(err)
		}
		reading, err := status.State(context.Background(), "saga", counts)
		if err != nil {
			t.Fatal(err)
		}
		return waiting, reading
	}

	waiting, reading := state(going(), ToRead{Unread: chaptersOf(400)}, 100)
	if waiting != "" || reading.State != canon.ReadingGoing || reading.ChaptersRead != 212 || reading.Spent != 240_000_000 || !reading.Approximate ||
		*reading.StopAt != stop || *reading.Step != step || reading.Unit != "RUB" || reading.RemainingSeconds == nil || *reading.RemainingSeconds != 400*1800/100 {
		t.Fatalf("going = %q %+v", waiting, reading)
	}
	// Two cards since the continuation are no pace: the estimate says the time.
	// ⌈400/4⌉·120 s of the chapters, the names and the layer by the formula: the time does not hang on the characters.
	estimate := domain.Count(domain.Input{Unread: make([]int, 400), Parallel: 4, Reasoning: true}).Seconds
	if _, reading = state(going(), ToRead{Unread: chaptersOf(400)}, 2); reading.RemainingSeconds == nil || *reading.RemainingSeconds != estimate {
		t.Fatalf("the time by the estimate = %v", reading.RemainingSeconds)
	}

	paused := going()
	paused.State, paused.Pause = domain.StatePaused, domain.PauseSum
	waiting, reading = state(paused, ToRead{NamesWait: true, ThreadsWait: true}, 0)
	if waiting != canon.PreparationWaitingPaused || reading.State != canon.ReadingPaused || *reading.Pause != "sum" || !reading.NamesWait || !reading.ThreadsWait {
		t.Fatalf("paused at the sum with the chapters read = %q %+v", waiting, reading)
	}
	if _, reading = state(paused, ToRead{}, 0); reading == nil || reading.State != canon.ReadingPaused {
		t.Fatalf("a pause with nothing to read = %+v", reading)
	}

	moved := going()
	moved.Host = "api.openai.com"
	waiting, reading = state(moved, ToRead{Unread: chaptersOf(400)}, 0)
	if waiting != canon.PreparationWaitingConsent || reading.State != canon.ReadingWaitingConsent || reading.Reason == nil || *reading.Reason != ReasonProviderChanged {
		t.Fatalf("another provider = %q %+v", waiting, reading)
	}
	if _, reading = state(nil, ToRead{}, 0); reading != nil {
		t.Fatalf("nothing to read without a consent = %+v", reading)
	}

	waiting, reading = state(going(), ToRead{Marked: []Unread{{ID: "c", Hash: "h", Reason: "cut_answer"}}}, 0)
	if waiting != "" || reading.State != canon.ReadingDone || reading.Unread == nil || reading.Unread.Chapters != 1 || reading.Unread.Reason != "cut_answer" {
		t.Fatalf("done but for a chapter = %q %+v", waiting, reading)
	}
	if _, reading = state(going(), ToRead{}, 0); reading != nil {
		t.Fatalf("all read = %+v", reading)
	}
	// Chapters that did not read for both reasons: the status names the reason of most of them, the same at every poll.
	mixed := ToRead{Marked: []Unread{{ID: "a", Reason: "cut_answer"}, {ID: "b", Reason: "unreadable"}, {ID: "c", Reason: "unreadable"}}}
	if _, reading = state(going(), mixed, 0); reading.Unread == nil || reading.Unread.Chapters != 3 || reading.Unread.Reason != "unreadable" {
		t.Fatalf("chapters that did not read for both reasons = %+v", reading.Unread)
	}
	mixed.Marked = mixed.Marked[:2]
	if _, reading = state(going(), mixed, 0); reading.Unread.Reason != "cut_answer" {
		t.Fatalf("as many of each = %+v", reading.Unread)
	}

	dollars := going()
	dollars.Unit = domain.Unit(aisettings.CurrencyUSD)
	waiting, reading = state(dollars, ToRead{Unread: chaptersOf(10)}, 0)
	if waiting != canon.PreparationWaitingPaused || reading.State != canon.ReadingPaused || *reading.Pause != "prices" {
		t.Fatalf("the currency changed under a sum in money = %q %+v", waiting, reading)
	}
	// A pause of another reason under a sum in money whose currency changed says «цены изменились»: «Продолжить ещё на X»
	// would read X in the old currency, the author names the sum again.
	dollars.State, dollars.Pause = domain.StatePaused, domain.PauseSum
	if _, reading = state(dollars, ToRead{Unread: chaptersOf(10)}, 0); *reading.Pause != "prices" {
		t.Fatalf("a pause at the sum after a change of the currency = %+v", reading)
	}

	// A consent without a sum follows the currency of the settings: no spent in a currency the reading no longer counts in.
	free := going()
	free.StopAt, free.Step, free.Unit = nil, nil, domain.Unit(aisettings.CurrencyUSD)
	waiting, reading = state(free, ToRead{Unread: chaptersOf(10)}, 0)
	if waiting != "" || reading.State != canon.ReadingGoing || reading.Unit != "RUB" || reading.Spent != 0 {
		t.Fatalf("a consent without a sum after a change of the currency = %q %+v", waiting, reading)
	}
	// A sum in tokens holds when the author names prices: the reading goes, counted in tokens.
	tokens, inTokens := int64(25_000_000), going()
	inTokens.Unit, inTokens.StopAt, inTokens.Step = domain.UnitTokens, &tokens, &tokens
	inTokens.Spent = domain.Spent{Tokens: aisettings.Tokens{Input: 3_000_000, Output: 1_000_000}}
	waiting, reading = state(inTokens, ToRead{Unread: chaptersOf(10)}, 0)
	if waiting != "" || reading.State != canon.ReadingGoing || reading.Unit != "tokens" || reading.Spent != 4_000_000 || *reading.StopAt != tokens {
		t.Fatalf("a sum in tokens with prices = %q %+v", waiting, reading)
	}
}

// specs/2026-11-15-desk-reading-rest.md RR2.1, RR2.2: the status says the names and the threads apart — what waits for the
// model and why what did not come together did not — only once no chapter is left to read; under a consent that acts and
// with nothing to wait for, what did not read or come together is done with its reasons — also when the stop sum is spent;
// a pause says its pause; without a consent that acts the marks do not act, everything waits, and the reading waits for the
// consent.
func TestTheStatusSaysTheNamesAndTheThreadsApart(t *testing.T) {
	settings := settingsOfTheTest{settings: pricedSettings(t), connected: true}
	stop := int64(1_000_000_000)
	consent := func(spent int64) *domain.Consent {
		return &domain.Consent{
			Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.Unit(aisettings.CurrencyRUB),
			StopAt: &stop, Step: &stop, Spent: domain.Spent{Micros: spent},
		}
	}
	state := func(consent *domain.Consent, toRead ToRead) *canon.IndexingReading {
		t.Helper()
		status := Status{Settings: settings, Consents: consentsOfTheTest{consent: consent}, Saga: sagaFake{toRead: toRead}, Pace: paceFake(0),
			Now: time.Now}
		reading, err := status.State(context.Background(), "saga", canon.IndexingPreparation{ChaptersRead: 612, ChaptersTotal: 612})
		if err != nil {
			t.Fatal(err)
		}
		return reading
	}
	reason := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	for name, step := range map[string]struct {
		toRead                ToRead
		state, names, threads string
		namesWait, threadWait bool
	}{
		"the names marked, the threads built": {ToRead{NamesUnread: "unreadable"}, canon.ReadingDone, "unreadable", "", false, false},
		"both marked":                         {ToRead{NamesUnread: "refused", ThreadsUnread: "no_answer"}, canon.ReadingDone, "refused", "no_answer", false, false},
		"the chapters and the names":          {ToRead{Marked: []Unread{{ID: "c", Reason: "cut_answer"}}, NamesUnread: "cut_answer"}, canon.ReadingDone, "cut_answer", "", false, false},
		"the names wait":                      {ToRead{NamesWait: true}, canon.ReadingGoing, "", "", true, false},
		"only the threads wait":               {ToRead{ThreadsWait: true, NamesUnread: "unreadable"}, canon.ReadingGoing, "unreadable", "", false, true},
	} {
		reading := state(consent(0), step.toRead)
		if reading == nil || reading.State != step.state || reason(rest(reading).Names) != step.names || reason(rest(reading).Threads) != step.threads ||
			reading.NamesWait != step.namesWait || reading.ThreadsWait != step.threadWait {
			t.Errorf("%s = %+v rest %+v", name, reading, reading.UnreadRest)
		}
	}
	if reading := state(consent(0), ToRead{Unread: chaptersOf(3), NamesUnread: "unreadable", NamesWait: true}); reading.State != canon.ReadingGoing ||
		reading.UnreadRest != nil || reading.NamesWait || reading.ThreadsWait {
		t.Errorf("chapters still read = %+v", reading)
	}
	if reading := state(consent(stop), ToRead{NamesUnread: "unreadable"}); reading.State != canon.ReadingDone || reading.Spent != stop {
		t.Errorf("done at the spent sum = %+v", reading)
	}
	paused := consent(0)
	paused.State, paused.Pause = domain.StatePaused, domain.PauseAuthor
	if reading := state(paused, ToRead{NamesUnread: "unreadable"}); reading.State != canon.ReadingPaused || *reading.Pause != "author" {
		t.Errorf("a pause = %+v", reading)
	}
	moved := consent(0)
	moved.Host = "api.openai.com"
	// Without a consent that acts the saga reads its marked chapter again and waits for the names.
	if reading := state(moved, ToRead{Unread: chaptersOf(1), NamesWait: true}); reading.State != canon.ReadingWaitingConsent ||
		reading.Reason == nil || *reading.Reason != ReasonProviderChanged || reading.UnreadRest != nil {
		t.Errorf("another host = %+v", reading)
	}
	if reading := state(moved, ToRead{NamesWait: true}); reading.State != canon.ReadingWaitingConsent || !reading.NamesWait {
		t.Errorf("another host, the names wait = %+v", reading)
	}
}

func rest(reading *canon.IndexingReading) canon.IndexingReadingRest {
	if reading == nil || reading.UnreadRest == nil {
		return canon.IndexingReadingRest{}
	}
	return *reading.UnreadRest
}
