// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

func amount(value int64) *int64 { return &value }

// specs/2026-11-13-desk-consent.md CN3.1, CN3.2: a new count begins only when no consent acts; a pause, a continuation, a
// new sum and «ещё на X» keep it; «ещё на X» is what was spent and X, and the step is the sum the author named last; a sum
// not above what was spent is refused by its field; a pause without a consent is nothing to pause.
func TestTheCountBeginsOnlyWithANewConsent(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	rubles := Unit(aisettings.CurrencyRUB)
	if _, err := Set(nil, Change{State: StatePaused}, start, SourceOwn, "api.deepseek.com", rubles); !errors.Is(err, ErrNothingToPause) {
		t.Fatalf("a pause without a consent = %v", err)
	}
	consent, err := Set(nil, Change{State: StateAllowed, StopAt: amount(1_000_000_000)}, start, SourceOwn, "api.deepseek.com", rubles)
	if err != nil || consent.State != StateAllowed || !consent.Since.Equal(start) || consent.Unit != rubles || *consent.StopAt != 1_000_000_000 ||
		*consent.Step != 1_000_000_000 || !consent.Acts(SourceOwn, "api.deepseek.com") {
		t.Fatalf("the consent = %+v, %v", consent, err)
	}
	consent.Spent = Spent{Tokens: aisettings.Tokens{Input: 900, Output: 100}, Micros: 1_000_000_000}
	consent.State, consent.Pause = StatePaused, PauseSum

	later := start.Add(time.Hour)
	more, err := Set(&consent, Change{State: StateAllowed, ContinueBy: amount(500_000_000)}, later, SourceOwn, "api.deepseek.com", rubles)
	if err != nil || !more.Since.Equal(start) || more.Spent.Micros != 1_000_000_000 || *more.StopAt != 1_500_000_000 || *more.Step != 500_000_000 ||
		more.State != StateAllowed || more.Pause != "" || !more.ResumedAt.Equal(later) {
		t.Fatalf("«ещё на X» = %+v, %v", more, err)
	}
	paused, err := Set(&more, Change{State: StatePaused}, later, SourceOwn, "api.deepseek.com", rubles)
	if err != nil || paused.Pause != PauseAuthor || !paused.Since.Equal(start) || *paused.StopAt != 1_500_000_000 {
		t.Fatalf("the pause of the author = %+v, %v", paused, err)
	}
	if _, err := Set(&paused, Change{State: StateAllowed, StopAt: amount(1_000_000_000)}, later, SourceOwn, "api.deepseek.com", rubles); !fieldIs(err, "stopAt") {
		t.Fatalf("a sum not above what was spent = %v", err)
	}
	if _, err := Set(&paused, Change{State: StateAllowed, StopAt: amount(5), NoStop: true}, later, SourceOwn, "api.deepseek.com", rubles); !errors.Is(err, ErrInvalidChange) {
		t.Fatalf("two sums at once = %v", err)
	}
	free, err := Set(&paused, Change{State: StateAllowed, NoStop: true}, later, SourceOwn, "api.deepseek.com", rubles)
	if err != nil || free.StopAt != nil || free.Step != nil || !free.Since.Equal(start) {
		t.Fatalf("no sum = %+v, %v", free, err)
	}

	// Another host: the consent does not act; the reading waits for a new one, whose count begins again.
	if paused.Acts(SourceOwn, "api.openai.com") {
		t.Fatal("a consent acts for another host")
	}
	other, err := Set(&paused, Change{State: StateAllowed}, later, SourceOwn, "api.openai.com", rubles)
	if err != nil || !other.Since.Equal(later) || other.Spent != (Spent{}) || other.Host != "api.openai.com" {
		t.Fatalf("a consent for a new host = %+v, %v", other, err)
	}
	// A consent of before the sources acts for none.
	if (&Consent{State: StateAllowed}).Acts(SourceOwn, "") {
		t.Fatal("a consent without a source acts")
	}
}

