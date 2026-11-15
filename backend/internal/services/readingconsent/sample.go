// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// SampleJob is the Job of a sample: its status, the code it failed with, the chapters it reads by their text and the host
// its window named.
type SampleJob struct {
	JobID     string
	Status    string
	ErrorCode string
	Chapters  []Chapter
	Host      string
}

// Live says the sample still reads.
func (job SampleJob) Live() bool {
	return job.Status == string(jobs.StatusQueued) || job.Status == string(jobs.StatusRunning) || job.Status == string(jobs.StatusCanceling)
}

// SampleCost is the cost of a request of the sample in a currency.
type SampleCost struct {
	Micros   int64
	Currency string
}

// SampleRequest is a request of the cards or passes of the sample as the journal keeps it.
type SampleRequest struct {
	At          time.Time
	Duration    time.Duration
	Host, Model string
	TokensKnown bool
	Tokens      aisettings.Tokens
	Cost        *SampleCost
	Estimated   bool
}

// SampleStore keeps the samples: the last of a saga, a new one under the key of its request, its requests by the journal.
type SampleStore interface {
	LastSample(ctx context.Context, projectID string) (*SampleJob, error)
	QueueSample(ctx context.Context, projectID, host string, chapters []Chapter, key string, at time.Time) (string, error)
	SampleRequests(ctx context.Context, jobID string) ([]SampleRequest, error)
}

// SampleCard is a card of a chapter the sample read, as the window shows it: the place of the chapter in words, the
// retelling, the events and who is in it (the parts of the card of the map).
type SampleCard struct {
	ChapterID string
	Place     string
	Summary   string
	Events    []string
	People    []string
	// Characters are the characters of the text of the chapter: the measure of the sample counts them.
	Characters int
}

// SampleCards read the cards of the chapters of a sample that have one for the text the sample read.
type SampleCards interface {
	Cards(ctx context.Context, projectID string, chapters []Chapter) ([]SampleCard, error)
}

// SampleCancel cancels the Job of a sample the standard way.
type SampleCancel interface {
	CancelJob(ctx context.Context, actorID, jobID string) error
}

// The states of a sample (CN2.4).
const (
	SampleNone      = "none"
	SampleQueued    = "queued"
	SampleRunning   = "running"
	SampleSucceeded = "succeeded"
	SampleCanceled  = "canceled"
	SampleFailed    = "failed"
)

// Sample is getReadingSample (CN2.4): the state, why it failed, how far it is, the cards it read, its time — the time of its
// requests by the journal, without the idle time between its runs — and what it spent in tokens and in the currency of the
// settings, approximate when an estimate is in it.
type Sample struct {
	State       string
	Failure     string
	Done, Total int
	Cards       []SampleCard
	Seconds     int
	Tokens      aisettings.Tokens
	Amount      *int64
	Unit        domain.Unit
	Approximate bool
}

// CodeNoSample refuses a sample of the helper of the account: it reads by the tier of the site, not for money.
const CodeNoSample = "reading.no_sample"

// Samples are the operations of the sample of three chapters (CN2).
type Samples struct {
	Settings Settings
	Owners   Owners
	Consents Consents
	Saga     Saga
	Store    SampleStore
	Cards    SampleCards
	Cancel   SampleCancel
	Now      func() time.Time
}

// Start is startReadingSample (CN2.1, CN2.3): only the owner, with the helper connected and no consent that acts; it chooses
// the first chapters of the published text without a card — at most three, in the order of the books — and writes them into
// the subject of its Job. The same key answers the same sample, and so does a live sample of the saga.
func (samples Samples) Start(ctx context.Context, actorID, projectID, key string) (Sample, error) {
	if err := samples.Owners.Owned(ctx, actorID, projectID); err != nil {
		return Sample{}, err
	}
	settings := samples.Settings.AI()
	own := settings.Own
	if !samples.Settings.Connected(ctx) || own == nil && settings.Source != aisettings.SourceAccount {
		return Sample{}, ErrNotConnected
	}
	if settings.Source == aisettings.SourceAccount {
		// The helper of the account reads by the tier of the site, not for money: it has no sample
		// (specs/2026-11-13-desk-account-ai.md AI3.4).
		return Sample{}, &Conflict{Code: CodeNoSample}
	}
	consent, err := samples.Consents.Consent(ctx, projectID)
	if err != nil {
		return Sample{}, err
	}
	if consent.Acts(domain.SourceOwn, own.Host()) {
		return Sample{}, &Conflict{Code: "reading.consent_given"}
	}
	last, err := samples.Store.LastSample(ctx, projectID)
	if err != nil {
		return Sample{}, err
	}
	if last == nil || !last.Live() {
		toRead, err := samples.Saga.ToRead(ctx, projectID, false)
		if err != nil {
			return Sample{}, err
		}
		if len(toRead.Unread) == 0 {
			return Sample{}, &Conflict{Code: "reading.nothing_to_read"}
		}
		chapters := toRead.Unread[:min(len(toRead.Unread), domain.SampleChapters)]
		if _, err := samples.Store.QueueSample(ctx, projectID, own.Host(), chapters, key, samples.Now()); err != nil && !errors.Is(err, jobs.ErrConflict) {
			return Sample{}, err
		}
	}
	return samples.read(ctx, projectID)
}

// Get is getReadingSample.
func (samples Samples) Get(ctx context.Context, actorID, projectID string) (Sample, error) {
	if err := samples.Owners.Owned(ctx, actorID, projectID); err != nil {
		return Sample{}, err
	}
	return samples.read(ctx, projectID)
}

