// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

type settingsOfTheTest struct {
	settings  aisettings.Settings
	connected bool
	limits    *AccountLimits
}

func (settings settingsOfTheTest) AI() aisettings.Settings        { return settings.settings }
func (settings settingsOfTheTest) Calibration() (float64, bool)   { return 0, false }
func (settings settingsOfTheTest) Connected(context.Context) bool { return settings.connected }
func (settingsOfTheTest) SiteHost() string                        { return "site.example.test" }
func (settings settingsOfTheTest) AccountLimits(context.Context) (AccountLimits, bool) {
	if settings.limits == nil {
		return AccountLimits{}, false
	}
	return *settings.limits, true
}

type consentsOfTheTest struct{ consent *domain.Consent }

func (consents consentsOfTheTest) Consent(context.Context, string) (*domain.Consent, error) {
	return consents.consent, nil
}

func deepseekSettings(t *testing.T) aisettings.Settings {
	t.Helper()
	chat, reader := "deepseek-chat", "deepseek-reasoner"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{Preset: "deepseek", ChatModel: &chat, CardsModel: &reader}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

// specs/2026-11-13-desk-consent.md CN3.1, CN3.7: the model reads the books of a saga only under a consent that acts — given for
// «Свой ИИ» at the host of the settings now — and is allowed; a consent of another host, one written before the consents
// had a source, a paused one or none wait; without the helper nothing reads. The sample reads by its Job alone.
func TestTheModelReadsOnlyUnderAConsentThatActs(t *testing.T) {
	settings := settingsOfTheTest{settings: deepseekSettings(t), connected: true}
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	acting := &domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StateAllowed, Unit: domain.UnitTokens, Since: since}
	for name, step := range map[string]struct {
		consent *domain.Consent
		sample  bool
		want    bool
	}{
		"allowed":            {acting, false, true},
		"none":               {nil, false, false},
		"another host":       {&domain.Consent{Source: domain.SourceOwn, Host: "api.openai.com", State: domain.StateAllowed, Since: since}, false, false},
		"before the sources": {&domain.Consent{State: domain.StateAllowed, Since: since}, false, false},
		"paused":             {&domain.Consent{Source: domain.SourceOwn, Host: "api.deepseek.com", State: domain.StatePaused, Pause: domain.PauseAuthor, Since: since}, false, false},
		"a sample":           {nil, true, true},
	} {
		policy := Policy{Settings: settings, Consents: consentsOfTheTest{consent: step.consent}}
		allowed, err := policy.ReadingAllowed(context.Background(), "saga", step.sample)
		if err != nil || allowed != step.want {
			t.Errorf("%s = %v, %v; want %v", name, allowed, err, step.want)
		}
	}
	settings.connected = false
	if allowed, _ := (Policy{Settings: settings, Consents: consentsOfTheTest{consent: acting}}).ReadingAllowed(context.Background(), "saga", true); allowed {
		t.Fatal("the model reads without the helper")
	}
}

// CN3.5: the reading takes as many chapters at once as «Запросов одновременно» of the author; without «Своего ИИ» the builder
// keeps its own number.
func TestTheReadingTakesTheRequestsAtOnceOfTheAuthor(t *testing.T) {
	settings := deepseekSettings(t)
	if atOnce := (Policy{Settings: settingsOfTheTest{settings: settings}}).ChaptersAtOnce(context.Background()); atOnce != settings.Own.Advanced.Parallel || atOnce < 1 {
		t.Fatalf("chapters at once = %d; want %d", atOnce, settings.Own.Advanced.Parallel)
	}
	if atOnce := (Policy{Settings: settingsOfTheTest{}}).ChaptersAtOnce(context.Background()); atOnce != 0 {
		t.Fatalf("chapters at once without the helper = %d", atOnce)
	}
}
