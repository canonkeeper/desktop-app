// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	journalrepo "canonkeeper/backend/internal/repository/postgres/modeljournal"
	service "canonkeeper/backend/internal/services/readingconsent"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-14-desk-journal.md JR2.6: a request of the sample writes its row of the journal and its request of the sample in
// one transaction, and what the sample spent and measured is read from its own table — the clearing of the journal leaves it.
func TestTheRequestsOfTheSampleOutliveTheClearingOfTheJournal(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_sample_requests")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	const sampleJob = "94000000-0000-4000-8000-0000000000a1"
	at := time.Now().UTC().Truncate(time.Millisecond)
	entry := modeljournal.Request{
		ID: modeljournal.NewID(), At: at, Purpose: "card", JobID: sampleJob, Profile: "own", ProviderHost: "api.deepseek.com", Model: "reader",
		Tokens: &modeljournal.Tokens{Input: 1000, CachedInput: 100, Output: 200}, Outcome: modeljournal.OutcomeCompleted,
		Duration: 3 * time.Second, Cost: &modeljournal.Cost{Micros: 1500, Currency: "RUB"},
	}
	if err := repository.SettleSample(ctx, consentSaga, entry); err != nil {
		t.Fatal(err)
	}
	read := func() []string {
		requests, err := repository.SampleRequests(ctx, sampleJob)
		if err != nil {
			t.Fatal(err)
		}
		var described []string
		for _, request := range requests {
			described = append(described, request.Host+" "+request.Model+" "+request.Duration.String()+" "+request.Cost.Currency)
			if !request.TokensKnown || request.Tokens.Input != 1000 || request.Tokens.Output != 200 || request.Cost.Micros != 1500 {
				t.Fatalf("the request of the sample = %+v", request)
			}
		}
		return described
	}
	before := read()
	if len(before) != 1 || before[0] != "api.deepseek.com reader 3s RUB" {
		t.Fatalf("the requests of the sample = %v", before)
	}
	if err := journalrepo.New(db).Clear(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_requests`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows after the clearing %d, %v", rows, err)
	}
	if after := read(); len(after) != 1 || after[0] != before[0] {
		t.Fatalf("the clearing took the sample: %v", after)
	}
}

// JR1.1 (the second check, finding 7): the journal cleared between the record of a sending and the answer — the row of the
// request carries the number of a record that is no more, no key ties them: the settling goes, and the count of the consent
// grows by the answer.
func TestAClearingBetweenTheRecordAndTheAnswerKeepsTheCount(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_clear_midway")
	seedConsentSagas(t, db)
	repository := New(db)
	journal := journalrepo.New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	stop := int64(1_000_000_000)
	if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed, StopAt: &stop}, now, domain.SourceOwn, "api.deepseek.com", domain.Unit("RUB"))
	}, false); err != nil {
		t.Fatal(err)
	}
	id := modeljournal.NewID()
	if err := journal.Send(ctx, modeljournal.Sending{
		ID: id, At: now, Purpose: "card", Profile: "own", Host: "api.deepseek.com", Bytes: 100,
		Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: "94000000-0000-4000-8000-0000000000c1", Precision: disclosure.Whole}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	entry := modeljournal.Request{
		ID: id, At: now, Purpose: "card", Profile: "own", ProviderHost: "api.deepseek.com", Model: "reader", RequestBytes: 100,
		Tokens: &modeljournal.Tokens{Input: 10, CachedInput: 2, Output: 5}, Outcome: modeljournal.OutcomeCompleted,
		Cost: &modeljournal.Cost{Micros: 700, Currency: "RUB"},
	}
	count := &service.Count{ProjectID: consentSaga, Since: now, Unit: domain.Unit("RUB"), Tokens: aisettings.Tokens{Input: 10, Cached: 2, Output: 5},
		Cost: &domain.Cost{Micros: 700, Currency: aisettings.CurrencyRUB}}
	if err := repository.Settle(ctx, entry, count); err != nil {
		t.Fatalf("the settling after the clearing: %v", err)
	}
	consent, err := repository.Consent(ctx, consentSaga)
	if err != nil || consent.Spent.Tokens != (aisettings.Tokens{Input: 10, Cached: 2, Output: 5}) || consent.Spent.Micros != 700 {
		t.Fatalf("the count = %+v, %v", consent.Spent, err)
	}
	var rows, records int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_requests WHERE id = $1), (SELECT count(*) FROM model_disclosures)`, id).Scan(&rows, &records); err != nil || rows != 1 || records != 0 {
		t.Fatalf("rows %d, records %d, %v", rows, records, err)
	}
}
