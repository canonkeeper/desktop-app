// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// The kinds of the Jobs whose requests read the books of a saga.
const (
	KindCards = "chapter_cards"
	KindLayer = "saga_layer"
)

// ReadingJob is the Job of a request of the reading as the check knows it: its saga and kind, whether it runs, and of the
// sample of three chapters the host its window named.
type ReadingJob struct {
	ProjectID  string
	Kind       string
	Live       bool
	Sample     bool
	SampleHost string
}

// Jobs find the Job of a request of the reading.
type Jobs interface {
	ReadingJob(ctx context.Context, jobID string) (ReadingJob, bool, error)
}

// Count is what a request adds to the count of the consent whose count began at Since: its tokens and its cost, whether
// they are an estimate, and the unit of the settings it went with — a consent without a sum follows it (domain In).
type Count struct {
	ProjectID string
	Since     time.Time
	Unit      domain.Unit
	Tokens    aisettings.Tokens
	Cost      *domain.Cost
	Estimated bool
}

// Ledger keeps the consent and its count: the pause of the check, and the count of an answer written with its row of the
// journal in one transaction — the journal is the author's to clear, the count is not.
type Ledger interface {
	Consent(ctx context.Context, projectID string) (*domain.Consent, error)
	Pause(ctx context.Context, projectID string, since time.Time, pause domain.Pause, at time.Time) (bool, error)
	Settle(ctx context.Context, entry modeljournal.Request, count *Count) error
	// SettleSample writes the row of the journal of a request of the sample and the request into the requests of the sample
	// at once (specs/2026-11-14-desk-journal.md JR2.6): what the sample spent and measured is apart from the journal, which the
	// author clears.
	SettleSample(ctx context.Context, projectID string, entry modeljournal.Request) error
}

// AdmitRequest is a request of the reading the gateway asks about (CN3.3).
type AdmitRequest struct {
	JobID, Purpose string
	Host, Model    string
	RequestBytes   int64
	Settings       aisettings.Settings
}

// Admissions is the check of every request of the reading at the gateway (specs/2026-11-13-desk-consent.md CN3.3) — the one
// place it is made. It holds the reserves of the requests of each saga that go, in memory: the gateway is one in the process,
// and a reserve whose count was not written stays in what was spent until the process ends.
type Admissions struct {
	Settings Settings
	Jobs     Jobs
	Ledger   Ledger
	Now      func() time.Time

	mu    sync.Mutex
	going map[string]int64
}

// purposesOf are the purposes a Job of the reading asks the model for: the cards Job its cards, passes and names, the sample
// its cards and passes alone, the layer Job its steps.
func purposesOf(job ReadingJob) map[string]bool {
	switch {
	case job.Kind == KindCards && job.Sample:
		return map[string]bool{string(coresteps.Card): true, string(coresteps.CardPass): true}
	case job.Kind == KindCards:
		return map[string]bool{string(coresteps.Card): true, string(coresteps.CardPass): true, string(coresteps.Names): true}
	case job.Kind == KindLayer:
		return map[string]bool{
			string(coresteps.LayerBook): true, string(coresteps.LayerReduce): true, string(coresteps.LayerVerify): true,
			string(coresteps.LayerGap): true,
		}
	default:
		return nil
	}
}

// refusedConsent is why a request without a consent that acts, or of a Job that is not of the reading, goes nowhere.
const refusedConsent = "consent"

// Admit lets a request of the reading go with its reserve, or refuses it with why (CN3.3): its Job runs and reads with this
// purpose; a sample reads its cards at the host its window named, apart from the consent; any other request goes only under
// a consent that acts and is allowed, within its sum with the reserves of the requests of the saga that go — over it, or with
// a sum in money whose prices changed, the consent is paused in the same transaction.
func (admissions *Admissions) Admit(ctx context.Context, request AdmitRequest) (*Admitted, string, error) {
	job, found, err := admissions.Jobs.ReadingJob(ctx, request.JobID)
	if err != nil {
		return nil, "", err
	}
	if !found || !job.Live || !purposesOf(job)[request.Purpose] {
		return nil, refusedConsent, nil
	}
	source, unit := domain.SourceAccount, domain.UnitTokens
	own := request.Settings.Own
	switch {
	case request.Settings.Source == aisettings.SourceAccount:
		// The helper of the account (specs/2026-11-13-desk-account-ai.md AI3.4): its consent names the site; no sample, no money
		// and no stop sum.
		if job.Sample {
			return nil, refusedConsent, nil
		}
	case own == nil:
		return nil, refusedConsent, nil
	default:
		source, unit = domain.SourceOwn, domain.UnitOf(own.Advanced)
	}
	if job.Sample {
		if job.SampleHost != request.Host {
			return nil, refusedConsent, nil
		}
		return &Admitted{admissions: admissions, project: job.ProjectID, sample: true, settings: request.Settings, source: source}, "", nil
	}
	admissions.mu.Lock()
	defer admissions.mu.Unlock()
	consent, err := admissions.Ledger.Consent(ctx, job.ProjectID)
	if err != nil {
		return nil, "", err
	}
	calibration, _ := admissions.Settings.Calibration()
	var reserve int64
	if consent != nil && source == domain.SourceOwn {
		var prices *aisettings.Prices
		if found, ok := own.Advanced.Prices(); ok {
			prices = &found
		}
		reserve = domain.Reserve(request.RequestBytes, own.Advanced.MaxOutput, calibration, prices, consent.Unit)
	}
	decision, pause := domain.Admit(consent, source, request.Host, unit, admissions.going[job.ProjectID], reserve)
	switch decision {
	case domain.Refuse:
		if consent != nil && consent.State == domain.StatePaused {
			return nil, string(consent.Pause), nil
		}
		return nil, refusedConsent, nil
	case domain.Halt:
		if _, err := admissions.Ledger.Pause(ctx, job.ProjectID, consent.Since, pause, admissions.Now()); err != nil {
			return nil, "", err
		}
		return nil, string(pause), nil
	}
	if admissions.going == nil {
		admissions.going = map[string]int64{}
	}
	admissions.going[job.ProjectID] += reserve
	return &Admitted{
		admissions: admissions, project: job.ProjectID, since: consent.Since, reserve: reserve, settings: request.Settings,
		calibration: calibration, source: source,
	}, "", nil
}

