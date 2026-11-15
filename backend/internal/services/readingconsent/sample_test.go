// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/jobs"
)

type sampleStoreOfTheTest struct {
	last     *SampleJob
	queued   [][]Chapter
	keys     []string
	requests []SampleRequest
}

func (store *sampleStoreOfTheTest) LastSample(context.Context, string) (*SampleJob, error) {
	return store.last, nil
}

func (store *sampleStoreOfTheTest) QueueSample(_ context.Context, _ string, host string, chapters []Chapter, key string, _ time.Time) (string, error) {
	store.queued, store.keys = append(store.queued, chapters), append(store.keys, key)
	store.last = &SampleJob{JobID: "sample-job", Status: "queued", Chapters: chapters, Host: host}
	return "sample-job", nil
}

func (store *sampleStoreOfTheTest) SampleRequests(context.Context, string) ([]SampleRequest, error) {
	return store.requests, nil
}

type sampleCardsOfTheTest struct{ carded map[string]bool }

func (cards sampleCardsOfTheTest) Cards(_ context.Context, _ string, chapters []Chapter) ([]SampleCard, error) {
	var shown []SampleCard
	for _, chapter := range chapters {
		if cards.carded[chapter.ID] {
			shown = append(shown, SampleCard{ChapterID: chapter.ID, Place: "Книга 1, гл. 1", Summary: "Пересказ", Characters: 2_000})
		}
	}
	return shown, nil
}

type cancelOfTheTest struct{ canceled []string }

func (cancel *cancelOfTheTest) CancelJob(_ context.Context, _ string, jobID string) error {
	cancel.canceled = append(cancel.canceled, jobID)
	return nil
}

// specs/2026-11-13-desk-consent.md CN2.1, CN2.3, CN2.4: the sample chooses the first chapters without a card, three at most,
// under the key of the request; a live sample answers itself; a consent that acts and a saga with nothing to read refuse;
// its state, its failure by the code of its Job, its time without the idle time between its runs and what it spent are
// told; «Остановить» cancels its Job.
func TestTheSampleOfThreeChapters(t *testing.T) {
	settings := settingsOfTheTest{settings: pricedSettings(t), connected: true}
	store := &sampleStoreOfTheTest{}
	carded := map[string]bool{}
	cancel := &cancelOfTheTest{}
	samples := Samples{
		Settings: settings, Owners: ownersFake{}, Consents: consentsOfTheTest{}, Store: store,
		Saga:  sagaFake{toRead: ToRead{Unread: []Chapter{{ID: "c1", Hash: "h1"}, {ID: "c2", Hash: "h2"}, {ID: "c3", Hash: "h3"}, {ID: "c4", Hash: "h4"}}}},
		Cards: sampleCardsOfTheTest{carded: carded}, Cancel: cancel, Now: time.Now,
	}
	if sample, _ := samples.Get(context.Background(), "author", "saga"); sample.State != SampleNone || sample.Total != 0 {
		t.Fatalf("before any = %+v", sample)
	}
	sample, err := samples.Start(context.Background(), "author", "saga", "key-1")
	if err != nil || sample.State != SampleQueued || sample.Total != 3 || len(store.queued) != 1 || len(store.queued[0]) != 3 ||
		store.queued[0][2].ID != "c3" || store.keys[0] != "key-1" || store.last.Host != "api.deepseek.com" {
		t.Fatalf("the start = %+v, %v, queued %v", sample, err, store.queued)
	}
	if again, err := samples.Start(context.Background(), "author", "saga", "key-2"); err != nil || again.State != SampleQueued || len(store.queued) != 1 {
		t.Fatalf("a second press while it lives = %+v, %v, queued %d", again, err, len(store.queued))
	}
	if err := samples.Stop(context.Background(), "author", "saga"); err != nil || len(cancel.canceled) != 1 || cancel.canceled[0] != "sample-job" {
		t.Fatalf("the stop = %v, canceled %v", err, cancel.canceled)
	}

	carded["c1"] = true
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store.last.Status, store.last.ErrorCode = "failed", "reading.funds"
	store.requests = []SampleRequest{
		{At: start, Duration: 60 * time.Second, Host: "api.deepseek.com", Model: "deepseek-reasoner", TokensKnown: true,
			Tokens: aisettings.Tokens{Input: 10_000, Output: 1_000}, Cost: &SampleCost{Micros: 420_000, Currency: "RUB"}},
		{At: start.Add(30 * time.Second), Duration: 60 * time.Second, Host: "api.deepseek.com", Model: "deepseek-reasoner", Estimated: true,
			Cost: &SampleCost{Micros: 100_000, Currency: "RUB"}},
		// A second run of the Job an hour later: the hour is not its time.
		{At: start.Add(time.Hour), Duration: 30 * time.Second, Host: "api.deepseek.com", Model: "deepseek-reasoner", TokensKnown: true,
			Tokens: aisettings.Tokens{Input: 1_000, Output: 100}},
	}
	sample, _ = samples.Get(context.Background(), "author", "saga")
	if sample.State != SampleFailed || sample.Failure != "funds" || sample.Done != 1 || sample.Total != 3 || sample.Seconds != 90+30 ||
		sample.Tokens.Input != 11_000 || !sample.Approximate || sample.Amount == nil || *sample.Amount != 420_000+100_000+30_000+12_000 ||
		sample.Unit != domain.Unit(aisettings.CurrencyRUB) {
		t.Fatalf("a sample that failed = %+v %v", sample, sample.Amount)
	}

	store.last.Status, store.last.ErrorCode = "succeeded", ""
	if _, err := samples.Start(context.Background(), "author", "saga", "key-3"); err != nil || len(store.queued) != 2 {
		t.Fatalf("a new sample after the last = %v, queued %d", err, len(store.queued))
	}
	given := samples
	given.Consents = consentsOfTheTest{consent: &domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StatePaused, Pause: domain.PauseAuthor}}
	var conflict *Conflict
	if _, err := given.Start(context.Background(), "author", "saga", "key-4"); !errors.As(err, &conflict) || conflict.Code != "reading.consent_given" {
		t.Fatalf("a sample under a consent = %v", err)
	}
	empty := samples
	empty.Store = &sampleStoreOfTheTest{}
	empty.Saga = sagaFake{}
	if _, err := empty.Start(context.Background(), "author", "saga", "key-5"); !errors.As(err, &conflict) || conflict.Code != "reading.nothing_to_read" {
		t.Fatalf("a sample of nothing = %v", err)
	}
	disconnected := samples
	disconnected.Settings = settingsOfTheTest{settings: pricedSettings(t)}
	if _, err := disconnected.Start(context.Background(), "author", "saga", "key-6"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("a sample without the helper = %v", err)
	}
}

