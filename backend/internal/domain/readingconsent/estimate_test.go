// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"slices"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
)

func price(text string) *aisettings.Price {
	value, ok := aisettings.ParsePrice(text)
	if !ok {
		panic(text)
	}
	return &value
}

func chapters(count, characters int) []int {
	out := make([]int, count)
	for i := range out {
		out[i] = characters
	}
	return out
}

// specs/2026-11-13-desk-consent.md CN1.2, CN1.4: the example of DA8.2 — 600 chapters of 10 000 characters, one book, a
// calibration of 3 characters a token, a model without reasoning — with the register of names and the thread layer, as the
// table of the spec counts it by hand.
func TestTheEstimateOfDA82IsTheTableCountedByHand(t *testing.T) {
	input := Input{
		Unread: chapters(600, 10_000), Books: 1, Calibration: 330, CalibrationChecked: true,
		Reasoning: false, Parallel: 4, Host: "api.deepseek.com",
		Prices: &aisettings.Prices{Currency: aisettings.CurrencyUSD, In: *price("0.30"), Out: *price("1.20")},
	}
	estimate := Count(input)
	// Cards: (4 700 + 2·3 300 + 10 000)·600·1,1 = 14 058 000 in, 7 000·600·1,1 = 4 620 000 out. Names: 250 and 25 a card.
	// The layer: a book of 600 digests — 2 500 + 400·600 in, 2 000 out; the reduction — 2 500 + 2 000 in, 3 000 out; 150
	// threads checked by 10 chapters of 3 300 — 35 500 in and 400 out each; what was missed — 7 500 in, 2 000 out; 30 added
	// threads checked — 35 500 in and 400 out each; all of it ·1,1.
	const cardsIn, cardsOut = 14_058_000, 4_620_000
	const namesIn, namesOut = 150_000, 15_000
	const layerIn, layerOut = 7_308_950, 86_900
	if estimate.Chapters != 600 || estimate.Input != cardsIn+namesIn+layerIn || estimate.Output != cardsOut+namesOut+layerOut {
		t.Fatalf("the tokens = %d chapters, %d in, %d out", estimate.Chapters, estimate.Input, estimate.Output)
	}
	if estimate.Kind != KindRough || estimate.Unit != Unit(aisettings.CurrencyUSD) {
		t.Fatalf("the kind and the unit = %s %s", estimate.Kind, estimate.Unit)
	}
	// 21 516 950·0,30 + 4 721 900·1,20 dollars for a million: 6,455085 + 5,66628 = 12,121365.
	if estimate.Amount == nil || *estimate.Amount != 12_121_365 {
		t.Fatalf("the amount = %v", estimate.Amount)
	}
	// The layer is 7 395 850 tokens of 26 238 850.
	if share := estimate.LayerShare; share < 0.2818 || share > 0.2820 {
		t.Fatalf("the share of the layer = %f", share)
	}
	// ⌈600/4⌉·120 s of chapters, 60 s of names, ⌈183/4⌉·30 s of the layer.
	if estimate.Seconds != 18_000+60+1_380 {
		t.Fatalf("the time = %d", estimate.Seconds)
	}
	// The stop the window offers is above the estimate: 12,12·1,2 = 14,55 — the next step of 1, 1,5, 2, 2,5, 3, 4, 5, 6, 8 is 15.
	if estimate.SuggestedStop != 15_000_000 {
		t.Fatalf("the suggested stop = %d", estimate.SuggestedStop)
	}
	if estimate.SampleChapters != 3 {
		t.Fatalf("the chapters of a sample = %d", estimate.SampleChapters)
	}
	// The edit of a chapter: its card and pass at the average chapter, and the names of all the cards again.
	edit := estimate.ChapterEdit
	if edit.Input != 23_430+150_000 || edit.Output != 7_700+15_000 || edit.Amount == nil {
		t.Fatalf("the edit of a chapter = %+v", edit)
	}
	for _, assumption := range []Assumption{AssumptionCalibrationChecked, AssumptionNoReasoning, AssumptionLayerByStructure, AssumptionRepeats} {
		if !slices.Contains(estimate.Assumptions, assumption) {
			t.Fatalf("the assumptions %v miss %s", estimate.Assumptions, assumption)
		}
	}

	// With reasoning the output of a chapter is 19 000.
	input.Reasoning = true
	if reasoning := Count(input); reasoning.Output != 19_000*600*11/10+namesOut+layerOut || !slices.Contains(reasoning.Assumptions, AssumptionReasoning) {
		t.Fatalf("with reasoning = %d out, %v", reasoning.Output, reasoning.Assumptions)
	}
}

