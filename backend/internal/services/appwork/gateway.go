// SPDX-License-Identifier: AGPL-3.0-only

package appwork

import (
	"context"
	"errors"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
)

// Ticket is what the gateway knows of the work of a Job (AI1.4, AI2.3): the source it took at its start and the permit the
// process holds for it; the permit lives only in this memory.
type Ticket struct {
	Source   domain.Source
	Kind     domain.Kind
	PermitID string
	ChatID   string
}

// TicketOf is the work of a Job the process holds; false — none: the gateway goes by the settings, and refuses a request of
// the account without one (AI1.4, AI2.3).
func (work *Work) TicketOf(jobID string) (Ticket, bool) {
	work.mu.Lock()
	defer work.mu.Unlock()
	known := work.live[jobID]
	if known == nil {
		return Ticket{}, false
	}
	return Ticket{Source: known.source, Kind: known.kind, PermitID: known.permit.ID, ChatID: known.chatID}, true
}

// WorkOf is the work of a Job the gateway asks about (AI1.4, AI2.3): the one the process holds, else — an upload of
// materials after a restart — the row its start kept, its permit found again by the gateway. A Job without a row took no
// source the app keeps.
func (work *Work) WorkOf(ctx context.Context, jobID string) (Ticket, bool) {
	if ticket, ok := work.TicketOf(jobID); ok {
		return ticket, true
	}
	row, ok, err := work.Rows.Work(ctx, jobID)
	if err != nil || !ok {
		return Ticket{}, false
	}
	return Ticket{Source: row.Source, Kind: row.Kind}, true
}

// Materials is the permit of the upload of materials of a Job of the account, found again by the ref of its start (AI2.3,
// AI3.8): the site answers the same permit for the same ref without a unit. It never opens another one — an upload whose
// row is of another source, or whose permit ended, has none ("").
func (work *Work) Materials(ctx context.Context, jobID string) (string, error) {
	row, ok, err := work.Rows.Work(ctx, jobID)
	if err != nil {
		return "", err
	}
	// An upload started under another link has no permit the site would find: asking would open and pay a new one.
	if !ok || row.Source != domain.SourceAccount || row.Kind != domain.KindMaterials || row.Link != work.Mark() {
		return "", nil
	}
	permit, err := work.open(ctx, domain.KindMaterials, domain.Ref(work.Salt, jobID, row.Number))
	if err != nil {
		return "", err
	}
	if !permit.Live() {
		return "", nil
	}
	work.mu.Lock()
	work.live[jobID] = &held{kind: domain.KindMaterials, source: domain.SourceAccount, permit: permit, link: row.Link, number: row.Number}
	work.mu.Unlock()
	return permit.ID, nil
}

// kindOf is the work a request of the purpose is part of: the chat and its scan are a turn, the materials an upload, any
// step of the reading the reading.
func kindOf(purpose string) domain.Kind {
	switch purpose {
	case "chat", "scan", string(domain.KindTurn):
		return domain.KindTurn
	case string(domain.KindMaterials):
		return domain.KindMaterials
	default:
		return domain.KindReading
	}
}

// Refused hears the refusal of the site the gateway answered a request of the Job with: it is the reason of the Job until a
// request the site takes (AI2.5), and the refusals that repeat are remembered for the work of its kind (AI2.6).
func (work *Work) Refused(jobID, purpose string, answer domain.Answer) {
	work.mu.Lock()
	known := work.live[jobID]
	if answer.Reason != "" {
		work.reasons[jobID] = answer.Reason
	}
	chatID := ""
	if known != nil {
		chatID = known.chatID
	}
	work.mu.Unlock()
	if answer.Remember != domain.RememberNothing {
		work.remember(answer.Remember, string(kindOf(purpose)), chatID)
	}
}

// Accepted hears a request of the Job the site took: no reason stays.
func (work *Work) Accepted(jobID string) {
	work.mu.Lock()
	defer work.mu.Unlock()
	delete(work.reasons, jobID)
}

// Dropped hears a request of a turn dropped mid-stream: the site reads the rest without the app and a late answer may take a
// returned unit back, so the return of the turn is pending (AI2.9).
func (work *Work) Dropped(jobID string) {
	work.mu.Lock()
	defer work.mu.Unlock()
	work.dropped[jobID] = true
}

// RefusedNow is the code of a refusal the app remembers for work of the purpose of the chat (AI2.6): the gateway gives it at
// once for a step of a kind the site does not take from this version.
func (work *Work) RefusedNow(purpose, chatID string) string {
	return work.remembered(string(kindOf(purpose)), chatID)
}