// CN3.4: a sum in money whose currency changed or whose prices went goes on only with a sum named again.
func TestASumInMoneyIsNamedAgainAfterAChangeOfThePrices(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	consent := Consent{
		Source: SourceOwn, Host: "api.deepseek.com", State: StatePaused, Pause: PausePrices, Unit: Unit(aisettings.CurrencyRUB),
		StopAt: amount(1_000_000_000), Since: start, Spent: Spent{Tokens: aisettings.Tokens{Input: 4_000_000, Output: 1_000_000}, Micros: 200_000_000},
	}
	if _, err := Set(&consent, Change{State: StateAllowed}, start, SourceOwn, "api.deepseek.com", UnitTokens); !fieldIs(err, "stopAt") {
		t.Fatalf("«Продолжить» without a sum = %v", err)
	}
	tokens, err := Set(&consent, Change{State: StateAllowed, StopAt: amount(25_000_000)}, start, SourceOwn, "api.deepseek.com", UnitTokens)
	if err != nil || tokens.Unit != UnitTokens || tokens.Count() != 5_000_000 || !tokens.Since.Equal(start) {
		t.Fatalf("a sum in tokens = %+v, %v", tokens, err)
	}
	// «Ещё на X» names X in the unit of the consent: after a change of the currency it would read rubles as dollars.
	if _, err := Set(&consent, Change{State: StateAllowed, ContinueBy: amount(10_000_000)}, start, SourceOwn, "api.deepseek.com", Unit(aisettings.CurrencyUSD)); !fieldIs(err, "continueBy") {
		t.Fatalf("«ещё на X» in another currency = %v", err)
	}
	dollars, err := Set(&consent, Change{State: StateAllowed, StopAt: amount(10_000_000)}, start, SourceOwn, "api.deepseek.com", Unit(aisettings.CurrencyUSD))
	if err != nil || dollars.Unit != Unit(aisettings.CurrencyUSD) || dollars.Count() != 0 || *dollars.StopAt != 10_000_000 {
		t.Fatalf("a sum in another currency = %+v, %v", dollars, err)
	}
	if !consent.PricesChanged(Unit(aisettings.CurrencyUSD)) || !consent.PricesChanged(UnitTokens) || consent.PricesChanged(Unit(aisettings.CurrencyRUB)) {
		t.Fatal("a sum in rubles does not see the change of its prices")
	}
}

// CN3.1, CN3.4: a sum in tokens holds whatever the prices — the author who names prices later reads on; a consent without a
// sum follows the unit of the settings, its count of money starting again in a new currency, the tokens kept.
func TestTheUnitOfAConsentFollowsThePricesOnlyWithoutASumInMoney(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	rubles, dollars := Unit(aisettings.CurrencyRUB), Unit(aisettings.CurrencyUSD)
	inTokens := Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: UnitTokens, StopAt: amount(25_000_000),
		Step: amount(25_000_000), Since: start, Spent: Spent{Tokens: aisettings.Tokens{Input: 3_000_000, Output: 1_000_000}}}
	if inTokens.PricesChanged(rubles) {
		t.Fatal("a sum in tokens waits for a sum named again when prices came")
	}
	if decision, pause := Admit(&inTokens, SourceOwn, "api.deepseek.com", rubles, 0, 1_000); decision != Go || pause != "" {
		t.Fatalf("a sum in tokens with prices = %v %s", decision, pause)
	}
	paused := inTokens
	paused.State, paused.Pause = StatePaused, PauseSum
	more, err := Set(&paused, Change{State: StateAllowed, ContinueBy: amount(5_000_000)}, start, SourceOwn, "api.deepseek.com", rubles)
	if err != nil || more.Unit != UnitTokens || *more.StopAt != 9_000_000 {
		t.Fatalf("«ещё на X» in tokens with prices = %+v, %v", more, err)
	}
	if again, err := Set(&paused, Change{State: StateAllowed}, start, SourceOwn, "api.deepseek.com", rubles); err != nil || again.Unit != UnitTokens {
		t.Fatalf("«Продолжить» in tokens with prices = %+v, %v", again, err)
	}
	if in := inTokens.In(rubles); in.Unit != UnitTokens || in.Count() != 4_000_000 {
		t.Fatalf("a sum in tokens in rubles = %+v", in)
	}

	free := Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: rubles, Since: start,
		Spent: Spent{Tokens: aisettings.Tokens{Input: 3_000_000, Output: 1_000_000}, Micros: 700_000_000}}
	if in := free.In(dollars); in.Unit != dollars || in.Spent.Micros != 0 || in.Spent.Tokens != free.Spent.Tokens {
		t.Fatalf("a consent without a sum in dollars = %+v", in)
	}
	if in := free.In(UnitTokens); in.Unit != UnitTokens || in.Count() != 4_000_000 {
		t.Fatalf("a consent without a sum without prices = %+v", in)
	}
	if in := free.In(rubles); in != free {
		t.Fatalf("a consent in its own unit = %+v", in)
	}
	if free.PricesChanged(dollars) {
		t.Fatal("a consent without a sum waits for a sum")
	}
}

