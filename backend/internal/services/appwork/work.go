// SPDX-License-Identifier: AGPL-3.0-only

// Package appwork is the count of the work of the desktop app on the models of the site of its account
// (specs/2026-11-13-desk-account-ai.md AI2, AI3): it opens the permit of a turn or an upload of materials in the transaction
// of their start, takes the permit of a turn again when a worker takes it, opens the permit of a reading at its first step,
// charges and returns the corrections, closes every permit once its work ended, and keeps in the memory of the process
// what the gateway needs — the permit of a Job, the reason the site last refused it, whether a request of it was dropped
// mid-stream — and the refusals that repeat. No id of a permit leaves the process; the site decides every unit.
package appwork

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// The terms of the calls of the count (AI2.1, AI2.7): an open of a permit and a close wait ten seconds each; the close of a
// turn is a call of its own after the terminal transaction.
const (
	OpenTerm  = 10 * time.Second
	CloseTerm = 10 * time.Second
	// maxNumbers bounds the numbers a reading tries after a restart before it gives up.
	maxNumbers = 64
)

// Site is the client of the app to the site of its account (adapters/siteapi).
type Site interface {
	// LinkMark is the mark of the link of the pass held now — "" without one: the site knows the refs of one device only.
	LinkMark(salt []byte) string
	OpenPermit(ctx context.Context, kind domain.Kind, ref string) (domain.Permit, error)
	ChargeUnit(ctx context.Context, permitID, ref string) error
	ReturnUnit(ctx context.Context, permitID, ref string) (bool, error)
	ClosePermit(ctx context.Context, permitID string, outcome domain.Outcome) (domain.Permit, error)
}

// Row is the work of a Job as the store keeps it: its kind, its source, the mark of the link it started under, the number
// of its permit — whose ref the site of that link knows — and whether the unit came back.
type Row struct {
	Kind     domain.Kind
	Source   domain.Source
	Link     string
	Number   int
	Returned domain.Returned
}

// Rows are the rows of the work outside the transactions of their starts.
type Rows interface {
	Work(ctx context.Context, jobID string) (Row, bool, error)
	// KeepNumber keeps the number of the permit the site answered for the next ref of the work (AI2.3).
	KeepNumber(ctx context.Context, jobID string, number int) error
	KeepReturned(ctx context.Context, jobID string, returned domain.Returned) error
	// Pending are the Jobs whose question is pending (AI2.9).
	Pending(ctx context.Context) ([]string, error)
	// Answering counts the turns of the account of the link whose Job is not over (specs/2026-11-15-desk-turns-here.md TH2.1).
	Answering(ctx context.Context, link string) (int64, error)
}

// Link is the link of the app with the account: an open and a close make what it told of the account stale (AI2.11).
type Link interface {
	Refresh()
}

// held is the permit of a Job the process holds: its kind and source, the permit, the mark of the link it was taken under,
// the chat of a turn, whether a worker took the turn — the stop of the app closes only the turns that run (AI2.7) — and
// whether a run before took it (AI2.9).
type held struct {
	kind    domain.Kind
	source  domain.Source
	permit  domain.Permit
	link    string
	number  int
	chatID  string
	begun   bool
	carried bool
}

// closing is a close that did not reach the site: it goes again before the next open (AI2.7).
type closing struct {
	permitID string
	outcome  domain.Outcome
	kind     domain.Kind
	link     string
	dropped  bool
}

// charge is the unit of a correction the site charged under the permit of its turn: a correction the turn did not take is
// returned under the same permit, whether the turn ended meanwhile or not (AI2.8). It lives for the time a correction is
// sent.
type charge struct {
	permitID string
	link     string
	at       time.Time
}

// chargeKept bounds how long the charge of a correction is kept: the sending of a correction ends well before.
const chargeKept = 5 * time.Minute

// SettleAfter is when the word of the site on a question left pending is final (AI2.9): the lease of a permit of a turn
// (five minutes) and the minute the site reads a dropped stream on.
const SettleAfter = 6 * time.Minute

