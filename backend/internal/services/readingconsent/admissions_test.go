// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// ledgerOfTheTest keeps the consent of one saga and its count as the repository does: the count moves only by Settle, the
// journal is apart and may fail.
type ledgerOfTheTest struct {
	mu          sync.Mutex
	consent     *domain.Consent
	rows        []modeljournal.Request
	counts      []*Count
	failJournal bool
	paused      []domain.Pause
	samples     []modeljournal.Request
}

func (ledger *ledgerOfTheTest) Consent(context.Context, string) (*domain.Consent, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.consent == nil {
		return nil, nil
	}
	copied := *ledger.consent
	return &copied, nil
}

func (ledger *ledgerOfTheTest) Pause(_ context.Context, _ string, since time.Time, pause domain.Pause, _ time.Time) (bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.consent == nil || ledger.consent.State != domain.StateAllowed || !ledger.consent.Since.Equal(since) {
		return false, nil
	}
	ledger.consent.State, ledger.consent.Pause = domain.StatePaused, pause
	ledger.paused = append(ledger.paused, pause)
	return true, nil
}

func (ledger *ledgerOfTheTest) Settle(_ context.Context, entry modeljournal.Request, count *Count) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.failJournal {
		return errors.New("the journal is gone")
	}
	ledger.rows = append(ledger.rows, entry)
	ledger.counts = append(ledger.counts, count)
	if count != nil && ledger.consent != nil && ledger.consent.Since.Equal(count.Since) {
		ledger.consent.Spent = ledger.consent.Spent.Add(count.Tokens, count.Cost, ledger.consent.Unit, count.Estimated)
	}
	return nil
}

// SettleSample writes the row of a request of the sample with its request of the sample, apart from the count.
func (ledger *ledgerOfTheTest) SettleSample(_ context.Context, _ string, entry modeljournal.Request) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.failJournal {
		return errors.New("the journal is gone")
	}
	ledger.rows = append(ledger.rows, entry)
	ledger.samples = append(ledger.samples, entry)
	return nil
}

type jobsOfTheTest map[string]ReadingJob

func (jobs jobsOfTheTest) ReadingJob(_ context.Context, jobID string) (ReadingJob, bool, error) {
	job, ok := jobs[jobID]
	return job, ok, nil
}

func pricedSettings(t *testing.T) aisettings.Settings {
	t.Helper()
	chat, reader, rubles, in, out := "deepseek-chat", "deepseek-reasoner", "RUB", "30", "120"
	maxOutput := 1_000
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &chat, CardsModel: &reader,
		Advanced: aisettings.AdvancedDraft{Currency: &rubles, PriceIn: &in, PriceOut: &out, MaxOutput: &maxOutput},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func admissionsOfTheTest(t *testing.T, ledger *ledgerOfTheTest) *Admissions {
	t.Helper()
	settings := settingsOfTheTest{settings: pricedSettings(t), connected: true}
	return &Admissions{
		Settings: settings, Ledger: ledger, Now: func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC) },
		Jobs: jobsOfTheTest{
			"cards":       {ProjectID: "saga", Kind: KindCards, Live: true},
			"layer":       {ProjectID: "saga", Kind: KindLayer, Live: true},
			"sample":      {ProjectID: "saga", Kind: KindCards, Live: true, Sample: true, SampleHost: "api.deepseek.com"},
			"old sample":  {ProjectID: "saga", Kind: KindCards, Live: true, Sample: true, SampleHost: "api.openai.com"},
			"ended cards": {ProjectID: "saga", Kind: KindCards},
		},
	}
}

func request(job, purpose string, settings aisettings.Settings) AdmitRequest {
	return AdmitRequest{JobID: job, Purpose: purpose, Host: "api.deepseek.com", Model: "deepseek-reasoner", RequestBytes: 15_000, Settings: settings}
}

