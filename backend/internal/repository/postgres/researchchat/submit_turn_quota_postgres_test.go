package researchchat

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type quotaChats struct {
	t          *testing.T
	db         *storedb.DB
	repository *Repository
	proof      auth.SessionWriteProof
	now        time.Time
	next       int
}

func newQuotaChats(t *testing.T, name string) *quotaChats {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, name)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	return &quotaChats{t: t, db: db, repository: New(db), proof: seedSubmitTurnCanonicalBaseline(t, db, now), now: now}
}

func (chats *quotaChats) setLimits(values map[quota.Key]int64) {
	chats.t.Helper()
	changes := make([]quota.OverrideChange, 0, len(values))
	for key, value := range values {
		changes = append(changes, quota.OverrideChange{Key: key, Value: &value})
	}
	if err := chats.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(context.Background(), tx, submitTurnActorID, changes, chats.now)
		return err
	}); err != nil {
		chats.t.Fatal(err)
	}
}

func (chats *quotaChats) chat() researchdomain.Session {
	chats.t.Helper()
	chats.next++
	session, err := chats.repository.CreateSession(context.Background(), service.CreateSessionCommand{
		ID: fmt.Sprintf("92000000-0000-4000-8000-%012d", chats.next), ProjectID: submitTurnProjectID,
		ActorUserID: submitTurnActorID, SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject,
		Title: "Quota", SessionWriteProof: chats.proof, ScopeHash: "quota-scope",
		OperationID: "research_chat.session.create.v1", IdempotencyKeyHash: fmt.Sprintf("quota-chat-%d", chats.next),
		RequestHash: fmt.Sprintf("quota-chat-request-%d", chats.next), StatusCode: 201, CreatedAt: chats.now,
	})
	if err != nil {
		chats.t.Fatalf("CreateSession() error = %v", err)
	}
	return session
}

func (chats *quotaChats) ask(session researchdomain.Session, key string, at time.Time) (jobs.Job, error) {
	return chats.repository.SubmitTurn(context.Background(), service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Кто такой Мирон?",
		SessionWriteProof: chats.proof, ScopeHash: "turn-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: key, RequestHash: key + "-request", CreatedAt: at,
	})
}

func (chats *quotaChats) count(query string, args ...any) int64 {
	chats.t.Helper()
	var value int64
	if err := chats.db.Pool.QueryRow(context.Background(), query, args...).Scan(&value); err != nil {
		chats.t.Fatal(err)
	}
	return value
}

func wantQuota(t *testing.T, err error, key quota.Key) *quota.Exceeded {
	t.Helper()
	exceeded, ok := quota.AsExceeded(err)
	if !ok || exceeded.Key != key {
		t.Fatalf("error = %v, want quota %s", err, key)
	}
	return exceeded
}

// R2.1, R1.3: the third question of a day over a limit of 2 is refused without a turn; a replay
// of an accepted question returns its turn and does not count.
func TestSubmitTurnRefusesQuestionsOverDailyQuota(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_quota_daily")
	chats.setLimits(map[quota.Key]int64{quota.KeyChatTurnsPerDay: 2, quota.KeyActiveTurns: 5})
	first, err := chats.ask(chats.chat(), "daily-1", chats.now)
	if err != nil {
		t.Fatalf("first question: %v", err)
	}
	secondChat := chats.chat()
	second, err := chats.ask(secondChat, "daily-2", chats.now)
	if err != nil || second.ID == first.ID {
		t.Fatalf("second question: %v", err)
	}
	thirdChat := chats.chat()
	exceeded := wantQuota(t, func() error { _, err := chats.ask(thirdChat, "daily-3", chats.now); return err }(), quota.KeyChatTurnsPerDay)
	if exceeded.Limit != 2 || exceeded.ResetsAt == nil || !exceeded.ResetsAt.Equal(time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("refusal = %+v", exceeded)
	}
	if turns := chats.count(`SELECT count(*) FROM chat_turns WHERE chat_id=$1::uuid`, thirdChat.ID); turns != 0 {
		t.Fatalf("refused question left %d turns", turns)
	}
	if used := chats.count(`SELECT chat_turns FROM writer_usage_days WHERE user_id=$1::uuid`, submitTurnActorID); used != 2 {
		t.Fatalf("day counter = %d, want 2", used)
	}
	replayed, err := chats.ask(secondChat, "daily-2", chats.now)
	if err != nil || replayed.ID != second.ID {
		t.Fatalf("replay = %s, %v; want %s", replayed.ID, err, second.ID)
	}
	if used := chats.count(`SELECT chat_turns FROM writer_usage_days WHERE user_id=$1::uuid`, submitTurnActorID); used != 2 {
		t.Fatalf("day counter after replay = %d, want 2", used)
	}
}