// goingOf is the reserve of the requests of a saga that go, in the unit of its consent.
func (admissions *Admissions) goingOf(projectID string) int64 {
	admissions.mu.Lock()
	defer admissions.mu.Unlock()
	return admissions.going[projectID]
}

// Admitted is a request of the reading let go, with its reserve.
type Admitted struct {
	admissions  *Admissions
	project     string
	since       time.Time
	reserve     int64
	sample      bool
	settings    aisettings.Settings
	calibration float64
	// source is the helper the request went to: the money fields — prices, a cost, a currency — are «Свой ИИ»'s alone
	// (specs/2026-11-13-desk-account-ai.md AI3.4); the account counts in tokens.
	source domain.Source
}

// own is the profile of «Свой ИИ» whose prices count the request; nil for the helper of the account.
func (ticket *Admitted) own() *aisettings.Own {
	if ticket.source != domain.SourceOwn {
		return nil
	}
	return ticket.settings.Own
}

// Settle replaces the reserve by what the request cost and writes the count of the consent with the row of the journal at
// once (CN3.3). A request the provider sent no usage for is counted by an estimate — the input by its size and the
// calibration, the output by the characters that went by — unless it failed before the provider began its answer (answered:
// any event of the answer came), which the provider takes nothing for. A row that failed leaves the reserve in what was
// spent. The sample writes its row with its request of the sample (JR2.6): it is not in the count of the consent.
func (ticket *Admitted) Settle(ctx context.Context, entry modeljournal.Request, answered bool) {
	tokens, estimated := aisettings.Tokens{}, false
	if entry.Tokens != nil {
		tokens = aisettings.Tokens{Input: entry.Tokens.Input, Cached: entry.Tokens.CachedInput, Output: entry.Tokens.Output}
	} else if entry.Outcome != modeljournal.OutcomeFailed || answered {
		calibration := ticket.calibration
		if calibration <= 0 {
			calibration, _ = ticket.admissions.Settings.Calibration()
		}
		tokens, estimated = aisettings.Unseen(entry.RequestBytes, entry.OutputCharacters, calibration), true
		entry.Estimated = true
		if own := ticket.own(); own != nil {
			if prices, ok := own.Advanced.Prices(); ok {
				entry.Cost = &modeljournal.Cost{Micros: prices.Cost(tokens), Currency: string(prices.Currency)}
			}
		}
	}
	if ticket.sample {
		_ = ticket.admissions.Ledger.SettleSample(ctx, ticket.project, entry)
		return
	}
	var count *Count
	{
		count = &Count{ProjectID: ticket.project, Since: ticket.since, Unit: domain.UnitTokens, Tokens: tokens, Estimated: estimated}
		if own := ticket.own(); own != nil {
			count.Unit = domain.UnitOf(own.Advanced)
		}
		if entry.Cost != nil {
			count.Cost = &domain.Cost{Micros: entry.Cost.Micros, Currency: aisettings.Currency(entry.Cost.Currency)}
		}
	}
	err := ticket.admissions.Ledger.Settle(ctx, entry, count)
	admissions := ticket.admissions
	admissions.mu.Lock()
	defer admissions.mu.Unlock()
	if err == nil {
		admissions.going[ticket.project] -= ticket.reserve
	}
}

// Release lets the reserve of a request that never went go, without a row of the journal and without a count
// (specs/2026-11-14-desk-journal.md JR1.1): its record did not lie, or it was refused between the check and the sending. It
// pauses nothing.
func (ticket *Admitted) Release(context.Context) {
	if ticket.sample {
		return
	}
	admissions := ticket.admissions
	admissions.mu.Lock()
	defer admissions.mu.Unlock()
	admissions.going[ticket.project] -= ticket.reserve
}

// Stop pauses the reading at a refusal of the site of the account that stops it (specs/2026-11-13-desk-account-ai.md
// AI3.5): the characters of the tier, the fence of the work of the reading, an app the site does not take, the reading of the
// site unavailable.
func (ticket *Admitted) Stop(ctx context.Context, reason string) {
	if ticket.sample {
		return
	}
	pause, ok := domain.PauseOfAccount(reason)
	if !ok {
		return
	}
	_, _ = ticket.admissions.Ledger.Pause(ctx, ticket.project, ticket.since, pause, ticket.admissions.Now())
}

// Failed pauses the reading at a refusal of the provider that stops it (CN3.4): the funds, the key and the model at once, no
// connection when the client of the app tried for the last time. A refusal of the sample ends the sample alone.
func (ticket *Admitted) Failed(ctx context.Context, failure aisettings.Failure, last bool) {
	if ticket.sample {
		return
	}
	pause, ok := domain.PauseOf(failure, last)
	if !ok {
		return
	}
	_, _ = ticket.admissions.Ledger.Pause(ctx, ticket.project, ticket.since, pause, ticket.admissions.Now())
}
