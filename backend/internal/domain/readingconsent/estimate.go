// SPDX-License-Identifier: AGPL-3.0-only

// Package readingconsent is the reading of the books of a saga by the author's own AI in the desktop app
// (specs/2026-11-13-desk-consent.md): the estimate before the consent, the consent with its stop sum and its count, the
// pauses and the sample of three chapters — the rules and the transitions; who stores and who asks is elsewhere.
package readingconsent

import (
	"math"

	"canonkeeper/backend/internal/domain/aisettings"
)

// Unit is what a sum of the reading is counted in: the ISO 4217 code of the currency of the prices, or tokens when the
// author named no prices (CN1.2).
type Unit string

// UnitTokens counts a sum in tokens: in and out.
const UnitTokens Unit = "tokens"

// UnitOf is the unit the settings count a sum in now.
func UnitOf(advanced aisettings.Advanced) Unit {
	if prices, ok := advanced.Prices(); ok {
		return Unit(prices.Currency)
	}
	return UnitTokens
}

// Kind is how much an estimate knows: by the formula alone, or by the sample of this provider and model.
type Kind string

const (
	KindRough  Kind = "rough"
	KindSample Kind = "sample"
)

// Assumption names what an estimate took for granted; the window may say it in words.
type Assumption string

const (
	// AssumptionCalibrationChecked: the tokens of a character are those «Проверить связь» measured for the model of the
	// reading.
	AssumptionCalibrationChecked Assumption = "calibration_checked"
	// AssumptionCalibrationFallback: no check measured the model of the reading; the top of the measurement is taken.
	AssumptionCalibrationFallback Assumption = "calibration_fallback"
	// AssumptionCalibrationSample: the tokens of a character and the output of a chapter are those of the sample.
	AssumptionCalibrationSample Assumption = "calibration_sample"
	// AssumptionReasoning and AssumptionNoReasoning: the output of a chapter with or without reasoning.
	AssumptionReasoning   Assumption = "reasoning"
	AssumptionNoReasoning Assumption = "no_reasoning"
	// AssumptionNamesByCards: the register of names reads the names of every card.
	AssumptionNamesByCards Assumption = "names_by_cards"
	// AssumptionLayerByStructure: the thread layer by its structure and the measurement of the site — coarser than the
	// chapters, within half to twice.
	AssumptionLayerByStructure Assumption = "layer_by_structure"
	// AssumptionRepeats: a tenth more for the repeats.
	AssumptionRepeats Assumption = "repeats_margin"
)

// The terms of the formula (CN1.4; specs/2026-11-07-desktop-app.md DA8.2; the measurement of Planning anchor).
const (
	// cardInstructions are the tokens of the instructions of a card and of its pass (≈ 14 000 characters).
	cardInstructions = 4_700
	// cardInPass are the tokens of the card the pass reads beside the chapter.
	cardInPass = 10_000
	// outputPlain and outputReasoning are the output of a card and its pass, without and with reasoning.
	outputPlain     = 7_000
	outputReasoning = 19_000
	// namesIn and namesOut are the register of names for a card: its names in, who is who out.
	namesIn  = 250
	namesOut = 25
	// The thread layer: the instructions of a call, the digest of a chapter in the call of its book, the answer of a book,
	// of the reduction and of what was missed, a thread for every four chapters checked by up to ten chapters of its text
	// with a short verdict, and a fifth more of threads added by what was missed.
	layerInstructions = 2_500
	layerDigest       = 400
	layerBookOut      = 2_000
	layerReduceOut    = 3_000
	layerGapOut       = 2_000
	layerVerifyOut    = 400
	layerKeptIn       = 3_000
	layerEvidence     = 10
	// The time of a chapter (card and pass), of the register and of a call of the layer.
	chapterSeconds = 120
	namesSeconds   = 60
	layerSeconds   = 30
)

// Sample is what the sample of three chapters measured: the provider and the model it read with, its chapters, their
// characters, the tokens of their cards and passes and the time of their requests one after another.
type Sample struct {
	Host, Model string
	Chapters    int
	Characters  int64
	Tokens      aisettings.Tokens
	Seconds     int
}

