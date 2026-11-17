// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"maps"
	"slices"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/canon"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// Pace counts the cards of a saga written since a moment: the pace of the reading since its last continuation.
type Pace interface {
	CardsSince(ctx context.Context, projectID string, since time.Time) (int, error)
}

// Status is the reading of the books of a saga as its status says it (specs/2026-11-13-desk-consent.md CN3.8), the port of
// the status the app gives: what it waits for and its state next to the counts.
type Status struct {
	Settings Settings
	Consents Consents
	Saga     Saga
	Pace     Pace
	Samples  Measures
	Now      func() time.Time
}

// unit is the unit the reading counts in by the settings now: the currency of the prices, or tokens without them.
func (status Status) unit() domain.Unit {
	to, _ := targetOf(status.Settings)
	return to.unit
}

// standing is the consent that acts, counted in the unit of the settings now (viewOf), and how it stands: «цены
// изменились» whenever its sum is in money the settings no longer price in its currency — whatever pause it keeps, since
// only a sum named again lets it go on (CN3.4) —, or the pause it keeps.
func (status Status) standing(ctx context.Context, projectID string) (ConsentView, domain.Pause, error) {
	consent, err := status.Consents.Consent(ctx, projectID)
	if err != nil {
		return ConsentView{}, "", err
	}
	view := viewOf(consent, status.Settings)
	if view.Consent == nil {
		return view, "", nil
	}
	switch consent := view.Consent; {
	case consent.PricesChanged(status.unit()):
		return view, domain.PausePrices, nil
	case consent.State == domain.StatePaused:
		return view, consent.Pause, nil
	default:
		return view, "", nil
	}
}

// Waiting is what the reading waits for: the consent — none acts —, or its pause; nothing while it goes.
func (status Status) Waiting(ctx context.Context, projectID string) (canon.PreparationWaiting, error) {
	view, pause, err := status.standing(ctx, projectID)
	switch {
	case err != nil:
		return "", err
	case view.Consent == nil:
		return canon.PreparationWaitingConsent, nil
	case pause != "":
		return canon.PreparationWaitingPaused, nil
	default:
		return "", nil
	}
}

// State is the state of the reading by the counts of the status (CN3.8). It is there while the saga has chapters to read,
// names or threads that wait for the model, or a pause; a saga whose consent acts and whose only chapters, names or threads
// left did not read or come together is done with them (specs/2026-11-15-desk-reading-rest.md RR2.2) — without a consent
// that acts their marks do not act, and they wait as unread. The names and the threads are said only once no chapter is
// left to read (RR2.1). The time left goes by the pace since the last continuation — a pause is not in it — or, without one,
// by the estimate.
func (status Status) State(ctx context.Context, projectID string, counts canon.IndexingPreparation) (*canon.IndexingReading, error) {
	view, pause, err := status.standing(ctx, projectID)
	if err != nil {
		return nil, err
	}
	toRead, err := status.Saga.ToRead(ctx, projectID, false)
	if err != nil {
		return nil, err
	}
	left := len(toRead.Unread)
	waits := toRead.NamesWait || toRead.ThreadsWait
	pending := left > 0 || waits
	reading := &canon.IndexingReading{
		ChaptersRead: counts.ChaptersRead, ChaptersTotal: counts.ChaptersTotal,
		NamesWait: left == 0 && toRead.NamesWait, ThreadsWait: left == 0 && toRead.ThreadsWait, Unit: string(status.unit()),
	}
	if left == 0 && (toRead.NamesUnread != "" || toRead.ThreadsUnread != "") {
		reading.UnreadRest = &canon.IndexingReadingRest{Names: reasonOrNone(toRead.NamesUnread), Threads: reasonOrNone(toRead.ThreadsUnread)}
	}
	if view.Source != "" {
		source := string(view.Source)
		reading.Source = &source
	}
	if len(toRead.Marked) > 0 {
		reading.Unread = &canon.IndexingReadingUnread{Chapters: len(toRead.Marked), Reason: reasonOf(toRead.Marked)}
	}
	consent := view.Consent
	switch {
	case consent == nil:
		if !pending {
			return nil, nil
		}
		reading.State = canon.ReadingWaitingConsent
		if view.Reason != "" {
			reason := view.Reason
			reading.Reason = &reason
		}
		return reading, nil
	case pause != "":
		reading.State = canon.ReadingPaused
		value := string(pause)
		reading.Pause = &value
	case pending:
		reading.State = canon.ReadingGoing
	case reading.Unread != nil || reading.UnreadRest != nil:
		reading.State = canon.ReadingDone
	default:
		return nil, nil
	}
	reading.Unit, reading.Spent, reading.Approximate = string(consent.Unit), consent.Count(), consent.Spent.Estimated
	reading.StopAt, reading.Step = consent.StopAt, consent.Step
	if consent.Source == domain.SourceAccount {
		// The two limits of the tier of the account for 30 days, as the site told them last (AI3.7).
		if limits, ok := status.Settings.AccountLimits(ctx); ok {
			reading.Limits = &canon.IndexingReadingLimits{
				Characters: canon.IndexingReadingLimit{Used: limits.Characters.Used, Limit: limits.Characters.Limit},
				Work:       canon.IndexingReadingLimit{Used: limits.Work.Used, Limit: limits.Work.Limit},
			}
		}
	}
	if reading.State == canon.ReadingGoing && left > 0 {
		seconds, err := status.remaining(ctx, projectID, consent.ResumedAt, toRead)
		if err != nil {
			return nil, err
		}
		reading.RemainingSeconds = seconds
	}
	return reading, nil
}