// Work is the count of the work of one run of the app.
type Work struct {
	Site   Site
	Rows   Rows
	Salt   []byte
	Link   Link
	Now    func() time.Time
	Logger *log.Logger

	mu       sync.Mutex
	live     map[string]*held
	closing  map[string]closing
	charged  map[string]charge
	reasons  map[string]string
	dropped  map[string]bool
	refusals *domain.Refusals
	// later are the questions left pending, with when the site's word on each is asked (AI2.9).
	later map[string]time.Time
}

// New is the count of a run.
func New(site Site, rows Rows, salt []byte, link Link, logger *log.Logger) *Work {
	return &Work{
		Site: site, Rows: rows, Salt: salt, Link: link, Now: time.Now, Logger: logger,
		live: map[string]*held{}, closing: map[string]closing{}, charged: map[string]charge{}, reasons: map[string]string{},
		dropped:  map[string]bool{},
		refusals: domain.NewRefusals(),
		later:    map[string]time.Time{},
	}
}

func (work *Work) log(line string) {
	if work.Logger != nil {
		work.Logger.Print(line)
	}
}

func (work *Work) refresh() {
	if work.Link != nil {
		work.Link.Refresh()
	}
}

// refreshAtOnce makes what the site told stale after its refusal «разом» (specs/2026-11-15-desk-turns-here.md TH2.3): the
// numbers the block of the refusal shows are not older than the refusal it explains.
func (work *Work) refreshAtOnce(refusal error) {
	if exceeded, ok := quota.AsExceeded(refusal); ok && exceeded.Key == quota.KeyActiveTurns {
		work.refresh()
	}
}

// Mark is the mark of the link of the app now (AI2.3): a work keeps it in the row of its start, and its permit is asked by
// its ref only under the same link — the site closed the permits of an unlinked device as canceled, and knows no ref of a
// new one.
func (work *Work) Mark() string { return work.Site.LinkMark(work.Salt) }

// Answering is the turns of the account this app still answers under the link now, of any saga
// (specs/2026-11-15-desk-turns-here.md TH2.1): only the app knows which of its questions still go; without a link, none.
func (work *Work) Answering(ctx context.Context) (int64, error) {
	link := work.Mark()
	if link == "" {
		return 0, nil
	}
	return work.Rows.Answering(ctx, link)
}

// Open opens the permit of a turn or of an upload of materials at the site (AI2.1, AI3.8), the last step of the transaction
// of its start: a refusal the app remembers goes back at once; the closes that did not reach the site go first; no answer
// is asked once more with the same ref — the site answers the same permit without a second unit —; a refusal of the tier is
// the refusal of the site; no second answer is a counter that did not answer, whose unit the site may give back itself.
func (work *Work) Open(ctx context.Context, opening quota.Work) error {
	kind := domain.Kind(opening.Kind)
	if code := work.remembered(string(kind), opening.ChatID); code != "" {
		return &quota.CounterRefusal{Code: code}
	}
	work.flush(ctx)
	permit, err := work.openTwice(ctx, kind, domain.Ref(work.Salt, opening.JobID, 0))
	if errors.Is(err, accountdomain.ErrUnreachable) {
		return &quota.CounterUnreachable{MayReturn: true}
	}
	if err != nil {
		refusal := work.refusalOf(err, string(kind), opening.ChatID)
		work.refreshAtOnce(refusal)
		return refusal
	}
	if !permit.Live() {
		// A permit the same ref already ended: the Job is new, so this is a failure of the site — the turn does not start.
		return &quota.CounterUnreachable{}
	}
	work.mu.Lock()
	work.live[opening.JobID] = &held{kind: kind, source: domain.SourceAccount, permit: permit, link: work.Mark(), chatID: opening.ChatID}
	work.mu.Unlock()
	work.refresh()
	return nil
}

func (work *Work) open(ctx context.Context, kind domain.Kind, ref string) (domain.Permit, error) {
	ctx, cancel := context.WithTimeout(ctx, OpenTerm)
	defer cancel()
	permit, err := work.Site.OpenPermit(ctx, kind, ref)
	if err != nil && ctx.Err() != nil {
		return domain.Permit{}, accountdomain.ErrUnreachable
	}
	return permit, err
}

