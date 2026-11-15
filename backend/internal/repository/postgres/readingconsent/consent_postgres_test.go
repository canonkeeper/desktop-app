// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	service "canonkeeper/backend/internal/services/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	consentAuthor = "94000000-0000-4000-8000-000000000001"
	consentSaga   = "94000000-0000-4000-8000-000000000002"
	strangerSaga  = "94000000-0000-4000-8000-000000000003"
	stranger      = "94000000-0000-4000-8000-000000000004"
)

func seedConsentSagas(t *testing.T, db *storedb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, user := range []string{consentAuthor, stranger} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
			VALUES ($1::uuid, $1::text || '@example.test', 'A', 'hash', now())`, user); err != nil {
			t.Fatal(err)
		}
	}
	for project, owner := range map[string]string{consentSaga: consentAuthor, strangerSaga: stranger} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
			VALUES ($1, $2, 'Северный предел', '', now(), now())`, project, owner); err != nil {
			t.Fatal(err)
		}
	}
}

// specs/2026-11-13-desk-consent.md CN3.1, CN3.2: the consent of a saga is kept whole under its lock with its source, host,
// unit, sum and count; a change that is refused keeps the consent as it was; a pause names its reason only while the consent
// is allowed and its count is the one it began; only the owner owns the saga.
func TestTheConsentOfASagaIsKeptWhole(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_consent_repo")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if consent, err := repository.Consent(ctx, consentSaga); err != nil || consent != nil {
		t.Fatalf("a saga without a consent = %+v, %v", consent, err)
	}
	stop := int64(25_000_000)
	set := func(change domain.Change, host string) (domain.Consent, error) {
		return repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
			return domain.Set(current, change, now, domain.SourceOwn, host, domain.UnitTokens)
		}, false)
	}
	if _, err := set(domain.Change{State: domain.StateAllowed, StopAt: &stop}, "api.deepseek.com"); err != nil {
		t.Fatal(err)
	}
	kept, err := repository.Consent(ctx, consentSaga)
	if err != nil || kept == nil || kept.Source != domain.SourceOwn || kept.Host != "api.deepseek.com" || kept.State != domain.StateAllowed ||
		kept.Unit != domain.UnitTokens || *kept.StopAt != stop || *kept.Step != stop || !kept.Since.Equal(now) || !kept.ResumedAt.Equal(now) {
		t.Fatalf("the consent = %+v, %v", kept, err)
	}
	if _, err := set(domain.Change{State: domain.StateAllowed, StopAt: new(int64)}, "api.deepseek.com"); err == nil {
		t.Fatal("a sum of nothing was kept")
	}
	if again, _ := repository.Consent(ctx, consentSaga); *again.StopAt != stop {
		t.Fatalf("a refused change changed the consent: %+v", again)
	}
	if paused, err := repository.Pause(ctx, consentSaga, now.Add(time.Second), domain.PauseSum, now); err != nil || paused {
		t.Fatalf("a pause of another count = %v, %v", paused, err)
	}
	if paused, err := repository.Pause(ctx, consentSaga, now, domain.PauseFunds, now); err != nil || !paused {
		t.Fatalf("a pause of the provider = %v, %v", paused, err)
	}
	if again, _ := repository.Consent(ctx, consentSaga); again.State != domain.StatePaused || again.Pause != domain.PauseFunds {
		t.Fatalf("the paused consent = %+v", again)
	}
	if paused, _ := repository.Pause(ctx, consentSaga, now, domain.PauseSum, now); paused {
		t.Fatal("a paused consent was paused again with another reason")
	}
	if err := repository.Owned(ctx, consentAuthor, consentSaga); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{consentAuthor, strangerSaga}, {stranger, consentSaga}, {consentAuthor, "not-a-saga"}} {
		if err := repository.Owned(ctx, step[0], step[1]); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("%v owns = %v", step, err)
		}
	}
	if _, err := repository.ChangeConsent(ctx, "94000000-0000-4000-8000-0000000000ff", now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.deepseek.com", domain.UnitTokens)
	}, false); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("the consent of a saga that is not there = %v", err)
	}
}