// CN1.4: the calibration of the check is taken only when it measured the model of the reading now; else the fallback of
// the top of the measurement. Without prices there is no money: the unit is tokens and the stop is in tokens.
func TestTheCalibrationAndThePricesOfTheEstimate(t *testing.T) {
	checked := Count(Input{Unread: chapters(10, 10_000), Books: 1, Calibration: 330, CalibrationChecked: true, Parallel: 4})
	fallback := Count(Input{Unread: chapters(10, 10_000), Books: 1, Parallel: 4})
	if fallback.Input <= checked.Input || !slices.Contains(fallback.Assumptions, AssumptionCalibrationFallback) {
		t.Fatalf("the fallback = %d in (checked %d), %v", fallback.Input, checked.Input, fallback.Assumptions)
	}
	if fallback.Amount != nil || fallback.Unit != UnitTokens {
		t.Fatalf("without prices = %v %s", fallback.Amount, fallback.Unit)
	}
	// The stop in tokens is above in + out by the same rule.
	if total := fallback.Input + fallback.Output; fallback.SuggestedStop < total*12/10 || fallback.SuggestedStop > total*3 {
		t.Fatalf("the stop in tokens %d for %d", fallback.SuggestedStop, total)
	}
	// 680 ₽ of the board gives 1 000 ₽, 19 million tokens 25 million.
	for estimate, want := range map[int64]int64{680_000_000: 1_000_000_000, 19_000_000: 25_000_000, 12_121_365: 15_000_000, 33_000: 40_000, 2_000_000: 2_500_000} {
		if got := suggested(estimate); got != want {
			t.Errorf("the stop above %d = %d; want %d", estimate, got, want)
		}
	}
}

// CN1.5: an estimate by the sample changes only what the sample measured — the tokens of a character of a chapter and the
// output of a chapter; the constant part of a chapter, the names and the layer stay by the formula. A sample of three
// short chapters does not multiply its tokens by the chapters of the saga.
func TestTheEstimateByTheSampleChangesOnlyWhatItMeasured(t *testing.T) {
	saga := Input{Unread: chapters(600, 10_000), Books: 1, Parallel: 4, Host: "api.deepseek.com", Model: "reader"}
	rough := Count(saga)
	// Three chapters of 2 000 characters at 0,33 tokens a character: 3·(14 700 + 2·660) in, 6 500 out a chapter, 90 s each.
	saga.Sample = &Sample{
		Host: "api.deepseek.com", Model: "reader", Chapters: 3, Characters: 6_000,
		Tokens: aisettings.Tokens{Input: 3 * (14_700 + 2*660), Output: 3 * 6_500}, Seconds: 270,
	}
	sampled := Count(saga)
	if sampled.Kind != KindSample || !slices.Contains(sampled.Assumptions, AssumptionCalibrationSample) {
		t.Fatalf("the kind = %s %v", sampled.Kind, sampled.Assumptions)
	}
	byFormula := estimateOf(Input{Unread: chapters(600, 10_000), Books: 1, Parallel: 4}, terms{calibration: 330, outputOfChapter: 6_500, secondsOfChapter: 90})
	if sampled.Input != byFormula.Input || sampled.Output != byFormula.Output {
		t.Fatalf("by the sample %d/%d, by the formula with what it measured %d/%d", sampled.Input, sampled.Output, byFormula.Input, byFormula.Output)
	}
	naive := int64(600) * (3*(14_700+2*660) + 3*6_500) / 3
	if sampled.Input+sampled.Output == naive || rough.Input == sampled.Input {
		t.Fatalf("the sample was multiplied, or measured nothing: %d", sampled.Input)
	}
	// The time is the time of a chapter of the sample over «Запросов одновременно».
	if sampled.Seconds != 150*90+60+46*30 {
		t.Fatalf("the time by the sample = %d", sampled.Seconds)
	}
	// Another model or another host after the sample — the estimate is rough again.
	for _, other := range []Sample{{Host: "api.openai.com", Model: "reader"}, {Host: "api.deepseek.com", Model: "writer"}} {
		sample := *saga.Sample
		sample.Host, sample.Model = other.Host, other.Model
		saga.Sample = &sample
		if again := Count(saga); again.Kind != KindRough || again.Input != rough.Input {
			t.Fatalf("a sample of %+v = %s %d", other, again.Kind, again.Input)
		}
	}
}

// CN1.6: a saga whose chapters are read while the names or the layer wait for the model counts only them; 0 chapters is no
// error. The sample has as many chapters as there are without a card, three at most.
func TestTheEstimateOfASagaWhoseChaptersAreRead(t *testing.T) {
	names := Count(Input{Carded: 40, Books: 2, NamesWait: true, Parallel: 4})
	if names.Chapters != 0 || names.Input != 40*250 || names.Output != 40*25 || names.LayerShare != 0 || names.SampleChapters != 0 {
		t.Fatalf("only the names = %+v", names)
	}
	layer := Count(Input{Carded: 40, Books: 2, CardedCharacters: 400_000, ThreadsWait: true, Parallel: 4})
	if layer.Chapters != 0 || layer.Input == 0 || layer.LayerShare != 1 {
		t.Fatalf("only the layer = %+v", layer)
	}
	if nothing := Count(Input{Carded: 40, Books: 2, Parallel: 4}); nothing.Input != 0 || nothing.Output != 0 || nothing.Seconds != 0 {
		t.Fatalf("nothing to read = %+v", nothing)
	}
	if two := Count(Input{Unread: chapters(2, 5_000), Books: 1, Parallel: 4}); two.SampleChapters != 2 {
		t.Fatalf("the sample of two chapters = %d", two.SampleChapters)
	}
}
