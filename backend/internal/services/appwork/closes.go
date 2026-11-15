// SPDX-License-Identifier: AGPL-3.0-only

package appwork

import (
	"context"
	"errors"
	"sync"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/appwork"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// The closes of the permits (AI2.7, AI2.9): the end of a turn, the end of a reading or an upload, the closes that did not
// reach the site and the stop of the app.

// stopKey marks the context the stop of the app cancels its turns under.
type stopKey struct{}

// Stopping is the context the stop of the app cancels its turns under (AI2.7, DT3.8): the end of a turn canceled under it is
// the stop of the app, not the author's stop, and asks the site no longer than the term of the stop.
func (work *Work) Stopping(ctx context.Context) context.Context {
	return context.WithValue(ctx, stopKey{}, true)
}

// End closes the permit of a turn once its terminal transaction is written (AI2.7, AI2.9): its own call in its own term;
// what the site answered says whether the unit came back — pending when a request of the turn was dropped mid-stream or the
// close did not reach the site, which goes again before the next open; an unlinking or a broken link is no return. A turn
// the stop of the app canceled ends as stopped.
func (work *Work) End(ctx context.Context, jobID string, end researchdomain.TurnEnd) {
	if ctx.Value(stopKey{}) != nil {
		work.stopped(ctx, jobID)
		return
	}
	outcome := domain.OutcomeFailed
	switch end {
	case researchdomain.TurnAnswered:
		outcome = domain.OutcomeAnswered
	case researchdomain.TurnCanceled:
		outcome = domain.OutcomeCanceled
	}
	work.close(context.WithoutCancel(ctx), jobID, outcome, true)
}

// close closes the permit of a Job the process holds, and forgets it. A turn of the account the process does not hold — taken
// by a run before a restart — asks its permit by the ref of its row under the same link, which spends nothing, and closes
// it. A turn a run before took may have had a request dropped by the end of that run, so its return is pending until the
// site's word is asked (AI2.9). A permit of another link is not the site's to close any more: the unlinking closed it
// canceled, without a return. Any answer of the site ends the close; only no answer goes again before the next open.
func (work *Work) close(ctx context.Context, jobID string, outcome domain.Outcome, turn bool) {
	work.mu.Lock()
	known := work.live[jobID]
	delete(work.live, jobID)
	dropped := work.dropped[jobID]
	delete(work.dropped, jobID)
	delete(work.reasons, jobID)
	work.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, CloseTerm)
	defer cancel()
	if known == nil && turn {
		var told bool
		if known, told = work.heldOfRow(ctx, jobID); told {
			return
		}
	}
	if known == nil || known.source != domain.SourceAccount || known.permit.ID == "" {
		return
	}
	returned := domain.ReturnedNo
	if known.link == work.Mark() {
		closed, err := work.Site.ClosePermit(ctx, known.permit.ID, outcome)
		work.refresh()
		returned = carriedOf(work.returnedOf(closed, err, dropped), known.carried)
		if !answered(err) {
			work.mu.Lock()
			work.closing[jobID] = closing{permitID: known.permit.ID, outcome: outcome, kind: known.kind, link: known.link, dropped: dropped}
			work.mu.Unlock()
			work.log(`{"event":"desktop_permit_close_not_sent"}`)
		}
	}
	if turn && known.kind == domain.KindTurn {
		work.keep(ctx, jobID, returned)
	}
}

// heldOfRow is the permit of a turn of the account whose row says nothing yet of its question, asked by the ref of its row
// under the link of its start: the site knows that ref and answers its permit without a unit. Nil — nothing to close; told —
// the row already says whether the question came back, or now says no: the turn started under another link.
func (work *Work) heldOfRow(ctx context.Context, jobID string) (*held, bool) {
	row, ok, err := work.Rows.Work(ctx, jobID)
	if err != nil || !ok || row.Source != domain.SourceAccount || row.Kind != domain.KindTurn || row.Returned != "" {
		return nil, true
	}
	if row.Link != work.Mark() {
		work.keep(ctx, jobID, domain.ReturnedNo)
		return nil, true
	}
	permit, err := work.open(ctx, domain.KindTurn, domain.Ref(work.Salt, jobID, row.Number))
	if err != nil || permit.ID == "" {
		return nil, true
	}
	return &held{kind: domain.KindTurn, source: domain.SourceAccount, permit: permit, link: row.Link, number: row.Number, carried: true}, false
}