// Reading is the permit of the reading of a Job (AI3.2): the one the process holds under the link of now, or the first live
// one by the numbers of the Job from 0 — no unit is spent, an ended one is passed by; open — the next number after the one
// the site ended.
func (work *Work) Reading(ctx context.Context, jobID string, renew bool) (string, error) {
	mark := work.Mark()
	work.mu.Lock()
	known := work.live[jobID]
	work.mu.Unlock()
	number := 0
	if known != nil && known.link == mark {
		if known.permit.Live() && !renew {
			return known.permit.ID, nil
		}
		number = known.number + 1
	}
	for ; number < maxNumbers; number++ {
		permit, err := work.open(ctx, domain.KindReading, domain.Ref(work.Salt, jobID, number))
		if err != nil {
			return "", err
		}
		if permit.Live() {
			work.mu.Lock()
			work.live[jobID] = &held{kind: domain.KindReading, source: domain.SourceAccount, permit: permit, link: mark, number: number}
			work.mu.Unlock()
			return permit.ID, nil
		}
	}
	return "", errors.New("appwork: no permit of the reading could be opened")
}

// Done closes the permit of the reading or of the upload of materials of a Job that ended (AI3.2, AI3.8): done, canceled or
// failed. A Job that asked no model holds none and sends nothing.
func (work *Work) Done(ctx context.Context, jobID string, outcome domain.Outcome) {
	work.close(context.WithoutCancel(ctx), jobID, outcome, false)
}

// Steer charges the unit of a correction of a turn of the account under its permit, by the key of its repeat (AI2.8): a
// turn of another source or one the process does not hold asks nothing.
func (work *Work) Steer(ctx context.Context, steer quota.Steer) error {
	work.mu.Lock()
	known := work.live[steer.JobID]
	work.mu.Unlock()
	if known == nil || known.source != domain.SourceAccount || known.permit.ID == "" {
		return nil
	}
	ref := domain.SteerRef(work.Salt, steer.Key)
	err := askTwice(ctx, func(ctx context.Context) error { return work.Site.ChargeUnit(ctx, known.permit.ID, ref) })
	if err != nil {
		return work.refusalOf(err, string(domain.KindTurn), known.chatID)
	}
	now := work.Now()
	work.mu.Lock()
	for record, kept := range work.charged {
		if now.Sub(kept.at) > chargeKept {
			delete(work.charged, record)
		}
	}
	work.charged[steer.Key] = charge{permitID: known.permit.ID, link: known.link, at: now}
	work.mu.Unlock()
	work.refresh()
	return nil
}

// Unsteer gives back the unit of a correction the turn did not take (AI2.8), under the permit it was charged under — the
// turn may have ended meanwhile, and the site returns it as well —, asked once more when the site did not answer. A
// correction this process did not charge, or one of another link, has nothing to give back.
func (work *Work) Unsteer(ctx context.Context, steer quota.Steer) error {
	work.mu.Lock()
	kept, ok := work.charged[steer.Key]
	delete(work.charged, steer.Key)
	work.mu.Unlock()
	if !ok || kept.link != work.Mark() {
		return nil
	}
	ref := domain.SteerRef(work.Salt, steer.Key)
	err := askTwice(ctx, func(ctx context.Context) error {
		_, err := work.Site.ReturnUnit(ctx, kept.permitID, ref)
		return err
	})
	if err != nil {
		work.log(`{"event":"desktop_permit_unit_not_returned"}`)
	}
	work.refresh()
	return nil
}

// Returned says whether the unit of the question of a turn came back (AI2.9): none for a turn no site counted — the site's,
// «Свой ИИ», no helper —, what the close of its permit said, pending until it said it.
func (work *Work) Returned(ctx context.Context, jobID string) domain.Returned {
	row, ok, err := work.Rows.Work(ctx, jobID)
	switch {
	case err != nil, !ok, row.Source != domain.SourceAccount:
		return domain.ReturnedNone
	case row.Returned != "":
		return row.Returned
	default:
		return domain.ReturnedPending
	}
}

// askTwice asks the site once more when a call got no answer — a broken connection or its term —, each call in a term of its
// own: the same ref answers the same, so a repeat spends nothing more (AI2.2).
func askTwice(ctx context.Context, call func(context.Context) error) error {
	ask := func() error {
		ctx, cancel := context.WithTimeout(ctx, OpenTerm)
		defer cancel()
		err := call(ctx)
		if err != nil && ctx.Err() != nil {
			return accountdomain.ErrUnreachable
		}
		return err
	}
	err := ask()
	if errors.Is(err, accountdomain.ErrUnreachable) {
		err = ask()
	}
	return err
}