// openTwice asks the permit of the ref with one repeat when the site did not answer (askTwice): the same ref answers the
// same permit, so the repeat spends no second unit (AI2.1, AI2.2).
func (work *Work) openTwice(ctx context.Context, kind domain.Kind, ref string) (domain.Permit, error) {
	var permit domain.Permit
	err := askTwice(ctx, func(ctx context.Context) error {
		var err error
		permit, err = work.Site.OpenPermit(ctx, kind, ref)
		return err
	})
	return permit, err
}

// refusalOf is the refusal of a start the page reads as on the site.
func (work *Work) refusalOf(err error, kind, chatID string) error {
	switch {
	case errors.Is(err, accountdomain.ErrPassInvalid), errors.Is(err, accountdomain.ErrNotHeld), errors.Is(err, accountdomain.ErrPassReplaced):
		return quota.ErrCounterUnlinked
	}
	refusal, ok := domain.AsRefusal(err)
	if !ok {
		return &quota.CounterUnreachable{}
	}
	if key, isQuota := quota.KeyOfCode(refusal.Code); isQuota {
		if key == quota.KeyActiveTurns {
			return &quota.Exceeded{Key: key}
		}
		return quota.DailyExceeded(key, 0, work.Now())
	}
	answer := domain.AnswerOf(*refusal)
	if answer.Remember != domain.RememberNothing {
		work.remember(answer.Remember, kind, chatID)
		return &quota.CounterRefusal{Code: work.remembered(kind, chatID)}
	}
	return &quota.CounterUnreachable{}
}

func (work *Work) remembered(kind, chatID string) string {
	work.mu.Lock()
	defer work.mu.Unlock()
	switch code := work.refusals.Of(kind, chatID, work.Now()); code {
	case domain.CodeValidation:
		return quota.CounterUnfitCode
	default:
		return code
	}
}

func (work *Work) remember(memory domain.Memory, kind, chatID string) {
	work.mu.Lock()
	defer work.mu.Unlock()
	work.refusals.Heard(memory, kind, chatID, work.Now())
}

// Begin takes the work of a turn before the model (AI2.3): a turn without a row or on «Свой ИИ» goes to its own source; a
// turn of the account asks its permit by the same ref, with one repeat, whenever a worker takes it — after the open of its
// start in the same process too, since the turn may have waited in the queue longer than the lease, and after a stop of the
// process — and
// reads its state as the site tells it now: alive — it goes under it; gone by unused while the turn waited — the next
// number opens a new one through the fence of the site, kept once the site answered it, and a refusal of the tier ends the
// turn with its code; ended after requests — the turn ends. A turn started under another link asks nothing: the site closed
// the permits of that device as canceled, without a return, and knows no ref of the new one. A turn that ends here keeps
// whether its question came back, as far as the answer tells it.
func (work *Work) Begin(ctx context.Context, run researchdomain.TurnRun) (string, error) {
	row, ok, err := work.Rows.Work(ctx, run.JobID)
	if err != nil {
		return "", err
	}
	if !ok || row.Source != domain.SourceAccount {
		work.mu.Lock()
		work.live[run.JobID] = &held{kind: domain.KindTurn, source: domain.SourceOwn, chatID: run.ChatID, begun: true}
		work.mu.Unlock()
		return "", nil
	}
	if row.Link != work.Mark() {
		return researchdomain.CodeAccountUnlinked, work.keepBegun(ctx, run.JobID, domain.ReturnedNo)
	}
	// A turn this process did not open a run before took: the end of that run may have dropped a request of it (AI2.9).
	work.mu.Lock()
	_, opened := work.live[run.JobID]
	work.mu.Unlock()
	carried := !opened
	permit, err := work.openTwice(ctx, domain.KindTurn, domain.Ref(work.Salt, run.JobID, row.Number))
	unused := err == nil && permit.WentUnused()
	if unused {
		next := row.Number + 1
		permit, err = work.openTwice(ctx, domain.KindTurn, domain.Ref(work.Salt, run.JobID, next))
		if err == nil {
			// The site answered the permit of the next ref: it knows that ref now.
			if err := work.Rows.KeepNumber(ctx, run.JobID, next); err != nil {
				return "", err
			}
			row.Number = next
		}
	}
	if code := beginFailure(err); code != "" {
		if code == researchdomain.CodeTurnsAtOnce {
			// The numbers of «разом» the app holds were older than this refusal (TH2.3).
			work.refresh()
		}
		returned := domain.ReturnedPending
		switch {
		case code == researchdomain.CodeAccountUnlinked:
			returned = domain.ReturnedNo
		case unused && (code == researchdomain.CodeQuestionsOver || code == researchdomain.CodeTurnsAtOnce):
			// The unit of the permit that went by unused came back, and the site spent no other.
			returned = domain.ReturnedYes
		}
		return code, work.keepBegun(ctx, run.JobID, returned)
	}
	if !permit.Live() {
		return researchdomain.CodePermitEnded, work.keepBegun(ctx, run.JobID, carriedOf(domain.ReturnedOf(&permit, false), carried))
	}
	work.mu.Lock()
	work.live[run.JobID] = &held{
		kind: domain.KindTurn, source: domain.SourceAccount, permit: permit, link: row.Link, number: row.Number, chatID: run.ChatID,
		begun: true, carried: carried,
	}
	work.mu.Unlock()
	work.refresh()
	return "", nil
}