// Input is what an estimate counts: the characters of each chapter of the published text without a card, the chapters
// with one and their characters, the books, whether the names or the threads wait for the model though every chapter is
// read, the calibration of the check of the connection and whether it measured the model of the reading now, the
// reasoning, «Запросов одновременно», the prices, the provider and the model of the reading, and the sample.
type Input struct {
	Unread             []int
	Carded             int
	CardedCharacters   int64
	Books              int
	NamesWait          bool
	ThreadsWait        bool
	Calibration        float64
	CalibrationChecked bool
	Reasoning          bool
	Parallel           int
	Prices             *aisettings.Prices
	Host, Model        string
	Sample             *Sample
}

// Edit is what reading a chapter again costs after the author edited it: its card and pass, and the names of all cards.
type Edit struct {
	Input, Output int64
	Amount        *int64
}

// Estimate is what reading the rest of the saga will take (CN1.3): its chapters, the tokens in and out, the time, the
// amount in millionths of the currency when there are prices, the unit, the share of the thread layer in the tokens, the
// kind, the assumptions, the stop the window offers in the unit, the chapters of a sample and the edit of a chapter.
type Estimate struct {
	Chapters       int
	Input, Output  int64
	Seconds        int
	Amount         *int64
	Unit           Unit
	LayerShare     float64
	Kind           Kind
	Assumptions    []Assumption
	SuggestedStop  int64
	SampleChapters int
	ChapterEdit    Edit
}

// SampleChapters is how many chapters a sample reads: those without a card, three at most.
const SampleChapters = 3

// terms are the measured or assumed numbers of a chapter.
type terms struct {
	calibration      float64
	outputOfChapter  int64
	secondsOfChapter int
}

// Count is the estimate (CN1.4, CN1.5). The sample counts only while it was read by the provider and the model of the
// reading now, and it changes only what it measured: the tokens of a character of a chapter and the output and the time
// of a chapter. The constant part of a chapter, the names and the layer stay by the formula.
func Count(input Input) Estimate {
	measure := terms{calibration: aisettings.FallbackCalibration, outputOfChapter: outputPlain, secondsOfChapter: chapterSeconds}
	assumptions := []Assumption{AssumptionCalibrationFallback}
	if input.CalibrationChecked && input.Calibration > 0 {
		measure.calibration, assumptions = input.Calibration, []Assumption{AssumptionCalibrationChecked}
	}
	reasoning := AssumptionNoReasoning
	if input.Reasoning {
		measure.outputOfChapter, reasoning = outputReasoning, AssumptionReasoning
	}
	kind := KindRough
	if sample := input.Sample; sample != nil && sample.Chapters > 0 && sample.Characters > 0 && sample.Host == input.Host && sample.Model == input.Model {
		perCharacter := (float64(sample.Tokens.Input) - float64(sample.Chapters)*(cardInstructions+cardInPass)) / (2 * float64(sample.Characters))
		if perCharacter > 0 {
			measure.calibration = perCharacter * 1000
		}
		measure.outputOfChapter = max(sample.Tokens.Output/int64(sample.Chapters), 1)
		if sample.Seconds > 0 {
			measure.secondsOfChapter = max(sample.Seconds/sample.Chapters, 1)
		}
		kind, assumptions = KindSample, []Assumption{AssumptionCalibrationSample}
	}
	estimate := estimateOf(input, measure)
	estimate.Kind = kind
	estimate.Assumptions = append(assumptions, reasoning, AssumptionNamesByCards, AssumptionLayerByStructure, AssumptionRepeats)
	return estimate
}

