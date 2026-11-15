// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/canon"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
)

// accountSettings are the settings of the helper of the account; the profile of «Свой ИИ» stays beside it.
func accountSettings(t *testing.T) aisettings.Settings {
	t.Helper()
	settings := deepseekSettings(t)
	settings.Source = aisettings.SourceAccount
	settings.Account = &aisettings.Account{ChatModel: "site-chat", CardsModel: "site-cards", ContextWindow: 400_000, CompactLimit: 300_000}
	return settings
}

// consentStoreOfTheTest changes the consent of one saga as the repository does.
type consentStoreOfTheTest struct{ consent *domain.Consent }

func (store *consentStoreOfTheTest) Consent(context.Context, string) (*domain.Consent, error) {
	return store.consent, nil
}

func (store *consentStoreOfTheTest) ChangeConsent(_ context.Context, _ string, _ time.Time, change func(*domain.Consent) (domain.Consent, error), _ bool) (domain.Consent, error) {
	next, err := change(store.consent)
	if err != nil {
		return domain.Consent{}, err
	}
	store.consent = &next
	return next, nil
}

type workOfTheTest struct{ queued, canceled int }

func (work *workOfTheTest) Queue(context.Context, string) error { work.queued++; return nil }

func (work *workOfTheTest) Cancel(context.Context, string, string) error { work.canceled++; return nil }

// specs/2026-11-13-desk-account-ai.md AI3.4: the consent of the helper of the account names the site and counts in tokens,
// with no stop sum; it does not act for «Свой ИИ», and the consent of «Свой ИИ» does not act for it — the reading waits for a
// new one with «провайдер сменился»; the builders read under it with one chapter at once.
func TestTheConsentOfTheAccountIsItsOwn(t *testing.T) {
	account := settingsOfTheTest{settings: accountSettings(t), connected: true}
	own := settingsOfTheTest{settings: deepseekSettings(t), connected: true}
	store := &consentStoreOfTheTest{}
	work := &workOfTheTest{}
	set := SetConsent{Settings: account, Owners: ownersFake{}, Store: store, Work: work, Now: func() time.Time { return time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC) }}
	sum := int64(1_000)
	if _, err := set.Execute(context.Background(), "author", "saga", domain.Change{State: domain.StateAllowed, StopAt: &sum}); !isField(err, "stopAt") {
		t.Fatalf("a stop sum of the account = %v", err)
	}
	view, err := set.Execute(context.Background(), "author", "saga", domain.Change{State: domain.StateAllowed})
	if err != nil || view.Consent.Source != domain.SourceAccount || view.Consent.Host != "site.example.test" || view.Consent.Unit != domain.UnitTokens || work.queued != 1 {
		t.Fatalf("the consent of the account = %+v %v", view.Consent, err)
	}
	for name, step := range map[string]struct {
		settings settingsOfTheTest
		want     bool
	}{"the account": {account, true}, "«Свой ИИ»": {own, false}} {
		allowed, err := Policy{Settings: step.settings, Consents: store}.ReadingAllowed(context.Background(), "saga", false)
		if err != nil || allowed != step.want {
			t.Errorf("%s reads = %v %v", name, allowed, err)
		}
	}
	if got := (GetConsent{Settings: own, Owners: ownersFake{}, Consents: store}).viewOf(); got.Consent != nil || got.Reason != ReasonProviderChanged {
		t.Fatalf("the consent of the account for «Свой ИИ» = %+v", got)
	}
	store.consent = &domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens}
	if allowed, _ := (Policy{Settings: account, Consents: store}).ReadingAllowed(context.Background(), "saga", false); allowed {
		t.Fatal("the consent of «Свой ИИ» reads for the account")
	}
	if (Policy{Settings: account}).ChaptersAtOnce(context.Background()) != 1 {
		t.Fatal("the account reads more than one chapter at once")
	}
	if _, err := (Samples{Settings: account, Owners: ownersFake{}}).Start(context.Background(), "author", "saga", "key"); !isConflict(err, CodeNoSample) {
		t.Fatalf("a sample of the account = %v", err)
	}
}

