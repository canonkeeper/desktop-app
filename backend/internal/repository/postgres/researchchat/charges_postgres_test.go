package researchchat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// countedElsewhere is a counter of a site as the fence of a test sees it: what it was asked to open, charge and return,
// what the transaction held when it was asked, and what it answers.
type countedElsewhere struct {
	mu        sync.Mutex
	opened    []quota.Work
	steered   []quota.Steer
	unsteered []quota.Steer
	// seen is whether the Job and its turn were written and the key of the repeat of the start closed with the Job in the
	// transaction when the counter was asked: the open is its last step.
	seen   []bool
	refuse error
}

func (counter *countedElsewhere) OpenInTx(ctx context.Context, tx pgx.Tx, work quota.Work) error {
	var rows int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM jobs WHERE id = $1::uuid) + (SELECT count(*) FROM chat_turns WHERE job_id = $1::uuid)
		     + (SELECT count(*) FROM idempotency_records WHERE response_json->>'jobId' = $1::text)
	`, work.JobID).Scan(&rows); err != nil {
		return err
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.opened = append(counter.opened, work)
	counter.seen = append(counter.seen, rows == 3)
	return counter.refuse
}

func (counter *countedElsewhere) SteerInTx(_ context.Context, _ pgx.Tx, steer quota.Steer) error {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.steered = append(counter.steered, steer)
	return counter.refuse
}

func (counter *countedElsewhere) UnsteerInTx(_ context.Context, _ pgx.Tx, steer quota.Steer) error {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.unsteered = append(counter.unsteered, steer)
	return nil
}

// specs/2026-11-13-desk-account-ai.md AI2.1: the counter of the site opens a turn as the last step of the transaction of
// its start — the Job and its turn are written when it is asked —; a refusal of a local check before it asks nothing; a
// refusal of the site or a failure after the open leaves nothing on the computer — no Job, no turn, no unit of the day —
// and nothing to close: the lease of the site gives the unit back.
func TestTheCounterOfTheSiteOpensATurnLastPostgres(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_app_charges")
	counter := &countedElsewhere{}
	chats.repository = New(chats.db).WithQuota(quotastore.Fence{Charges: counter})
	chats.setLimits(map[quota.Key]int64{quota.KeyChatTurnsPerDay: 2, quota.KeyActiveTurns: 5})
	session := chats.chat()

	job, err := chats.ask(session, "charged-1", chats.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.opened) != 1 || counter.opened[0].JobID != job.ID || counter.opened[0].Kind != quota.WorkTurn ||
		counter.opened[0].ChatID != session.ID || counter.opened[0].UserID != submitTurnActorID || !counter.seen[0] {
		t.Fatalf("the counter was asked %+v, seeing the turn %v", counter.opened, counter.seen)
	}
	// A repeat of the same question reaches no transaction of a start: nothing is opened again.
	if again, err := chats.ask(session, "charged-1", chats.now); err != nil || again.ID != job.ID || len(counter.opened) != 1 {
		t.Fatalf("a repeat = %v, %d opens", err, len(counter.opened))
	}

	counter.refuse = quota.DailyExceeded(quota.KeyChatTurnsPerDay, 0, chats.now)
	_, err = chats.ask(chats.chat(), "refused-by-the-site", chats.now.Add(time.Second))
	if exceeded := wantQuota(t, err, quota.KeyChatTurnsPerDay); exceeded.ResetsAt == nil {
		t.Fatalf("the refusal of the site = %+v", exceeded)
	}
	if len(counter.opened) != 2 || !counter.seen[1] {
		t.Fatalf("the counter was not asked last: %+v %v", counter.opened, counter.seen)
	}
	if turns := chats.count(`SELECT count(*) FROM jobs WHERE kind = 'research_chat_turn'`); turns != 1 || spent(t, chats.db, "chat_turns") != 1 {
		t.Fatalf("a refused open left %d turns and %d units", turns, spent(t, chats.db, "chat_turns"))
	}
	counter.refuse = &quota.CounterUnreachable{MayReturn: true}
	if _, err := chats.ask(chats.chat(), "unreachable", chats.now.Add(2*time.Second)); err == nil {
		t.Fatal("a counter that did not answer started the turn")
	} else if unreachable, ok := quota.AsCounterUnreachable(err); !ok || !unreachable.MayReturn {
		t.Fatalf("the counter that did not answer = %v", err)
	}
	counter.refuse = nil

	// A local check refuses before the counter: the day of the local limit is over.
	chats.askOrFail(chats.chat(), "charged-2", chats.now.Add(3*time.Second))
	opened := len(counter.opened)
	_, err = chats.ask(chats.chat(), "local-refusal", chats.now.Add(4*time.Second))
	wantQuota(t, err, quota.KeyChatTurnsPerDay)
	if len(counter.opened) != opened {
		t.Fatalf("a local refusal asked the counter: %d opens", len(counter.opened)-opened)
	}
}

// AI2.8: a correction is charged by the counter of the site under the Job of its turn by the key of its repeat, as the last
// step; one the turn did not take is returned, one it took is not.
func TestTheCounterOfTheSiteChargesACorrectionPostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_app_steer_charges")
	counter := &countedElsewhere{}
	fixture.repository = fixture.repository.WithQuota(quotastore.Fence{Charges: counter})
	fixture.run(t)
	ctx := context.Background()
	refused, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.steered) != 1 || counter.steered[0].JobID != fixture.job.ID || counter.steered[0].Key != refused.RepeatKey || refused.RepeatKey == "" {
		t.Fatalf("the correction asked %+v", counter.steered)
	}
	if err := fixture.repository.FinishSteer(ctx, refused, false); err != nil {
		t.Fatal(err)
	}
	if len(counter.unsteered) != 1 || counter.unsteered[0] != (quota.Steer{JobID: fixture.job.ID, Key: refused.RepeatKey, At: refused.Charge.At}) {
		t.Fatalf("the return = %+v", counter.unsteered)
	}
	accepted, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.FinishSteer(ctx, accepted, true); err != nil {
		t.Fatal(err)
	}
	if len(counter.steered) != 2 || counter.steered[1].Key != refused.RepeatKey || len(counter.unsteered) != 1 {
		t.Fatalf("a repeat of the key = %+v, returns %+v", counter.steered, counter.unsteered)
	}
	// A refusal of the counter takes the correction back whole: no key, no unit.
	counter.refuse = errors.New("the site refused")
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-2")); err == nil {
		t.Fatal("a refused correction went")
	}
	if spent(t, fixture.db, "chat_turns") != 2 {
		t.Fatalf("units after a refused correction = %d", spent(t, fixture.db, "chat_turns"))
	}
	// AI2.2: the site may have charged a correction whose transaction did not commit; the repeat of the same sending names it
	// by the same key of its repeat — one unit at the site —, though the row of the key was written anew.
	counter.refuse = nil
	again, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-2"))
	if err != nil {
		t.Fatal(err)
	}
	last := len(counter.steered)
	if last < 2 || counter.steered[last-1].Key != counter.steered[last-2].Key || counter.steered[last-1].Key == counter.steered[0].Key ||
		again.RepeatKey != counter.steered[last-1].Key {
		t.Fatalf("the repeat of a correction the commit lost = %+v", counter.steered)
	}
}