// returnedOf is what a close answered of the unit of a turn (AI2.9): the site's answer, with a dropped request pending; an
// unlinking or a broken link is no return; a refusal of the site is no return it could tell; no answer is pending.
func (work *Work) returnedOf(closed domain.Permit, err error, dropped bool) domain.Returned {
	switch {
	case err == nil:
		return domain.ReturnedOf(&closed, dropped)
	case errors.Is(err, accountdomain.ErrPassInvalid), errors.Is(err, accountdomain.ErrNotHeld), errors.Is(err, accountdomain.ErrPassReplaced):
		// An unlinking closes the permits of the device as canceled, without a return.
		return domain.ReturnedNo
	default:
		if _, refused := domain.AsRefusal(err); refused {
			return domain.ReturnedNo
		}
		return domain.ReturnedPending
	}
}

// answered says the site answered a call — with the permit or a refusal: a close it answered does not go again.
func answered(err error) bool {
	if err == nil {
		return true
	}
	if _, refused := domain.AsRefusal(err); refused {
		return true
	}
	return errors.Is(err, accountdomain.ErrPassInvalid) || errors.Is(err, accountdomain.ErrNotHeld) || errors.Is(err, accountdomain.ErrPassReplaced)
}

// flush sends again the closes that did not reach the site, all at once within one term: they are part of the start of
// the next turn. A close of another link or one the site answered goes no more.
func (work *Work) flush(ctx context.Context) {
	work.mu.Lock()
	pending := make(map[string]closing, len(work.closing))
	for jobID, close := range work.closing {
		pending[jobID] = close
	}
	work.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, CloseTerm)
	defer cancel()
	mark := work.Mark()
	var group sync.WaitGroup
	for jobID, close := range pending {
		group.Add(1)
		go func() {
			defer group.Done()
			returned := domain.ReturnedNo
			if close.link == mark {
				closed, err := work.Site.ClosePermit(ctx, close.permitID, close.outcome)
				if !answered(err) {
					return
				}
				returned = work.returnedOf(closed, err, close.dropped)
			}
			work.mu.Lock()
			delete(work.closing, jobID)
			work.mu.Unlock()
			if close.kind == domain.KindTurn {
				work.keep(ctx, jobID, returned)
			}
		}()
	}
	group.Wait()
}

// StopRunning closes the permits of the turns that run as failed before the stop of the app cancels them (AI2.7), all at once
// within the term of ctx: whether the question comes back the site decides by what it saw. A turn no worker took yet is left
// to the cancel under Stopping.
func (work *Work) StopRunning(ctx context.Context) {
	work.mu.Lock()
	var turns []string
	for jobID, known := range work.live {
		if known.kind == domain.KindTurn && known.source == domain.SourceAccount && known.permit.ID != "" && known.begun {
			turns = append(turns, jobID)
		}
	}
	work.mu.Unlock()
	var group sync.WaitGroup
	for _, jobID := range turns {
		group.Add(1)
		go func() {
			defer group.Done()
			work.stopClose(ctx, jobID)
		}()
	}
	group.Wait()
}