func (get GetConsent) viewOf() ConsentView {
	consent, _ := get.Consents.Consent(context.Background(), "saga")
	return viewOf(consent, get.Settings)
}

func isField(err error, field string) bool {
	var invalid *FieldError
	return errors.As(err, &invalid) && invalid.Field == field
}

func isConflict(err error, code string) bool {
	var conflict *Conflict
	return errors.As(err, &conflict) && conflict.Code == code
}

// AI3.4, AI3.5: a request of the reading of the account goes under the consent of the account — without a reserve, money or a
// sample —; a refusal of the site that stops the reading pauses the consent with its reason, and any other stops nothing.
func TestTheCheckLetsTheAccountReadUnderItsConsent(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	ledger := &ledgerOfTheTest{consent: &domain.Consent{
		Source: domain.SourceAccount, Host: "site.example.test", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
	}}
	admissions := admissionsOfTheTest(t, ledger)
	settings := accountSettings(t)
	asked := AdmitRequest{JobID: "cards", Purpose: "card", Host: "site.example.test", Model: "site-cards", RequestBytes: 15_000, Settings: settings}
	ticket, refusal, err := admissions.Admit(context.Background(), asked)
	if err != nil || ticket == nil || ticket.reserve != 0 {
		t.Fatalf("a request of the account = %q %v", refusal, err)
	}
	ticket.Stop(context.Background(), "words")
	ticket.Stop(context.Background(), "characters")
	if len(ledger.paused) != 1 || ledger.paused[0] != domain.PauseCharacters {
		t.Fatalf("the pauses = %v", ledger.paused)
	}
	asked.JobID = "sample"
	if ticket, _, _ := admissions.Admit(context.Background(), asked); ticket != nil {
		t.Fatal("a sample reads for the account")
	}
	ledger.consent.Source, ledger.consent.Host, ledger.consent.State, ledger.consent.Pause = domain.SourceOwn, "api.deepseek.com", domain.StateAllowed, ""
	asked.JobID = "cards"
	if ticket, refusal, _ := admissions.Admit(context.Background(), asked); ticket != nil || refusal != refusedConsent {
		t.Fatalf("the consent of «Свой ИИ» let the account read: %q", refusal)
	}
}

// AI3.4, AI4.2 (the money fields are «Свой ИИ»'s alone): a step of the account settled without a usage — a Job canceled, the
// author's pause — is estimated in tokens; neither its row of the journal nor the count of the consent of the account carries
// the prices or the currency of the profile of «Свой ИИ» that stays in the settings.
func TestASettledStepOfTheAccountCarriesNoMoneyOfTheOwnProfile(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	ledger := &ledgerOfTheTest{consent: &domain.Consent{
		Source: domain.SourceAccount, Host: "site.example.test", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
	}}
	admissions := admissionsOfTheTest(t, ledger)
	settings := pricedSettings(t)
	settings.Source = aisettings.SourceAccount
	settings.Account = &aisettings.Account{ChatModel: "site-chat", CardsModel: "site-cards", ContextWindow: 400_000, CompactLimit: 300_000}
	ticket, refusal, err := admissions.Admit(context.Background(), AdmitRequest{
		JobID: "cards", Purpose: "card", Host: "site.example.test", Model: "site-cards", RequestBytes: 15_000, Settings: settings,
	})
	if err != nil || ticket == nil {
		t.Fatalf("a request of the account = %q %v", refusal, err)
	}
	ticket.Settle(context.Background(), modeljournal.Request{
		Purpose: "card", JobID: "cards", Profile: "account", Outcome: modeljournal.OutcomeCanceled, RequestBytes: 15_000, OutputCharacters: 200,
	}, true)
	if len(ledger.rows) != 1 || ledger.rows[0].Cost != nil || !ledger.rows[0].Estimated {
		t.Fatalf("the row of the account = %+v", ledger.rows)
	}
	if count := ledger.counts[0]; count == nil || count.Unit != domain.UnitTokens || count.Cost != nil || count.Tokens.Input == 0 {
		t.Fatalf("the count of the consent of the account = %+v", count)
	}
}