// specs/2026-11-13-desk-consent.md CN3.3: a request of the reading goes only while what was spent with the reserves of the
// requests that go and its own reserve is within the sum — the reserve is the input by the size and the output the limit of
// an answer; the one that would go over pauses the consent at «сумма»; an answer replaces the reserve by its cost.
func TestNothingGoesOverTheSum(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	// A reserve: 15 000 bytes are 4 200 tokens in at the fallback 420, and 1 000 out — 4 200·30 + 1 000·120 = 246 000.
	const reserve = 246_000
	stop := int64(3 * reserve)
	ledger := &ledgerOfTheTest{consent: &domain.Consent{
		Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.Unit(aisettings.CurrencyRUB),
		StopAt: &stop, Since: since, ResumedAt: since,
	}}
	admissions := admissionsOfTheTest(t, ledger)
	settings := pricedSettings(t)
	var tickets []*Admitted
	for range 3 {
		ticket, refusal, err := admissions.Admit(context.Background(), request("cards", "card", settings))
		if err != nil || ticket == nil {
			t.Fatalf("a request within the sum = %q, %v", refusal, err)
		}
		tickets = append(tickets, ticket)
	}
	if ticket, refusal, _ := admissions.Admit(context.Background(), request("layer", "layer_verify", settings)); ticket != nil || refusal != string(domain.PauseSum) {
		t.Fatalf("a request over the sum went: %q", refusal)
	}
	if ledger.consent.State != domain.StatePaused || ledger.consent.Pause != domain.PauseSum {
		t.Fatalf("the consent over the sum = %+v", ledger.consent)
	}
	// The answers replace their reserves: 10 000 in and 100 out cost 312 000 each — more than the reserve.
	for _, ticket := range tickets {
		ticket.Settle(context.Background(), modeljournal.Request{
			Purpose: "card", Tokens: &modeljournal.Tokens{Input: 10_000, Output: 100}, Outcome: modeljournal.OutcomeCompleted,
			Cost: &modeljournal.Cost{Micros: 312_000, Currency: "RUB"},
		}, true)
	}
	if ledger.consent.Spent.Micros != 3*312_000 || len(ledger.rows) != 3 || admissions.goingOf("saga") != 0 {
		t.Fatalf("the count = %+v, rows %d, going %d", ledger.consent.Spent, len(ledger.rows), admissions.goingOf("saga"))
	}
}

