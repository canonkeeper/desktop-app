// SPDX-License-Identifier: AGPL-3.0-only

// Package readingconsent is what the desktop app does with the reading of the books of a saga by the author's own AI
// (specs/2026-11-13-desk-consent.md): the estimate before the consent, the consent and its pauses, the check of every
// request of the reading at the gateway with its count, the sample of three chapters and the state of the reading the
// status of a saga says.
package readingconsent

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/domain/sagamemory"
)

var (
	// ErrNotFound is a saga that is not the author's own, or none.
	ErrNotFound = errors.New("reading consent: saga not found")
	// ErrNotConnected is the reading asked for while the helper is not connected.
	ErrNotConnected = errors.New("reading consent: the helper is not connected")
)

// Settings are the settings of «Свой ИИ» as they are now and the calibration of the last check that measured the model of
// the reading they name; of the helper of the account (specs/2026-11-13-desk-account-ai.md AI3.4, AI3.7) the host of its
// site and the two limits of its tier for the reading as the site told them last.
type Settings interface {
	AI() aisettings.Settings
	Calibration() (float64, bool)
	Connected(ctx context.Context) bool
	SiteHost() string
	AccountLimits(ctx context.Context) (AccountLimits, bool)
}

// AccountLimits are the two limits of the tier of the account for the reading of the books over 30 days (AI3.4, AI3.7): the
// characters of the inputs of the card steps, and the work of the reading in tokens.
type AccountLimits struct {
	Characters, Work domain.Limit
}

// target is whom the reading of the settings now goes to (AI1.4, AI3.4): the source, the host the consent names and the unit
// it counts in — the provider of «Свой ИИ», or the site of the account, which counts in tokens and takes no money.
type target struct {
	source domain.Source
	host   string
	unit   domain.Unit
}

// targetOf is the target of the settings now; false — no helper the reading could go to.
func targetOf(settings Settings) (target, bool) {
	ai := settings.AI()
	if ai.Source == aisettings.SourceAccount {
		return target{source: domain.SourceAccount, host: settings.SiteHost(), unit: domain.UnitTokens}, true
	}
	if own := ai.Own; own != nil {
		return target{source: domain.SourceOwn, host: own.Host(), unit: domain.UnitOf(own.Advanced)}, true
	}
	return target{unit: domain.UnitTokens}, false
}

// Owners say whether the author owns a live saga.
type Owners interface {
	Owned(ctx context.Context, actorID, projectID string) error
}

// ToRead is what the published text of a saga has to read: the characters of each chapter without a card (or with a mark
// «не прочиталась» that acts left out), the chapters with a card and their characters, the books with chapters, and whether
// the names or the threads wait for the model though every chapter is read — or are marked, with why
// (specs/2026-11-15-desk-reading-rest.md RR2.1).
type ToRead struct {
	Unread []Chapter
	// Marked are the chapters without a card marked «не прочиталась» for their current text whose marks act (CN3.6, RR1.4):
	// left out of Unread.
	Marked           []Unread
	Carded           int
	CardedCharacters int64
	Books            int
	NamesWait        bool
	ThreadsWait      bool
	// NamesUnread and ThreadsUnread are why the names and the threads are marked while their marks act (RR1.2, RR1.3).
	NamesUnread, ThreadsUnread string
}

// Chapter is a chapter of the published text by its current text: its characters and the parts its card reads by, when
// measured.
type Chapter struct {
	ID, Hash   string
	Characters int
	Parts      int
}

// Measure is what the characters of the paragraphs of a chapter say of it: the characters it holds and the parts its card
// reads by — the one division of the card writer (sagamemory.PartsOf; specs/2026-11-17-long-chapters.md LC1.6).
func Measure(lengths []int) (characters, parts int) {
	for _, length := range lengths {
		characters += length
	}
	return characters, len(sagamemory.PartsOf(lengths))
}

// unreadOf are the chapters an estimate counts.
func unreadOf(chapters []Chapter) []domain.Chapter {
	unread := make([]domain.Chapter, 0, len(chapters))
	for _, chapter := range chapters {
		unread = append(unread, domain.Chapter{Characters: int64(chapter.Characters), Parts: chapter.Parts})
	}
	return unread
}

// Saga reads what the published text of a saga has to read; the characters of its chapters only when measured — the
// status polled by the page counts them only for a time by the estimate.
type Saga interface {
	ToRead(ctx context.Context, projectID string, measured bool) (ToRead, error)
	// Inputs are the characters of the inputs of the card steps of the chapters, by the function that builds them — what
	// the site of the account counts (specs/2026-11-13-desk-account-ai.md AI3.4).
	Inputs(ctx context.Context, projectID string, chapters []Chapter) (int64, error)
}

