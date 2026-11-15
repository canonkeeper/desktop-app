// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/readingconsent"
)

func (store *consentStoreOfTheTest) Paused(_ context.Context, source domain.Source, pauses []domain.Pause) ([]string, error) {
	if store.consent == nil || store.consent.Source != source || store.consent.State != domain.StatePaused {
		return nil, nil
	}
	for _, pause := range pauses {
		if store.consent.Pause == pause {
			return []string{"saga"}, nil
		}
	}
	return nil, nil
}

// specs/2026-11-13-desk-account-ai.md AI3.6: the reading of the account goes on by itself from the pauses that are due — the
// same consent and count, the Jobs queued —; the pause of the author, «Свой ИИ» and a helper not connected never do.
func TestTheReadingOfTheAccountGoesOnByItself(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	paused := func(pause domain.Pause, source domain.Source) *consentStoreOfTheTest {
		return &consentStoreOfTheTest{consent: &domain.Consent{
			Source: source, Host: "site.example.test", State: domain.StatePaused, Pause: pause, Unit: domain.UnitTokens, Since: since, ResumedAt: since,
			Spent: domain.Spent{Estimated: true},
		}}
	}
	account := settingsOfTheTest{settings: accountSettings(t), connected: true}
	now := func() time.Time { return since.Add(time.Hour) }
	store, work := paused(domain.PauseConnection, domain.SourceAccount), &workOfTheTest{}
	resumed, err := Resume{Settings: account, Store: store, Work: work, Now: now}.Execute(context.Background(), domain.PauseConnection, domain.PauseUnavailable)
	if err != nil || resumed != 1 || store.consent.State != domain.StateAllowed || !store.consent.Since.Equal(since) || !store.consent.Spent.Estimated || work.queued != 1 {
		t.Fatalf("resumed %d %v: %+v, queued %d", resumed, err, store.consent, work.queued)
	}
	for name, step := range map[string]struct {
		store    *consentStoreOfTheTest
		settings settingsOfTheTest
		pauses   []domain.Pause
	}{
		"the author":           {paused(domain.PauseAuthor, domain.SourceAccount), account, []domain.Pause{domain.PauseConnection}},
		"not due":              {paused(domain.PauseUnfit, domain.SourceAccount), account, []domain.Pause{domain.PauseConnection}},
		"«Свой ИИ»":            {paused(domain.PauseConnection, domain.SourceOwn), settingsOfTheTest{settings: deepseekSettings(t), connected: true}, []domain.Pause{domain.PauseConnection}},
		"the helper is absent": {paused(domain.PauseConnection, domain.SourceAccount), settingsOfTheTest{settings: accountSettings(t)}, []domain.Pause{domain.PauseConnection}},
	} {
		work := &workOfTheTest{}
		resumed, err := Resume{Settings: step.settings, Store: step.store, Work: work, Now: now}.Execute(context.Background(), step.pauses...)
		if err != nil || resumed != 0 || step.store.consent.State != domain.StatePaused || work.queued != 0 {
			t.Errorf("%s went on: %d %v", name, resumed, err)
		}
	}
}