// CN3.3: the count of the consent is its own — a row of the journal that failed leaves the reserve in what was spent; a
// request without usage is counted by an estimate with its mark; the chat, scan, the materials and the sample are never in
// it; a request of the reading under another Job, a Job that ended or a purpose of another Job goes nowhere.
func TestTheCountOfTheConsentIsItsOwn(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	ledger := &ledgerOfTheTest{consent: &domain.Consent{
		Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
	}}
	admissions := admissionsOfTheTest(t, ledger)
	settings := pricedSettings(t)
	ledger.failJournal = true
	ticket, _, _ := admissions.Admit(context.Background(), request("cards", "card", settings))
	ticket.Settle(context.Background(), modeljournal.Request{Purpose: "card", Tokens: &modeljournal.Tokens{Input: 100, Output: 10}, Outcome: modeljournal.OutcomeCompleted}, true)
	if going := admissions.goingOf("saga"); going != 4_200+1_000 {
		t.Fatalf("the reserve of a row that failed = %d", going)
	}
	ledger.failJournal = false
	ticket, _, _ = admissions.Admit(context.Background(), request("cards", "card_pass", settings))
	ticket.Settle(context.Background(), modeljournal.Request{Purpose: "card_pass", Outcome: modeljournal.OutcomeCanceled, RequestBytes: 15_000, OutputCharacters: 3_000}, true)
	if spent := ledger.consent.Spent; spent.Tokens.Input != 4_200 || spent.Tokens.Output != 1_260 || !spent.Estimated || !ledger.rows[0].Estimated || ledger.rows[0].Cost == nil {
		t.Fatalf("a request without usage = %+v, row %+v", spent, ledger.rows[0])
	}
	// A refusal of the provider before its answer took nothing; a stream torn after the provider began its answer — its
	// reasoning hidden, no text went by — took the input all the same: it is counted by the estimate.
	ticket, _, _ = admissions.Admit(context.Background(), request("cards", "card", settings))
	ticket.Settle(context.Background(), modeljournal.Request{Purpose: "card", Outcome: modeljournal.OutcomeFailed, RequestBytes: 15_000}, false)
	if spent := ledger.consent.Spent; spent.Tokens.Input != 4_200 || spent.Tokens.Output != 1_260 {
		t.Fatalf("a refusal before the answer was counted: %+v", spent)
	}
	ticket, _, _ = admissions.Admit(context.Background(), request("cards", "card", settings))
	ticket.Settle(context.Background(), modeljournal.Request{Purpose: "card", Outcome: modeljournal.OutcomeFailed, RequestBytes: 15_000}, true)
	if spent := ledger.consent.Spent; spent.Tokens.Input != 2*4_200 || spent.Tokens.Output != 1_260 {
		t.Fatalf("a stream torn after the answer began = %+v", spent)
	}
	for _, refused := range []AdmitRequest{
		request("sample", "names", settings), request("old sample", "card", settings), request("ended cards", "card", settings),
		request("layer", "card", settings), request("cards", "layer_book", settings), request("unknown", "card", settings),
	} {
		if ticket, refusal, err := admissions.Admit(context.Background(), refused); ticket != nil || refusal == "" || err != nil {
			t.Errorf("%+v went: %q %v", refused, refusal, err)
		}
	}
	before := ledger.consent.Spent
	ticket, _, _ = admissions.Admit(context.Background(), request("sample", "card", settings))
	if ticket == nil {
		t.Fatal("a request of the sample was refused")
	}
	ticket.Settle(context.Background(), modeljournal.Request{Purpose: "card", Tokens: &modeljournal.Tokens{Input: 500, Output: 50}, Outcome: modeljournal.OutcomeCompleted}, true)
	if ledger.consent.Spent != before || len(ledger.rows) != 4 || len(ledger.samples) != 1 {
		t.Fatalf("the sample moved the count: %+v, rows %d, requests of the sample %d", ledger.consent.Spent, len(ledger.rows), len(ledger.samples))
	}
}

