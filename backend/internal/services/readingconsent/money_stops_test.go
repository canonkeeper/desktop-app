// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

type ownedOfTheTest struct {
	owner    string
	consents []domain.Consent
}

func (owned *ownedOfTheTest) ConsentsOf(_ context.Context, ownerID string) ([]domain.Consent, error) {
	owned.owner = ownerID
	return owned.consents, nil
}

func pricedIn(t *testing.T, currency string) aisettings.Settings {
	t.Helper()
	chat, reader, in, out := "deepseek-chat", "deepseek-chat", "20", "80"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &chat, CardsModel: &reader, Advanced: aisettings.AdvancedDraft{PriceIn: &in, PriceOut: &out, Currency: &currency},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

// specs/2026-11-13-desk-consent-web.md RW3.1: getAiSettings says how many sagas of the author a change of the currency or the
// removal of the prices would pause — those read under a consent that acts with a sum in money of the currency now; a consent
// paused by the author acts, one of another host does not, a sum in tokens or no sum is no sum in money.
func TestTheSagasUnderASumInMoneyAreCountedByTheRuleOfTheDomain(t *testing.T) {
	rubles := domain.Unit(aisettings.CurrencyRUB)
	stop := int64(1_000_000_000)
	inRubles := domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: rubles, StopAt: &stop}
	paused := inRubles
	paused.State, paused.Pause = domain.StatePaused, domain.PauseAuthor
	elsewhere := inRubles
	elsewhere.Host = "api.openai.com"
	inTokens := domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, StopAt: &stop}
	free := domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: rubles}
	for name, step := range map[string]struct {
		settings aisettings.Settings
		consents []domain.Consent
		want     int
	}{
		"none":                        {pricedIn(t, "RUB"), nil, 0},
		"without a sum and in tokens": {pricedIn(t, "RUB"), []domain.Consent{free, inTokens}, 0},
		"in rubles, paused and going": {pricedIn(t, "RUB"), []domain.Consent{inRubles, paused, free}, 2},
		"another host":                {pricedIn(t, "RUB"), []domain.Consent{elsewhere}, 0},
		"already waiting for a sum":   {pricedIn(t, "USD"), []domain.Consent{inRubles}, 0},
		"without «Свой ИИ» at all":    {aisettings.Settings{Source: aisettings.SourceNone}, []domain.Consent{inRubles}, 0},
	} {
		owned := &ownedOfTheTest{consents: step.consents}
		count, err := MoneyStops{Settings: settingsOfTheTest{settings: step.settings, connected: true}, Consents: owned, Author: "author-1"}.Count(context.Background())
		if err != nil || count != step.want {
			t.Fatalf("%s: %d, %v; want %d", name, count, err, step.want)
		}
		if step.settings.Own != nil && owned.owner != "author-1" {
			t.Fatalf("%s: the consents of %q were read", name, owned.owner)
		}
	}
}