func estimateOf(input Input, measure terms) Estimate {
	parallel := max(input.Parallel, 1)
	estimate := Estimate{Chapters: len(input.Unread), Unit: UnitTokens, SampleChapters: min(len(input.Unread), SampleChapters)}
	if input.Prices != nil {
		estimate.Unit = Unit(input.Prices.Currency)
	}
	textOf := func(characters int64) int64 {
		return int64(math.Round(float64(characters) * measure.calibration / 1000))
	}
	all := input.Carded + len(input.Unread)
	characters := input.CardedCharacters
	var cardsIn, cardsOut int64
	for _, chapter := range input.Unread {
		characters += int64(chapter)
		cardsIn += cardInstructions + 2*textOf(int64(chapter)) + cardInPass
		cardsOut += measure.outputOfChapter
	}
	cardsIn, cardsOut = repeated(cardsIn), repeated(cardsOut)
	var namesTokensIn, namesTokensOut int64
	seconds := ceilDiv(len(input.Unread), parallel) * measure.secondsOfChapter
	if len(input.Unread) > 0 || input.NamesWait {
		namesTokensIn, namesTokensOut = int64(all)*namesIn, int64(all)*namesOut
		seconds += namesSeconds
	}
	var layerIn, layerOut int64
	if (len(input.Unread) > 0 || input.ThreadsWait) && all > 0 {
		average := characters / int64(all)
		books := int64(max(input.Books, 1))
		threads := int64(math.Ceil(float64(all) / 4))
		added := int64(math.Ceil(float64(threads) / 5))
		check := layerInstructions + int64(min(all, layerEvidence))*textOf(average)
		layerIn = books*layerInstructions + int64(all)*layerDigest + // a call for each book over the digests of its cards
			layerInstructions + layerBookOut*books + // the reduction of the threads of all books
			threads*check + // the check of each thread by the text of its chapters
			layerInstructions + layerBookOut*books + layerKeptIn + // what was missed
			added*check // the check of what it added
		layerOut = layerBookOut*books + layerReduceOut + threads*layerVerifyOut + layerGapOut + added*layerVerifyOut
		layerIn, layerOut = repeated(layerIn), repeated(layerOut)
		seconds += ceilDiv(int(books+1+threads+1+added), parallel) * layerSeconds
	}
	estimate.Input = cardsIn + namesTokensIn + layerIn
	estimate.Output = cardsOut + namesTokensOut + layerOut
	estimate.Seconds = seconds
	if total := estimate.Input + estimate.Output; total > 0 {
		estimate.LayerShare = float64(layerIn+layerOut) / float64(total)
	}
	average := int64(0)
	if all > 0 {
		average = characters / int64(all)
	}
	estimate.ChapterEdit = Edit{
		Input:  repeated(cardInstructions+2*textOf(average)+cardInPass) + int64(max(all, 1))*namesIn,
		Output: repeated(measure.outputOfChapter) + int64(max(all, 1))*namesOut,
	}
	if prices := input.Prices; prices != nil {
		amount := prices.Cost(aisettings.Tokens{Input: estimate.Input, Output: estimate.Output})
		edit := prices.Cost(aisettings.Tokens{Input: estimate.ChapterEdit.Input, Output: estimate.ChapterEdit.Output})
		estimate.Amount, estimate.ChapterEdit.Amount = &amount, &edit
		estimate.SuggestedStop = suggested(amount)
	} else {
		estimate.SuggestedStop = suggested(estimate.Input + estimate.Output)
	}
	return estimate
}

// repeated adds the tenth of the repeats (r = 1,1), rounded up.
func repeated(tokens int64) int64 { return (tokens*11 + 9) / 10 }

func ceilDiv(count, by int) int { return (count + by - 1) / by }

// ladder are the steps a stop sum is rounded up to, in tenths of a power of ten.
var ladder = []int64{10, 15, 20, 25, 30, 40, 50, 60, 80, 100}

// suggested is the stop the window offers (CN1.3): a fifth above the estimate, rounded up to the next of 1, 1,5, 2, 2,5,
// 3, 4, 5, 6, 8 of its power of ten — a round sum the reading will most likely not reach.
func suggested(estimate int64) int64 {
	target := max((estimate*12+9)/10, 1)
	for power := int64(1); power <= math.MaxInt64/100; power *= 10 {
		for _, step := range ladder {
			if value := step * power / 10; value >= target && value > 0 {
				return value
			}
		}
	}
	return target
}