// CN1.5: the last sample that read measures its provider, its model, its chapters with their characters, the tokens and the
// time of a chapter one request after another; a request of another model, or one without usage, measures nothing.
func TestTheSampleMeasuresTheEstimate(t *testing.T) {
	store := &sampleStoreOfTheTest{last: &SampleJob{JobID: "sample-job", Status: "succeeded", Chapters: []Chapter{{ID: "c1", Hash: "h1"}, {ID: "c2", Hash: "h2"}}}}
	samples := Samples{Store: store, Cards: sampleCardsOfTheTest{carded: map[string]bool{"c1": true, "c2": true}}}
	store.requests = []SampleRequest{
		{Duration: 50 * time.Second, Host: "api.deepseek.com", Model: "reader", TokensKnown: true, Tokens: aisettings.Tokens{Input: 15_000, Output: 6_000}},
		{Duration: 40 * time.Second, Host: "api.deepseek.com", Model: "reader", TokensKnown: true, Tokens: aisettings.Tokens{Input: 16_000, Output: 1_000}},
	}
	measured, err := samples.Measured(context.Background(), "saga")
	if err != nil || measured == nil || measured.Chapters != 2 || measured.Characters != 4_000 || measured.Tokens.Input != 31_000 ||
		measured.Seconds != 90 || measured.Model != "reader" {
		t.Fatalf("the measure = %+v, %v", measured, err)
	}
	store.requests[1].Model = "writer"
	if measured, _ := samples.Measured(context.Background(), "saga"); measured != nil {
		t.Fatalf("a measure of two models = %+v", measured)
	}
	store.requests[1].Model, store.requests[1].TokensKnown = "reader", false
	if measured, _ := samples.Measured(context.Background(), "saga"); measured != nil {
		t.Fatalf("a measure without usage = %+v", measured)
	}
}

type ownersFake struct{}

func (ownersFake) Owned(context.Context, string, string) error { return nil }

// CN2.1: the sample reads as many chapters as the subject of its Job holds — the rule of the app and the bound of the core
// are one number.
func TestTheSampleReadsWhatItsSubjectHolds(t *testing.T) {
	if domain.SampleChapters != jobs.MaxSampleChapters {
		t.Fatalf("the sample reads %d chapters, its subject holds %d", domain.SampleChapters, jobs.MaxSampleChapters)
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.4: a sample whose provider did not answer through the repeats of a request
// ends with the stop «провайдер не отвечает» and says its own words of before — «ответ помощника не пришёл», the reason other;
// the reasons of its other stops are theirs.
func TestASampleTheProviderDidNotAnswerSaysItsOwnWords(t *testing.T) {
	for code, want := range map[string]string{
		"reading.no_answer": "other", "reading.funds": "funds", "reading.connection": "connection", "reading.paused": "paused",
		"cards.consent": "helper", "cards.incomplete": "other",
	} {
		if got := failureOf(SampleJob{Status: "failed", ErrorCode: code}); got != want {
			t.Errorf("a sample that ended %s = %q; want %q", code, got, want)
		}
	}
}