// AI3.7: the state of the reading of the account names the two limits of its tier as the site told them; the reading of «Свой
// ИИ» says nothing of them.
func TestTheStateOfTheReadingOfTheAccountNamesItsLimits(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	limits := AccountLimits{Characters: domain.Limit{Used: 900_000, Limit: 1_000_000}, Work: domain.Limit{Used: 100, Limit: 8_000_000}}
	settings := settingsOfTheTest{settings: accountSettings(t), connected: true, limits: &limits}
	consent := &domain.Consent{
		Source: domain.SourceAccount, Host: "site.example.test", State: domain.StatePaused, Pause: domain.PauseCharacters, Unit: domain.UnitTokens,
		Since: since, ResumedAt: since,
	}
	status := Status{Settings: settings, Consents: consentsOfTheTest{consent: consent}, Saga: sagaFake{toRead: ToRead{Unread: chaptersOf(3)}},
		Pace: paceFake(0), Now: func() time.Time { return since.Add(time.Hour) }}
	waiting, err := status.Waiting(context.Background(), "saga")
	if err != nil || waiting != canon.PreparationWaitingPaused {
		t.Fatalf("waiting = %q %v", waiting, err)
	}
	reading, err := status.State(context.Background(), "saga", canon.IndexingPreparation{ChaptersRead: 3, ChaptersTotal: 6})
	if err != nil || reading.State != canon.ReadingPaused || *reading.Pause != "characters" || reading.Limits == nil ||
		reading.Limits.Characters.Used != 900_000 || reading.Limits.Work.Limit != 8_000_000 || reading.Unit != "tokens" || reading.StopAt != nil {
		t.Fatalf("the state = %+v %v", reading, err)
	}
	own := settingsOfTheTest{settings: deepseekSettings(t), connected: true, limits: &limits}
	consent.Source, consent.Host = domain.SourceOwn, "api.deepseek.com"
	status.Settings = own
	if reading, _ := status.State(context.Background(), "saga", canon.IndexingPreparation{}); reading == nil || reading.Limits != nil {
		t.Fatalf("«Свой ИИ» names the limits of the account: %+v", reading)
	}
}

// AI3.4: the estimate of the account is in characters and the limits of the tier — no money, sample or stop sum: the
// characters are those of the inputs of the card steps, and the limit that lets less through ends the reading first.
func TestTheEstimateOfTheAccountIsInCharactersAndLimits(t *testing.T) {
	limits := AccountLimits{Characters: domain.Limit{Used: 990_000, Limit: 1_000_000}, Work: domain.Limit{Limit: 80_000_000}}
	settings := settingsOfTheTest{settings: accountSettings(t), connected: true, limits: &limits}
	get := GetEstimate{Settings: settings, Owners: ownersFake{}, Saga: sagaFake{toRead: ToRead{Unread: chaptersOf(6), Books: 1}, input: 61_234}}
	estimate, err := get.Execute(context.Background(), "author", "saga")
	if err != nil || estimate.Account == nil || estimate.Account.Characters != 61_234 || estimate.Account.EndsFirst != domain.EndsCharacters ||
		estimate.Amount != nil || estimate.Unit != domain.UnitTokens || estimate.SampleChapters != 0 || estimate.SuggestedStop != 0 ||
		estimate.Host != "site.example.test" || estimate.Chapters != 6 {
		t.Fatalf("the estimate = %+v %+v %v", estimate.Estimate, estimate.Account, err)
	}
}

// AI3.4: an estimate whose limits the site has not told yet — after a restart without a connection — says it does not know
// them, not that the tier lets nothing through.
func TestAnEstimateWithoutTheLimitsOfTheSiteDoesNotKnowThem(t *testing.T) {
	settings := settingsOfTheTest{settings: accountSettings(t), connected: true}
	get := GetEstimate{Settings: settings, Owners: ownersFake{}, Saga: sagaFake{toRead: ToRead{Unread: chaptersOf(6), Books: 1}, input: 61_234}}
	estimate, err := get.Execute(context.Background(), "author", "saga")
	if err != nil || estimate.Account == nil || estimate.Account.Characters != 61_234 || estimate.Account.LimitsTold || estimate.Account.EndsFirst != "" {
		t.Fatalf("the estimate without the limits = %+v %v", estimate.Account, err)
	}
}