// Stop is cancelReadingSample: the live sample of the saga is canceled the standard way; its cards stay.
func (samples Samples) Stop(ctx context.Context, actorID, projectID string) error {
	if err := samples.Owners.Owned(ctx, actorID, projectID); err != nil {
		return err
	}
	last, err := samples.Store.LastSample(ctx, projectID)
	if err != nil || last == nil || !last.Live() {
		return err
	}
	return samples.Cancel.CancelJob(ctx, actorID, last.JobID)
}

func (samples Samples) read(ctx context.Context, projectID string) (Sample, error) {
	last, err := samples.Store.LastSample(ctx, projectID)
	if err != nil {
		return Sample{}, err
	}
	unit := domain.UnitTokens
	var prices *aisettings.Prices
	if own := samples.Settings.AI().Own; own != nil {
		unit = domain.UnitOf(own.Advanced)
		if found, ok := own.Advanced.Prices(); ok {
			prices = &found
		}
	}
	sample := Sample{State: SampleNone, Unit: unit}
	if last == nil {
		return sample, nil
	}
	sample.State, sample.Failure, sample.Total = stateOf(*last), failureOf(*last), len(last.Chapters)
	if sample.Cards, err = samples.Cards.Cards(ctx, projectID, last.Chapters); err != nil {
		return Sample{}, err
	}
	sample.Done = len(sample.Cards)
	requests, err := samples.Store.SampleRequests(ctx, last.JobID)
	if err != nil {
		return Sample{}, err
	}
	sample.Seconds = busySeconds(requests)
	var micros int64
	for _, request := range requests {
		sample.Tokens = sample.Tokens.Add(request.Tokens)
		sample.Approximate = sample.Approximate || request.Estimated
		if prices != nil && request.Cost != nil && request.Cost.Currency == string(prices.Currency) {
			micros += request.Cost.Micros
		} else if prices != nil && request.TokensKnown {
			micros += prices.Cost(request.Tokens)
		}
	}
	if prices != nil {
		sample.Amount = &micros
	}
	return sample, nil
}

// stateOf is the state of a sample by its Job.
func stateOf(job SampleJob) string {
	switch jobs.Status(job.Status) {
	case jobs.StatusQueued:
		return SampleQueued
	case jobs.StatusRunning, jobs.StatusCanceling:
		return SampleRunning
	case jobs.StatusSucceeded:
		return SampleSucceeded
	case jobs.StatusCanceled:
		return SampleCanceled
	default:
		return SampleFailed
	}
}

// failureOf is why a sample did not read, by the code of its Job: the reasons of the pauses of the reading, the helper that
// left, or another failure — a provider that did not answer too, which the sample says as before: «ответ помощника не
// пришёл» (specs/2026-11-15-desk-reading-no-answer.md NA1.4).
func failureOf(job SampleJob) string {
	if stateOf(job) != SampleFailed {
		return ""
	}
	switch code := job.ErrorCode; {
	case code == sagamemory.StopNoAnswer.Code():
		return "other"
	case strings.HasPrefix(code, "reading."):
		return strings.TrimPrefix(code, "reading.")
	case code == "cards.consent":
		return "helper"
	default:
		return "other"
	}
}

// busySeconds is the time of the requests of the sample by the journal: the union of their intervals — the requests that
// went at once count once, the idle time between the runs of its Job not at all.
func busySeconds(requests []SampleRequest) int {
	type span struct{ from, to time.Time }
	spans := make([]span, 0, len(requests))
	for _, request := range requests {
		spans = append(spans, span{from: request.At, to: request.At.Add(request.Duration)})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from.Before(spans[j].from) })
	var total time.Duration
	var current *span
	for i := range spans {
		switch {
		case current == nil:
			current = &spans[i]
		case !spans[i].from.After(current.to):
			if spans[i].to.After(current.to) {
				current.to = spans[i].to
			}
		default:
			total += current.to.Sub(current.from)
			current = &spans[i]
		}
	}
	if current != nil {
		total += current.to.Sub(current.from)
	}
	return int((total + time.Second - 1) / time.Second)
}

// Measured is what the last sample that read measured for the estimate (CN1.5): the provider and the model of its requests
// — one of each, or no measure —, its chapters with their characters, the tokens of their cards and passes and the time of
// a chapter one request after another. A sample whose requests the provider sent no usage for measured nothing.
func (samples Samples) Measured(ctx context.Context, projectID string) (*domain.Sample, error) {
	last, err := samples.Store.LastSample(ctx, projectID)
	if err != nil || last == nil || stateOf(*last) != SampleSucceeded {
		return nil, err
	}
	cards, err := samples.Cards.Cards(ctx, projectID, last.Chapters)
	if err != nil || len(cards) == 0 {
		return nil, err
	}
	requests, err := samples.Store.SampleRequests(ctx, last.JobID)
	if err != nil || len(requests) == 0 {
		return nil, err
	}
	measured := &domain.Sample{Host: requests[0].Host, Model: requests[0].Model, Chapters: len(cards)}
	var serial time.Duration
	for _, request := range requests {
		if !request.TokensKnown || request.Host != measured.Host || request.Model != measured.Model {
			return nil, nil
		}
		measured.Tokens = measured.Tokens.Add(request.Tokens)
		serial += request.Duration
	}
	for _, card := range cards {
		measured.Characters += int64(card.Characters)
	}
	measured.Seconds = int(serial / time.Second)
	return measured, nil
}