// reasonOrNone is the reason of a mark, or none.
func reasonOrNone(reason string) *string {
	if reason == "" {
		return nil
	}
	return &reason
}

// reasonOf is why most of the chapters that did not read did not, the same at every poll: of as many, the first by name.
func reasonOf(marked []Unread) string {
	counts := map[string]int{}
	for _, mark := range marked {
		counts[mark.Reason]++
	}
	reasons := slices.Sorted(maps.Keys(counts))
	slices.SortStableFunc(reasons, func(a, b string) int { return counts[b] - counts[a] })
	return reasons[0]
}

// paceFrom is how many cards since the last continuation make a pace: fewer say little of a long reading.
const paceFrom = 3

// remaining is the time the chapters left take: by the cards written since the last continuation, or by the estimate.
func (status Status) remaining(ctx context.Context, projectID string, resumed time.Time, toRead ToRead) (*int, error) {
	left := len(toRead.Unread)
	cards, err := status.Pace.CardsSince(ctx, projectID, resumed)
	if err != nil {
		return nil, err
	}
	if elapsed := status.Now().Sub(resumed); cards >= paceFrom && elapsed > 0 {
		seconds := max(int(elapsed.Seconds()*float64(left)/float64(cards)), 1)
		return &seconds, nil
	}
	// Without a pace the estimate says the time: it is the one that measures the chapters.
	if toRead, err = status.Saga.ToRead(ctx, projectID, true); err != nil {
		return nil, err
	}
	input := domain.Input{Books: toRead.Books, Carded: toRead.Carded, CardedCharacters: toRead.CardedCharacters, Parallel: 1}
	if ai := status.Settings.AI(); ai.Source == aisettings.SourceAccount {
		input.Host, input.Reasoning = status.Settings.SiteHost(), true
	} else if own := ai.Own; own != nil {
		input.Parallel, input.Host, input.Model = own.Advanced.Parallel, own.Host(), own.CardsModel
		input.Reasoning = own.Advanced.Reasoning != aisettings.ReasoningNone
	}
	input.Unread = unreadOf(toRead.Unread)
	if status.Samples != nil {
		if input.Sample, err = status.Samples.Measured(ctx, projectID); err != nil {
			return nil, err
		}
	}
	seconds := max(domain.Count(input).Seconds, 1)
	return &seconds, nil
}
