// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"errors"
	"testing"
)

func priced(text string) *Price {
	price, ok := ParsePrice(text)
	if !ok {
		panic(text)
	}
	return &price
}

// specs/2026-11-13-desk-consent.md CN1.2: the cost of a request is (in − cache)·price in + cache·price of the cache +
// out·price out in millionths of the currency, rounded up; without the price of the cache the cache goes at the price in;
// without both prices there is no money.
func TestTheCostOfARequestIsOneFunctionOfTheDomain(t *testing.T) {
	ruble := Advanced{Currency: CurrencyRUB, PriceIn: priced("30"), PriceOut: priced("120")}
	prices, ok := ruble.Prices()
	if !ok || prices.Currency != CurrencyRUB {
		t.Fatalf("the prices of rubles = %+v, %v", prices, ok)
	}
	// 1 000 000 in, 400 000 of them cached, 100 000 out: 600 000·30 + 400 000·30 + 100 000·120 = 42 rubles.
	if cost := prices.Cost(Tokens{Input: 1_000_000, Cached: 400_000, Output: 100_000}); cost != 42_000_000 {
		t.Fatalf("without the price of the cache = %d", cost)
	}
	ruble.PriceCache = priced("3")
	prices, _ = ruble.Prices()
	// 600 000·30 + 400 000·3 + 100 000·120 = 18 + 1,2 + 12 = 31,2 rubles.
	if cost := prices.Cost(Tokens{Input: 1_000_000, Cached: 400_000, Output: 100_000}); cost != 31_200_000 {
		t.Fatalf("with the price of the cache = %d", cost)
	}
	// A part of a millionth goes up: 1 token at 0.0001 for a million is 0.0000000001 of the currency — one millionth.
	tiny := Advanced{Currency: CurrencyUSD, PriceIn: priced("0.0001"), PriceOut: priced("0.0001")}
	tinyPrices, _ := tiny.Prices()
	if cost := tinyPrices.Cost(Tokens{Input: 1}); cost != 1 {
		t.Fatalf("a part of a millionth = %d", cost)
	}
	// The highest price at the most tokens a journal keeps does not overflow.
	top := Advanced{Currency: CurrencyEUR, PriceIn: priced("1000000"), PriceOut: priced("1000000")}
	topPrices, _ := top.Prices()
	if cost := topPrices.Cost(Tokens{Input: 1 << 40, Output: 1 << 40}); cost <= 0 {
		t.Fatalf("the highest price overflows: %d", cost)
	}
	for _, advanced := range []Advanced{{Currency: CurrencyUSD}, {Currency: CurrencyUSD, PriceIn: priced("1")}, {Currency: CurrencyUSD, PriceOut: priced("1")}} {
		if _, ok := advanced.Prices(); ok {
			t.Fatalf("prices without both = %+v", advanced)
		}
	}
	if usage := (Tokens{Input: 10, Cached: 4, Output: 3}); usage.Sum() != 13 {
		t.Fatalf("the tokens of a sum in tokens = %d; want in + out", usage.Sum())
	}
}

// CN1.1: the prices have a currency — rubles, dollars, yuan, euros, the dollar by default — and a third price of the
// cache; a price goes up to 1 000 000 for a million in any currency.
func TestThePricesHaveACurrencyAndAPriceOfTheCache(t *testing.T) {
	settings, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek", Advanced: AdvancedDraft{
		Currency: text("RUB"), PriceIn: text("6000"), PriceOut: text("1000000"), PriceCache: text("0.5"),
	}}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	advanced := settings.Own.Advanced
	if advanced.Currency != CurrencyRUB || advanced.PriceIn.String() != "6000" || advanced.PriceOut.String() != "1000000" ||
		advanced.PriceCache.String() != "0.5" {
		t.Fatalf("the prices = %+v", advanced)
	}
	plain, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek"}}.Settings()
	if err != nil || plain.Own.Advanced.Currency != CurrencyUSD || plain.Own.Advanced.PriceCache != nil {
		t.Fatalf("the defaults = %+v, %v", plain.Own.Advanced, err)
	}
	for field, draft := range map[string]AdvancedDraft{
		"own.advanced.currency":                {Currency: text("GBP")},
		"own.advanced.priceInPerMillion":       {PriceIn: text("1000000.0001")},
		"own.advanced.priceCachedInPerMillion": {PriceCache: text("1e3")},
	} {
		_, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek", Advanced: draft}}.Settings()
		var refused *FieldError
		if !errors.As(err, &refused) || refused.Field != field {
			t.Errorf("%+v = %v; want the field %s", draft, err, field)
		}
	}
}

// CN1.1: a write of the settings that leaves out the currency and the price of the cache keeps them — the page of
// «Настройки → Помощник» writes «Дополнительно» without them; the price of the cache goes only when the write clears it.
func TestAWriteWithoutTheCurrencyKeepsIt(t *testing.T) {
	previous, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek", Advanced: AdvancedDraft{
		Currency: text("CNY"), PriceIn: text("2"), PriceOut: text("8"), PriceCache: text("0.5"),
	}}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	next, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek", Advanced: AdvancedDraft{
		PriceIn: text("3"), PriceOut: text("9"), Parallel: number(2),
	}}}.SettingsOver(previous)
	if err != nil {
		t.Fatal(err)
	}
	advanced := next.Own.Advanced
	if advanced.Currency != CurrencyCNY || advanced.PriceCache.String() != "0.5" || advanced.PriceIn.String() != "3" || advanced.Parallel != 2 {
		t.Fatalf("a write without the currency = %+v", advanced)
	}
	cleared, err := Draft{Source: "own", Own: &OwnDraft{Preset: "deepseek", Advanced: AdvancedDraft{
		Currency: text("EUR"), ClearPriceCache: true,
	}}}.SettingsOver(previous)
	if err != nil || cleared.Own.Advanced.Currency != CurrencyEUR || cleared.Own.Advanced.PriceCache != nil {
		t.Fatalf("a write that clears the price of the cache = %+v, %v", cleared.Own.Advanced, err)
	}
	first, err := Draft{Source: "own", Own: &OwnDraft{Preset: "openai"}}.SettingsOver(None())
	if err != nil || first.Own.Advanced.Currency != CurrencyUSD {
		t.Fatalf("a first write = %+v, %v", first.Own, err)
	}
}

// CN3.3: a request without the usage of the provider is counted by an estimate — the input by the size of the request
// and the calibration, the output by the characters that went by.
func TestARequestWithoutUsageIsCountedByAnEstimate(t *testing.T) {
	usage := Unseen(15_000, 3_000, 400)
	// 15 000 bytes are 10 000 characters at 1,5 bytes a character: 4 000 tokens; 3 000 characters out: 1 200.
	if usage.Input != 4_000 || usage.Output != 1_200 || usage.Cached != 0 {
		t.Fatalf("the estimate = %+v", usage)
	}
	if usage := Unseen(1, 0, 400); usage.Input != 1 || usage.Output != 0 {
		t.Fatalf("a part of a token goes up: %+v", usage)
	}
}