// AI3.7, the table «Страницам B2»: the state of the reading and the consent name who reads the books — the source of the
// consent that acts, or of the settings while none does — so the page never mixes the reading of the account with «Свой ИИ».
func TestTheReadingNamesItsSource(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	consent := &domain.Consent{Source: domain.SourceAccount, Host: "site.example.test", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since, ResumedAt: since}
	for name, step := range map[string]struct {
		settings aisettings.Settings
		consent  *domain.Consent
		want     string
	}{
		"the account reads":        {accountSettings(t), consent, "account"},
		"the account waits":        {accountSettings(t), nil, "account"},
		"«Свой ИИ» waits":          {deepseekSettings(t), nil, "own"},
		"«Свой ИИ» after the site": {deepseekSettings(t), consent, "own"},
	} {
		settings := settingsOfTheTest{settings: step.settings, connected: true}
		status := Status{Settings: settings, Consents: consentsOfTheTest{consent: step.consent}, Saga: sagaFake{toRead: ToRead{Unread: chaptersOf(3)}},
			Pace: paceFake(0), Now: func() time.Time { return since.Add(time.Hour) }}
		reading, err := status.State(context.Background(), "saga", canon.IndexingPreparation{ChaptersRead: 3, ChaptersTotal: 6})
		if err != nil || reading == nil || reading.Source == nil || *reading.Source != step.want {
			t.Errorf("%s: the state = %+v %v", name, reading, err)
		}
		view, err := (GetConsent{Settings: settings, Owners: ownersFake{}, Consents: consentsOfTheTest{consent: step.consent}}).Execute(context.Background(), "author", "saga")
		if err != nil || string(view.Source) != step.want {
			t.Errorf("%s: the consent names %q %v", name, view.Source, err)
		}
	}
}

// marksOfTheTest keep the marks «не прочиталась».
type marksOfTheTest struct{ marked int }

func (marks *marksOfTheTest) Unread(context.Context, string, int) ([]Unread, error) { return nil, nil }

func (marks *marksOfTheTest) MarkUnread(context.Context, string, string, string, int, string, string, time.Time) error {
	marks.marked++
	return nil
}

func (marks *marksOfTheTest) RestMarks(context.Context, string) ([]RestMark, error) { return nil, nil }

func (marks *marksOfTheTest) MarkRest(context.Context, string, RestMark, time.Time) error { return nil }

// AI3.5: the helper of the account marks no chapter «не прочиталась»; «Свой ИИ» does.
func TestTheAccountMarksNoChapterUnread(t *testing.T) {
	marks := &marksOfTheTest{}
	key := sagamemoryservice.CardKey{ChapterID: "chapter", ContentHash: "hash"}
	_ = Policy{Settings: settingsOfTheTest{settings: accountSettings(t), connected: true}, Marks: marks}.MarkUnread(context.Background(), "saga", key, 1, "unreadable")
	_ = Policy{Settings: settingsOfTheTest{settings: deepseekSettings(t), connected: true}, Marks: marks}.MarkUnread(context.Background(), "saga", key, 1, "unreadable")
	if marks.marked != 1 {
		t.Fatalf("marked %d", marks.marked)
	}
	// The policy says so to the writer of the cards: a chapter the provider refused ends at once only where it is marked
	// (specs/2026-11-15-desk-reading-no-answer.md NA2.1).
	account := Policy{Settings: settingsOfTheTest{settings: accountSettings(t), connected: true}, Marks: marks}
	own := Policy{Settings: settingsOfTheTest{settings: deepseekSettings(t), connected: true}, Marks: marks}
	if account.MarksChapters(context.Background()) || !own.MarksChapters(context.Background()) {
		t.Fatalf("the account marks chapters: %v, «Свой ИИ»: %v", account.MarksChapters(context.Background()), own.MarksChapters(context.Background()))
	}
}
