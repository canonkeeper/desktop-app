// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/readingconsent"
	sagamemorydomain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/readingconsent"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-15-desk-reading-no-answer.md NA1.2, NA2.1, NA2.2, migration 0154: the check of the base takes the pause
// «провайдер не отвечает» of a consent of «Свой ИИ» — it is kept, read back and goes on only by the author — and a chapter
// marked refused, whose mark acts by the one rule of the marks: the consent, the same helper, the same text; another model
// of the reading lets the chapter read again.
func TestTheBaseKeepsAProviderThatDoesNotAnswerAndAChapterItRefused(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_no_answer")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	consent, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.deepseek.com", domain.UnitTokens)
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if paused, err := repository.Pause(ctx, consentSaga, consent.Since, domain.PauseNoAnswer, now); err != nil || !paused {
		t.Fatalf("the pause «провайдер не отвечает» = %v, %v", paused, err)
	}
	if kept, err := repository.Consent(ctx, consentSaga); err != nil || kept.State != domain.StatePaused || kept.Pause != domain.PauseNoAnswer {
		t.Fatalf("the paused consent = %+v, %v", kept, err)
	}
	if found, err := repository.Paused(ctx, domain.SourceAccount, []domain.Pause{domain.PauseConnection, domain.PauseUnavailable}); err != nil || len(found) != 0 {
		t.Fatalf("the pause of «Свой ИИ» found for the account to go on by itself: %v, %v", found, err)
	}
	// «Повторить» is the author's: the consent goes on with its count.
	resumed, err := repository.ChangeConsent(ctx, consentSaga, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, domain.SourceOwn, "api.deepseek.com", domain.UnitTokens)
	}, false)
	if err != nil || resumed.State != domain.StateAllowed || resumed.Pause != "" || !resumed.Since.Equal(consent.Since) {
		t.Fatalf("«Повторить» = %+v, %v", resumed, err)
	}

	ai := helperOf(t, "deepseek", "deepseek-reasoner", 32_000)
	policy := service.Policy{Settings: heldSettings{ai: &ai}, Consents: repository, Marks: repository}
	chapter := sagamemoryservice.CardKey{ChapterID: "94000000-0000-4000-8000-0000000000c1", ContentHash: "hash-1"}
	version := sagamemorydomain.CurrentCardInstructions.Version
	if err := policy.MarkUnread(ctx, consentSaga, chapter, version, sagamemorydomain.UnreadRefused); err != nil {
		t.Fatalf("a chapter the provider refused was not marked: %v", err)
	}
	marked, err := policy.Marked(ctx, consentSaga, version)
	if err != nil || len(marked) != 1 || marked[0].Reason != sagamemorydomain.UnreadRefused {
		t.Fatalf("the marks = %+v, %v", marked, err)
	}
	if unread, err := policy.Unread(ctx, consentSaga, version); err != nil || !unread[chapter] {
		t.Fatalf("the mark of the same helper does not act: %v, %v", unread, err)
	}
	ai = helperOf(t, "deepseek", "deepseek-chat", 32_000)
	if unread, err := policy.Unread(ctx, consentSaga, version); err != nil || unread[chapter] {
		t.Fatalf("another model of the reading keeps the chapter: %v, %v", unread, err)
	}
}