// CN3.3, CN3.6: an answer of the reading writes its row of the journal — with its cost and the mark of an estimate — and
// its count in one transaction; the cost counts only in the unit of the consent and only to the count that began at its
// start; a row that cannot be written writes no count. The marks «не прочиталась» are kept by the text of a chapter and the
// version of the instructions.
func TestAnAnswerOfTheReadingWritesItsRowAndItsCountAtOnce(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_ledger")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	stop := int64(1_000_000_000)
	if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed, StopAt: &stop}, now, domain.SourceOwn, "api.deepseek.com", domain.Unit("RUB"))
	}, false); err != nil {
		t.Fatal(err)
	}
	entry := modeljournal.Request{
		At: now, Purpose: "card", Profile: "own", ProviderHost: "api.deepseek.com", Model: "reader", RequestBytes: 100,
		Outcome: modeljournal.OutcomeCanceled, Estimated: true, Cost: &modeljournal.Cost{Micros: 700, Currency: "RUB"},
	}
	count := &service.Count{ProjectID: consentSaga, Since: now, Unit: domain.Unit("RUB"), Tokens: aisettings.Tokens{Input: 10, Cached: 2, Output: 5}, Estimated: true,
		Cost: &domain.Cost{Micros: 700, Currency: aisettings.CurrencyRUB}}
	if err := repository.Settle(ctx, entry, count); err != nil {
		t.Fatal(err)
	}
	other := *count
	other.Since, other.Cost = now.Add(-time.Hour), &domain.Cost{Micros: 5, Currency: aisettings.CurrencyRUB}
	if err := repository.Settle(ctx, entry, &other); err != nil {
		t.Fatal(err)
	}
	dollars := *count
	dollars.Cost = &domain.Cost{Micros: 9, Currency: aisettings.CurrencyUSD}
	if err := repository.Settle(ctx, entry, &dollars); err != nil {
		t.Fatal(err)
	}
	broken := entry
	broken.Purpose = ""
	if err := repository.Settle(ctx, broken, count); err == nil {
		t.Fatal("a row that cannot be written was settled")
	}
	consent, _ := repository.Consent(ctx, consentSaga)
	if consent.Spent.Tokens != (aisettings.Tokens{Input: 20, Cached: 4, Output: 10}) || consent.Spent.Micros != 700 || !consent.Spent.Estimated {
		t.Fatalf("the count = %+v", consent.Spent)
	}
	var rows int
	var cost int64
	var currency string
	var estimated bool
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), max(cost_micros), max(currency), bool_and(estimated) FROM model_requests`).Scan(&rows, &cost, &currency, &estimated); err != nil ||
		rows != 3 || cost != 700 || currency != "RUB" || !estimated {
		t.Fatalf("the journal = %d rows, %d %s %v, %v", rows, cost, currency, estimated, err)
	}

	chapter := "94000000-0000-4000-8000-0000000000c1"
	if err := repository.MarkUnread(ctx, consentSaga, chapter, "hash-1", 7, "cut_answer", strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if unread, err := repository.Unread(ctx, consentSaga, 7); err != nil || len(unread) != 1 || unread[0].ID != chapter || unread[0].Hash != "hash-1" || unread[0].Reason != "cut_answer" {
		t.Fatalf("the marks = %+v, %v", unread, err)
	}
	if unread, _ := repository.Unread(ctx, consentSaga, 8); len(unread) != 0 {
		t.Fatalf("a mark of another version of the instructions = %+v", unread)
	}
	if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.deepseek.com", domain.Unit("RUB"))
	}, true); err != nil {
		t.Fatal(err)
	}
	if unread, _ := repository.Unread(ctx, consentSaga, 7); len(unread) != 0 {
		t.Fatalf("the repeat kept the marks: %+v", unread)
	}

	// CN3.1, CN3.4: a consent without a sum follows the unit of the settings of the answer — its count of money starts again
	// in the new currency, the tokens kept; without prices it counts tokens.
	if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed, NoStop: true}, now, domain.SourceOwn, "api.deepseek.com", domain.Unit("RUB"))
	}, false); err != nil {
		t.Fatal(err)
	}
	inDollars := *count
	inDollars.Unit, inDollars.Cost = domain.Unit("USD"), &domain.Cost{Micros: 30, Currency: aisettings.CurrencyUSD}
	if err := repository.Settle(ctx, entry, &inDollars); err != nil {
		t.Fatal(err)
	}
	if consent, _ := repository.Consent(ctx, consentSaga); consent.Unit != domain.Unit("USD") || consent.Spent.Micros != 30 ||
		consent.Spent.Tokens != (aisettings.Tokens{Input: 30, Cached: 6, Output: 15}) {
		t.Fatalf("a consent without a sum after an answer in dollars = %+v", consent)
	}
	withoutPrices := *count
	withoutPrices.Unit, withoutPrices.Cost = domain.UnitTokens, nil
	if err := repository.Settle(ctx, entry, &withoutPrices); err != nil {
		t.Fatal(err)
	}
	if consent, _ := repository.Consent(ctx, consentSaga); consent.Unit != domain.UnitTokens || consent.Count() != 60 {
		t.Fatalf("a consent without a sum after an answer without prices = %+v", consent)
	}
}

// CN3.2, CN3.3: an answer counted while the author changes the consent or the gateway pauses it stays in what was spent —
// a change under the lock of the consent rewrites the count it read, so it reads it as the last word.
func TestAnAnswerCountedDuringAChangeOfTheConsentStaysInItsCount(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_consent_race")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	stop := int64(1_000_000)
	if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed, StopAt: &stop}, now, domain.SourceOwn, "api.example.test", domain.UnitTokens)
	}, false); err != nil {
		t.Fatal(err)
	}
	entry := modeljournal.Request{At: now, Purpose: "card", Profile: "own", ProviderHost: "api.example.test", Model: "reader",
		RequestBytes: 1, Outcome: modeljournal.OutcomeCompleted, Tokens: &modeljournal.Tokens{Input: 1000, Output: 500}}
	count := &service.Count{ProjectID: consentSaga, Since: now, Unit: domain.UnitTokens, Tokens: aisettings.Tokens{Input: 1000, Output: 500}}
	// settleDuring counts an answer while the change holds the lock of the consent, then lets the change write what it read.
	settleDuring := func(t *testing.T) <-chan error {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- repository.Settle(ctx, entry, count) }()
		select {
		case err := <-done:
			t.Errorf("the count of an answer did not wait for the change of the consent: %v", err)
			closed := make(chan error)
			close(closed)
			return closed
		case <-time.After(300 * time.Millisecond):
		}
		return done
	}
	spent := func(t *testing.T, want int64) {
		t.Helper()
		consent, err := repository.Consent(ctx, consentSaga)
		if err != nil || consent.Spent.Tokens.Sum() != want {
			t.Fatalf("spent = %+v, %v; want %d tokens", consent, err, want)
		}
	}

	t.Run("the pause of the author", func(t *testing.T) {
		var done <-chan error
		if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
			done = settleDuring(t)
			return domain.Set(current, domain.Change{State: domain.StatePaused}, now, domain.SourceOwn, "api.example.test", domain.UnitTokens)
		}, false); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		spent(t, 1500)
	})
	t.Run("the pause of the gateway", func(t *testing.T) {
		if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
			return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.example.test", domain.UnitTokens)
		}, false); err != nil {
			t.Fatal(err)
		}
		project, _ := storedb.ParseUUID(consentSaga)
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		current, err := lockConsent(ctx, tx, project)
		if err != nil {
			t.Fatal(err)
		}
		done := settleDuring(t)
		next := *current
		next.State, next.Pause = domain.StatePaused, domain.PauseFunds
		if err := writeConsent(ctx, tx, project, next, now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		spent(t, 3000)
	})
}

// specs/2026-11-13-desk-account-ai.md AI3.4–AI3.6, migration 0151: a consent of the helper of the account is kept with the site
// for its host and its own pauses; the sagas paused with a reason are found by their source and reason.
func TestAConsentOfTheAccountKeepsItsPauses(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_consent_account")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	consent, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceAccount, "site.example.test", domain.UnitTokens)
	}, false)
	if err != nil || consent.Source != domain.SourceAccount {
		t.Fatalf("the consent of the account = %+v %v", consent, err)
	}
	for _, pause := range []domain.Pause{domain.PauseCharacters, domain.PauseReadingFence, domain.PauseUnfit, domain.PauseUnavailable} {
		if paused, err := repository.Pause(ctx, consentSaga, consent.Since, pause, now); err != nil || !paused {
			t.Fatalf("%s: %v %v", pause, paused, err)
		}
		found, err := repository.Paused(ctx, domain.SourceAccount, []domain.Pause{pause, domain.PauseConnection})
		if err != nil || len(found) != 1 || found[0] != consentSaga {
			t.Fatalf("%s found %v %v", pause, found, err)
		}
		if found, _ := repository.Paused(ctx, domain.SourceOwn, []domain.Pause{pause}); len(found) != 0 {
			t.Fatalf("the consent of the account found for «Свой ИИ»: %v", found)
		}
		if found, _ := repository.Paused(ctx, domain.SourceAccount, []domain.Pause{domain.PauseAuthor}); len(found) != 0 {
			t.Fatalf("%s found as the pause of the author", pause)
		}
		if _, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
			return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceAccount, "site.example.test", domain.UnitTokens)
		}, false); err != nil {
			t.Fatal(err)
		}
	}
}

// specs/2026-11-13-desk-consent-web.md RW3.1: the field of getAiSettings — the sagas of the author read under a consent that
// acts with a sum in money — counted by the service over the consents of his live sagas: none without consents, none for a
// consent without a sum or with a sum in tokens, one a saga with a sum in rubles; the pause of the author acts, a consent of
// another host does not; the saga of another author is not his.
func TestTheSagasUnderASumInMoneyAreTheAuthorsActingOnes(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_money_stops")
	seedConsentSagas(t, db)
	ctx := context.Background()
	second, third := "94000000-0000-4000-8000-000000000012", "94000000-0000-4000-8000-000000000013"
	for _, project := range []string{second, third} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
			VALUES ($1, $2, 'Северный предел', '', now(), now())`, project, consentAuthor); err != nil {
			t.Fatal(err)
		}
	}
	repository := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rubles := domain.Unit(aisettings.CurrencyRUB)
	settings := settingsInRubles(t)
	stops := service.MoneyStops{Settings: settings, Consents: repository, Author: consentAuthor}
	count := func() int {
		t.Helper()
		value, err := stops.Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	give := func(project string, change domain.Change, host string, unit domain.Unit) {
		t.Helper()
		if _, err := repository.ChangeConsent(ctx, project, now, func(current *domain.Consent) (domain.Consent, error) {
			return domain.Set(current, change, now, domain.SourceOwn, host, unit)
		}, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(); got != 0 {
		t.Fatalf("no consents: %d", got)
	}
	tokens, money := int64(25_000_000), int64(1_000_000_000)
	give(consentSaga, domain.Change{State: domain.StateAllowed, NoStop: true}, "api.deepseek.com", rubles)
	give(second, domain.Change{State: domain.StateAllowed, StopAt: &tokens}, "api.deepseek.com", domain.UnitTokens)
	if got := count(); got != 0 {
		t.Fatalf("a consent without a sum and one in tokens: %d", got)
	}
	give(third, domain.Change{State: domain.StateAllowed, StopAt: &money}, "api.deepseek.com", rubles)
	give(strangerSaga, domain.Change{State: domain.StateAllowed, StopAt: &money}, "api.deepseek.com", rubles)
	if got := count(); got != 1 {
		t.Fatalf("one saga of the author with a sum in rubles: %d", got)
	}
	give(third, domain.Change{State: domain.StatePaused}, "api.deepseek.com", rubles)
	if got := count(); got != 1 {
		t.Fatalf("the pause of the author acts: %d", got)
	}
	give(second, domain.Change{State: domain.StateAllowed, StopAt: &money}, "api.openai.com", rubles)
	if got := count(); got != 1 {
		t.Fatalf("a consent of another host is counted: %d", got)
	}
}

type settingsOfTheMoney struct{ settings aisettings.Settings }

func (settings settingsOfTheMoney) AI() aisettings.Settings        { return settings.settings }
func (settings settingsOfTheMoney) Calibration() (float64, bool)   { return 0, false }
func (settings settingsOfTheMoney) Connected(context.Context) bool { return true }
func (settingsOfTheMoney) SiteHost() string                        { return "" }
func (settingsOfTheMoney) AccountLimits(context.Context) (service.AccountLimits, bool) {
	return service.AccountLimits{}, false
}

func settingsInRubles(t *testing.T) settingsOfTheMoney {
	t.Helper()
	chat, in, out, currency := "deepseek-chat", "20", "80", "RUB"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &chat, CardsModel: &chat, Advanced: aisettings.AdvancedDraft{PriceIn: &in, PriceOut: &out, Currency: &currency},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settingsOfTheMoney{settings: settings}
}