// Measures give what the last sample of a saga that read measured, or nil.
type Measures interface {
	Measured(ctx context.Context, projectID string) (*domain.Sample, error)
}

// Estimate is getReadingEstimate: the estimate with the provider it goes to and whether the model works on the computer.
type Estimate struct {
	domain.Estimate
	Host        string
	Local       bool
	NamesWait   bool
	ThreadsWait bool
	// Account is the estimate of the helper of the account instead of money (AI3.4); nil for «Свой ИИ».
	Account *domain.AccountReading
}

// GetEstimate is getReadingEstimate (CN1.3): the model is not called.
type GetEstimate struct {
	Settings Settings
	Owners   Owners
	Saga     Saga
	Samples  Measures
}

func (get GetEstimate) Execute(ctx context.Context, actorID, projectID string) (Estimate, error) {
	if err := get.Owners.Owned(ctx, actorID, projectID); err != nil {
		return Estimate{}, err
	}
	settings := get.Settings.AI()
	if !get.Settings.Connected(ctx) {
		return Estimate{}, ErrNotConnected
	}
	if settings.Source == aisettings.SourceAccount {
		return get.ofAccount(ctx, projectID)
	}
	if settings.Own == nil {
		return Estimate{}, ErrNotConnected
	}
	own := *settings.Own
	toRead, err := get.Saga.ToRead(ctx, projectID, true)
	if err != nil {
		return Estimate{}, err
	}
	input := domain.Input{
		Carded: toRead.Carded, CardedCharacters: toRead.CardedCharacters, Books: toRead.Books,
		NamesWait: toRead.NamesWait, ThreadsWait: toRead.ThreadsWait,
		Reasoning: own.Advanced.Reasoning != aisettings.ReasoningNone, Parallel: own.Advanced.Parallel,
		Host: own.Host(), Model: own.CardsModel,
	}
	input.Unread = unreadOf(toRead.Unread)
	input.Calibration, input.CalibrationChecked = get.Settings.Calibration()
	if prices, ok := own.Advanced.Prices(); ok {
		input.Prices = &prices
	}
	if get.Samples != nil {
		if input.Sample, err = get.Samples.Measured(ctx, projectID); err != nil {
			return Estimate{}, err
		}
	}
	return Estimate{
		Estimate: domain.Count(input), Host: own.Host(), Local: own.Local(), NamesWait: toRead.NamesWait, ThreadsWait: toRead.ThreadsWait,
	}, nil
}

// ofAccount is the estimate of the helper of the account (AI3.4): no money, no sample and no stop sum — the characters the
// reading of the chapters without a card takes, by the function that builds the input of their card steps (exact for new
// chapters, at most for chapters the site read before), the work of the reading by the formula, and both limits of the tier
// as the site told them last, with which ends the reading first.
func (get GetEstimate) ofAccount(ctx context.Context, projectID string) (Estimate, error) {
	toRead, err := get.Saga.ToRead(ctx, projectID, true)
	if err != nil {
		return Estimate{}, err
	}
	// The site reads with one place of the two of the app and a model that reasons.
	input := domain.Input{
		Carded: toRead.Carded, CardedCharacters: toRead.CardedCharacters, Books: toRead.Books,
		NamesWait: toRead.NamesWait, ThreadsWait: toRead.ThreadsWait, Reasoning: true, Parallel: 1, Host: get.Settings.SiteHost(),
	}
	input.Unread = unreadOf(toRead.Unread)
	characters, err := get.Saga.Inputs(ctx, projectID, toRead.Unread)
	if err != nil {
		return Estimate{}, err
	}
	estimate := domain.Count(input)
	estimate.Amount, estimate.Unit, estimate.SuggestedStop, estimate.SampleChapters = nil, domain.UnitTokens, 0, 0
	estimate.ChapterEdit.Amount = nil
	limits, told := get.Settings.AccountLimits(ctx)
	account := domain.AccountReadingOf(characters, estimate.Input+estimate.Output, limits.Characters, limits.Work, told)
	return Estimate{
		Estimate: estimate, Host: get.Settings.SiteHost(), NamesWait: toRead.NamesWait, ThreadsWait: toRead.ThreadsWait, Account: &account,
	}, nil
}

// FieldError names the field of a write that is not valid by its name in the contract ("stopAt").
type FieldError = domain.FieldError

// Conflict is a write the reading cannot do now, with the code of the refusal.
type Conflict struct {
	Code string
}

func (err *Conflict) Error() string { return "reading consent: " + err.Code }