// R2.2, R2.5: two questions in work refuse a third in any chat; once one is done, the next is
// accepted.
func TestSubmitTurnRefusesQuestionOverActiveQuota(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_quota_active")
	first, err := chats.ask(chats.chat(), "active-1", chats.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chats.ask(chats.chat(), "active-2", chats.now); err != nil {
		t.Fatal(err)
	}
	waiting := chats.chat()
	exceeded := wantQuota(t, func() error { _, err := chats.ask(waiting, "active-3", chats.now); return err }(), quota.KeyActiveTurns)
	if exceeded.Limit != 2 || exceeded.ResetsAt != nil {
		t.Fatalf("refusal = %+v", exceeded)
	}
	if used := chats.count(`SELECT chat_turns FROM writer_usage_days WHERE user_id=$1::uuid`, submitTurnActorID); used != 2 {
		t.Fatalf("refused question changed the day counter: %d", used)
	}
	if _, err := chats.db.Pool.Exec(context.Background(), `
		UPDATE jobs SET status='canceled', can_cancel=false, completed_at=$2, updated_at=$2
		WHERE id=$1::uuid
	`, first.ID, chats.now); err != nil {
		t.Fatal(err)
	}
	if _, err := chats.ask(waiting, "active-3", chats.now); err != nil {
		t.Fatalf("question after one finished: %v", err)
	}
}

// R2.3: two submissions of one writer at once with one free place — exactly one is accepted.
func TestSubmitTurnQuotaHoldsUnderConcurrentSubmissions(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_quota_race")
	chats.setLimits(map[quota.Key]int64{quota.KeyActiveTurns: 1})
	sessions := []researchdomain.Session{chats.chat(), chats.chat()}
	errs := make([]error, len(sessions))
	var wait sync.WaitGroup
	for index, session := range sessions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, errs[index] = chats.ask(session, fmt.Sprintf("race-%d", index), chats.now)
		}()
	}
	wait.Wait()
	accepted, refused := 0, 0
	for _, err := range errs {
		if err == nil {
			accepted++
		} else if exceeded, ok := quota.AsExceeded(err); ok && exceeded.Key == quota.KeyActiveTurns {
			refused++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("accepted %d, refused %d; want 1 and 1", accepted, refused)
	}
}

// R1.2: questions at 23:59 and 00:01 Moscow time count in different days.
func TestSubmitTurnCountsMoscowDays(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_quota_day")
	beforeMidnight := time.Date(2026, 10, 28, 20, 59, 0, 0, time.UTC)
	afterMidnight := time.Date(2026, 10, 28, 21, 1, 0, 0, time.UTC)
	if _, err := chats.ask(chats.chat(), "day-1", beforeMidnight); err != nil {
		t.Fatal(err)
	}
	if _, err := chats.ask(chats.chat(), "day-2", afterMidnight); err != nil {
		t.Fatal(err)
	}
	if days := chats.count(`
		SELECT count(*) FROM writer_usage_days
		WHERE user_id=$1::uuid AND chat_turns=1 AND day IN ('2026-10-28', '2026-10-29')
	`, submitTurnActorID); days != 2 {
		t.Fatalf("day rows = %d, want 2", days)
	}
}