// stopped ends a turn the stop of the app canceled (AI2.7, DT3.8). One the stop closed already asks nothing more. One no worker
// took asked no model: nothing goes to the site — the lease of the site gives its unit back, outside the ceiling of the returns
// of the app —, and its question is pending until the site's word is asked; so is the question of a turn a run before took
// that no worker took again. One that runs is closed as failed within the term of the stop.
func (work *Work) stopped(ctx context.Context, jobID string) {
	work.mu.Lock()
	known := work.live[jobID]
	if known != nil && !known.begun {
		delete(work.live, jobID)
	}
	work.mu.Unlock()
	switch {
	case known == nil:
		if row, ok, err := work.Rows.Work(ctx, jobID); err == nil && ok && row.Source == domain.SourceAccount && row.Kind == domain.KindTurn && row.Returned == "" {
			work.keep(ctx, jobID, domain.ReturnedPending)
		}
	case known.source != domain.SourceAccount:
	case known.begun:
		work.stopClose(ctx, jobID)
	default:
		work.keep(ctx, jobID, domain.ReturnedPending)
	}
}

// stopClose closes the permit of a turn the stop of the app stops as failed: the cancel after the close drops the streams of
// the turn, and a late answer may take the unit back, so its question is pending until the site's word is asked.
func (work *Work) stopClose(ctx context.Context, jobID string) {
	work.mu.Lock()
	work.dropped[jobID] = true
	work.mu.Unlock()
	work.close(ctx, jobID, domain.OutcomeFailed, true)
}

// keep writes whether the question of a turn came back; a pending one is settled once the word of the site is final.
func (work *Work) keep(ctx context.Context, jobID string, returned domain.Returned) {
	if err := work.Rows.KeepReturned(context.WithoutCancel(ctx), jobID, returned); err != nil {
		work.log(`{"event":"desktop_permit_returned_not_kept"}`)
	}
	work.mu.Lock()
	defer work.mu.Unlock()
	if returned == domain.ReturnedPending {
		work.later[jobID] = work.Now().Add(SettleAfter)
	} else {
		delete(work.later, jobID)
	}
}

// Recall takes the questions a run before left pending (AI2.9): the word of the site on them is asked once the lease of a
// permit and the minute of a dropped stream passed since the start.
func (work *Work) Recall(ctx context.Context) {
	pending, err := work.Rows.Pending(ctx)
	if err != nil {
		work.log(`{"event":"desktop_pending_not_recalled"}`)
		return
	}
	at := work.Now().Add(SettleAfter)
	work.mu.Lock()
	defer work.mu.Unlock()
	for _, jobID := range pending {
		if _, known := work.later[jobID]; !known {
			work.later[jobID] = at
		}
	}
}

// Settle asks the word of the site, all at once within one term, on the questions left pending whose time came, once each in
// this run (AI2.9): by the ref of the row of the same link — the site answers the permit of a ref it knows without a unit,
// and closes an ended one no more —, so pending does not stay. A turn a worker holds again tells at its end; one of another
// link is not the site's to ask. Nothing is asked while none is due.
func (work *Work) Settle(ctx context.Context) {
	now := work.Now()
	work.mu.Lock()
	var due []string
	for jobID, at := range work.later {
		if _, held := work.live[jobID]; !held && !now.Before(at) {
			due = append(due, jobID)
			delete(work.later, jobID)
		}
	}
	work.mu.Unlock()
	if len(due) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, CloseTerm)
	defer cancel()
	var group sync.WaitGroup
	for _, jobID := range due {
		group.Add(1)
		go func() {
			defer group.Done()
			row, ok, err := work.Rows.Work(ctx, jobID)
			if err != nil || !ok || row.Returned != domain.ReturnedPending || row.Link != work.Mark() {
				return
			}
			permit, err := work.Site.OpenPermit(ctx, domain.KindTurn, domain.Ref(work.Salt, jobID, row.Number))
			if err == nil && permit.Live() {
				permit, err = work.Site.ClosePermit(ctx, permit.ID, domain.OutcomeFailed)
			}
			if err == nil {
				work.keep(ctx, jobID, domain.ReturnedOf(&permit, false))
			}
		}()
	}
	group.Wait()
}