// CN3.3: a request goes only while the consent acts and is allowed and what was spent with the reserves of the requests
// that go and of this one is within the sum; over the sum the consent stops at «сумма», a sum in money without its prices at
// «цены изменились»; without a sum only the consent counts.
func TestARequestGoesOnlyWithinTheSum(t *testing.T) {
	rubles := Unit(aisettings.CurrencyRUB)
	consent := &Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: rubles, StopAt: amount(100), Spent: Spent{Micros: 40}}
	for _, step := range []struct {
		going, reserve int64
		unit           Unit
		host           string
		decision       Decision
		pause          Pause
	}{
		{0, 60, rubles, "api.deepseek.com", Go, ""},
		{30, 30, rubles, "api.deepseek.com", Go, ""},
		{30, 31, rubles, "api.deepseek.com", Halt, PauseSum},
		{0, 1, UnitTokens, "api.deepseek.com", Halt, PausePrices},
		{0, 1, rubles, "api.openai.com", Refuse, ""},
	} {
		if decision, pause := Admit(consent, SourceOwn, step.host, step.unit, step.going, step.reserve); decision != step.decision || pause != step.pause {
			t.Errorf("%+v = %v %s", step, decision, pause)
		}
	}
	consent.State = StatePaused
	if decision, _ := Admit(consent, SourceOwn, "api.deepseek.com", rubles, 0, 1); decision != Refuse {
		t.Fatal("a paused consent lets a request go")
	}
	if decision, _ := Admit(nil, SourceOwn, "api.deepseek.com", rubles, 0, 1); decision != Refuse {
		t.Fatal("no consent lets a request go")
	}
	consent.State, consent.StopAt = StateAllowed, nil
	if decision, _ := Admit(consent, SourceOwn, "api.deepseek.com", UnitTokens, 1_000_000, 1_000_000); decision != Go {
		t.Fatal("a consent without a sum stops a request")
	}
	// The reserve of a request: its input by its size and the calibration, its output the limit of an answer.
	prices := &aisettings.Prices{Currency: aisettings.CurrencyRUB, In: 30 * 10_000, Out: 120 * 10_000}
	if reserve := Reserve(15_000, 16_000, 400, nil, UnitTokens); reserve != 4_000+16_000 {
		t.Fatalf("a reserve in tokens = %d", reserve)
	}
	if reserve := Reserve(15_000, 16_000, 400, prices, rubles); reserve != prices.Cost(aisettings.Tokens{Input: 4_000, Output: 16_000}) {
		t.Fatalf("a reserve in rubles = %d", reserve)
	}
}

func fieldIs(err error, field string) bool {
	var invalid *FieldError
	return errors.As(err, &invalid) && invalid.Field == field
}

// specs/2026-11-13-desk-consent-web.md RW3.1: a sum in money of the currency the settings price in now is what a change of the
// currency or the removal of the prices pauses; a sum in tokens, no sum, or a sum already waiting for a new one are not.
func TestASumInMoneyIsWhatAChangeOfThePricesPauses(t *testing.T) {
	rubles, dollars := Unit(aisettings.CurrencyRUB), Unit(aisettings.CurrencyUSD)
	inRubles := Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: rubles, StopAt: amount(1_000_000_000)}
	if !inRubles.StopsInMoney(rubles) {
		t.Fatal("a sum in rubles priced in rubles is not a sum in money")
	}
	paused := inRubles
	paused.State, paused.Pause = StatePaused, PauseAuthor
	if !paused.StopsInMoney(rubles) {
		t.Fatal("a sum in rubles paused by the author is not a sum in money")
	}
	if inRubles.StopsInMoney(dollars) || inRubles.StopsInMoney(UnitTokens) {
		t.Fatal("a sum in rubles that already waits for a new sum is counted again")
	}
	inTokens := Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: UnitTokens, StopAt: amount(25_000_000)}
	free := Consent{Source: SourceOwn, Host: "api.deepseek.com", State: StateAllowed, Unit: rubles}
	if inTokens.StopsInMoney(UnitTokens) || inTokens.StopsInMoney(rubles) || free.StopsInMoney(rubles) {
		t.Fatal("a sum in tokens or no sum is a sum in money")
	}
}