// carriedOf is what a turn a run before took tells of its question (AI2.9): the end of that run may have dropped a request of
// it, and a late answer may still take a returned unit back, so a yes is pending until the site's word is asked.
func carriedOf(returned domain.Returned, carried bool) domain.Returned {
	if carried && returned == domain.ReturnedYes {
		return domain.ReturnedPending
	}
	return returned
}

// keepBegun keeps whether the question of a turn that ended before the model came back, and forgets the permit of its
// start: the failure of the turn closes nothing more.
func (work *Work) keepBegun(ctx context.Context, jobID string, returned domain.Returned) error {
	work.mu.Lock()
	delete(work.live, jobID)
	work.mu.Unlock()
	work.keep(ctx, jobID, returned)
	return nil
}

// beginFailure is the code a turn ends with when its permit could not be taken.
func beginFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, accountdomain.ErrPassInvalid), errors.Is(err, accountdomain.ErrNotHeld), errors.Is(err, accountdomain.ErrPassReplaced):
		return researchdomain.CodeAccountUnlinked
	}
	if refusal, ok := domain.AsRefusal(err); ok {
		switch refusal.Code {
		case researchdomain.CodeQuestionsOver, researchdomain.CodeTurnsAtOnce:
			return refusal.Code
		}
		return codeOfReason(domain.AnswerOf(*refusal).Reason)
	}
	return researchdomain.CodeSiteUnreachable
}

// codeOfReason is the code of a reason of the table of the gateway.
func codeOfReason(reason string) string {
	switch reason {
	case domain.ReasonDayFence:
		return researchdomain.CodeDayFence
	case domain.ReasonUnlinked:
		return researchdomain.CodeAccountUnlinked
	case domain.ReasonPermitEnded:
		return researchdomain.CodePermitEnded
	case domain.ReasonUnavailable:
		return researchdomain.CodeSiteUnavailable
	case domain.ReasonUnfit:
		return researchdomain.CodeAppUnfit
	case domain.ReasonTooLarge:
		return researchdomain.CodeThreadTooLarge
	case domain.ReasonFailed:
		return researchdomain.CodeTurnFailed
	case domain.ReasonUnreachable:
		return researchdomain.CodeSiteUnreachable
	case domain.ReasonJournal:
		return researchdomain.CodeJournalUnavailable
	default:
		return ""
	}
}

// Failure is the code of the reason the site last refused a request of the Job with; a request it took after clears it
// (AI2.5).
func (work *Work) Failure(jobID string) string {
	work.mu.Lock()
	defer work.mu.Unlock()
	return codeOfReason(work.reasons[jobID])
}