// CN3.1, CN3.4: a consent of another host lets nothing go; the funds, the key and the model pause the reading at the first
// refusal, no connection only at the last attempt of the client — and a wait the provider asks for too
// (specs/2026-11-15-desk-reading-no-answer.md NA1.1); a refusal of the sample pauses nothing.
func TestARefusalOfTheProviderPausesTheReading(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	fresh := func() *domain.Consent {
		return &domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since}
	}
	settings := pricedSettings(t)
	other := fresh()
	other.Host = "api.openai.com"
	ledger := &ledgerOfTheTest{consent: other}
	if ticket, refusal, _ := admissionsOfTheTest(t, ledger).Admit(context.Background(), request("cards", "card", settings)); ticket != nil || refusal != "consent" {
		t.Fatalf("a consent of another host let a request go: %q", refusal)
	}
	for failure, want := range map[aisettings.Failure]domain.Pause{
		aisettings.FailureFunds: domain.PauseFunds, aisettings.FailureKey: domain.PauseKey, aisettings.FailureModel: domain.PauseModel,
	} {
		ledger := &ledgerOfTheTest{consent: fresh()}
		ticket, _, _ := admissionsOfTheTest(t, ledger).Admit(context.Background(), request("cards", "card", settings))
		ticket.Failed(context.Background(), failure, false)
		if ledger.consent.Pause != want {
			t.Errorf("%s paused %q", failure, ledger.consent.Pause)
		}
	}
	ledger = &ledgerOfTheTest{consent: fresh()}
	admissions := admissionsOfTheTest(t, ledger)
	ticket, _, _ := admissions.Admit(context.Background(), request("cards", "card", settings))
	ticket.Failed(context.Background(), aisettings.FailureUnreachable, false)
	ticket.Failed(context.Background(), aisettings.FailureRate, false)
	ticket.Failed(context.Background(), aisettings.FailureRefused, true)
	if ledger.consent.State != domain.StateAllowed {
		t.Fatalf("a wait or a connection before the last attempt or a refusal of a chapter paused: %+v", ledger.consent)
	}
	ticket.Failed(context.Background(), aisettings.FailureUnreachable, true)
	if ledger.consent.Pause != domain.PauseConnection {
		t.Fatalf("no connection at the last attempt = %+v", ledger.consent)
	}
	ledger = &ledgerOfTheTest{consent: fresh()}
	ticket, _, _ = admissionsOfTheTest(t, ledger).Admit(context.Background(), request("sample", "card", settings))
	ticket.Failed(context.Background(), aisettings.FailureFunds, true)
	if ledger.consent.State != domain.StateAllowed {
		t.Fatal("a refusal of the sample paused the consent")
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1, NA1.4: a provider that does not answer — a 5xx, a wait it asks for, a
// stream torn mid-answer — pauses the reading at «провайдер не отвечает» only on the last attempt of the client, its funds
// stay «средства» and no connection «связь»; the sample pauses nothing: it says its own words.
func TestAProviderThatDoesNotAnswerPausesTheReading(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	settings := pricedSettings(t)
	for _, failure := range []aisettings.Failure{aisettings.FailureOther, aisettings.FailureRate} {
		ledger := &ledgerOfTheTest{consent: &domain.Consent{
			Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
		}}
		ticket, _, _ := admissionsOfTheTest(t, ledger).Admit(context.Background(), request("cards", "card", settings))
		ticket.Failed(context.Background(), failure, false)
		if ledger.consent.State != domain.StateAllowed {
			t.Fatalf("%s before the last attempt paused: %+v", failure, ledger.consent)
		}
		ticket.Failed(context.Background(), failure, true)
		if ledger.consent.State != domain.StatePaused || ledger.consent.Pause != domain.PauseNoAnswer {
			t.Fatalf("%s on the last attempt = %+v", failure, ledger.consent)
		}
		sample := &ledgerOfTheTest{consent: &domain.Consent{
			Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
		}}
		ticket, _, _ = admissionsOfTheTest(t, sample).Admit(context.Background(), request("sample", "card", settings))
		ticket.Failed(context.Background(), failure, true)
		if sample.consent.State != domain.StateAllowed {
			t.Fatalf("%s of the sample paused the consent", failure)
		}
	}
}

// specs/2026-11-14-desk-journal.md JR1.1, JR1.6: a request whose record of the sending did not lie releases its reserve — no row,
// no count — and pauses nothing; with three such requests in a row, the next request within the sum goes.
func TestARequestThatNeverWentReleasesItsReserve(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	const reserve = 246_000
	stop := int64(reserve + reserve/2)
	ledger := &ledgerOfTheTest{consent: &domain.Consent{
		Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.Unit(aisettings.CurrencyRUB),
		StopAt: &stop, Since: since, ResumedAt: since,
	}}
	admissions := admissionsOfTheTest(t, ledger)
	settings := pricedSettings(t)
	for range 4 {
		ticket, refusal, err := admissions.Admit(context.Background(), request("cards", "card", settings))
		if err != nil || ticket == nil {
			t.Fatalf("a request within the sum = %q, %v", refusal, err)
		}
		ticket.Release(context.Background())
	}
	if admissions.goingOf("saga") != 0 || len(ledger.rows) != 0 || len(ledger.paused) != 0 || ledger.consent.State != domain.StateAllowed || ledger.consent.Spent.Micros != 0 {
		t.Fatalf("going %d, rows %d, paused %v, consent %+v", admissions.goingOf("saga"), len(ledger.rows), ledger.paused, ledger.consent)
	}
}
