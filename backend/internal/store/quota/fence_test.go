package quotastore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// planStub is a build's plan: the same settings for every writer, and the queriers it was read in.
type planStub struct {
	settings quota.Settings
	seen     []Querier
}

func (plan *planStub) PlanSettingsInTx(_ context.Context, db Querier, _ string) (quota.Settings, error) {
	plan.seen = append(plan.seen, db)
	return plan.settings, nil
}

// specs/2026-11-08-subscription-tiers.md TR2.3, TR2.4: a fence without a plan keeps the limits as they were — own,
// platform, code; with a plan its keys stand between the writer's own value and the platform's, the checks read it in
// their own transaction and the reads of the store see the same limits and sources.
func TestFenceTakesThePlanBetweenOwnAndPlatformPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_fence_plan")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "own-and-plan@example.test")
	seed.user(quotaWriterB, "plan-only@example.test")
	seed.user(quotaAdmin, "admin@example.test")
	ctx := context.Background()
	plain := New(db, Fence{})
	if _, err := plain.SetOverrides(ctx, quotaWriterA, []quota.OverrideChange{{Key: quota.KeyProjects, Value: limit(9)}}, seed.now); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.SetPlatformDefaults(ctx, quotaAdmin, []quota.OverrideChange{
		{Key: quota.KeyChatTurnsPerDay, Value: limit(40)}, {Key: quota.KeyAccountBooks, Value: limit(50)}, {Key: quota.KeyActiveTurns, Value: limit(4)},
	}, seed.now); err != nil {
		t.Fatal(err)
	}
	plan := &planStub{settings: quota.Settings{quota.KeyChatTurnsPerDay: 2, quota.KeyAccountBooks: 12, quota.KeyProjects: 3}}
	fence := Fence{Plans: plan}

	before := quota.Defaults()
	before.ChatTurnsPerDay, before.AccountBooks, before.ActiveTurns, before.Projects = 40, 50, 4, 9
	if limits, err := (Fence{}).EffectiveLimitsInTx(ctx, db.Pool, quotaWriterA); err != nil || limits != before {
		t.Fatalf("limits without a plan = %+v, %v; want %+v", limits, err, before)
	}
	planned := before
	planned.ChatTurnsPerDay, planned.AccountBooks = 2, 12
	if limits, err := fence.EffectiveLimitsInTx(ctx, db.Pool, quotaWriterA); err != nil || limits != planned {
		t.Fatalf("limits with a plan = %+v, %v; want %+v (his own projects stay over the plan)", limits, err, planned)
	}

	var reserving pgx.Tx
	reserve := func(at time.Time) error {
		return db.WithTx(ctx, func(tx pgx.Tx) error {
			reserving = tx
			return fence.ReserveTurnInTx(ctx, tx, quotaWriterB, quota.KeyChatTurnsPerDay, at)
		})
	}
	for turn := range 2 {
		if err := reserve(seed.now.Add(time.Duration(turn) * time.Minute)); err != nil {
			t.Fatalf("question %d: %v", turn+1, err)
		}
	}
	if exceeded, ok := quota.AsExceeded(reserve(seed.now.Add(time.Hour))); !ok || exceeded.Key != quota.KeyChatTurnsPerDay || exceeded.Limit != 2 {
		t.Fatalf("third question = %+v, %v; want the plan's limit 2", exceeded, ok)
	}
	if last := plan.seen[len(plan.seen)-1]; last != Querier(reserving) {
		t.Fatal("the plan was not read in the transaction of the check")
	}

	store := New(db, fence)
	account, err := store.AccountQuota(ctx, quotaWriterB, seed.now)
	if err != nil || account.Limits.ChatTurnsPerDay != 2 || account.Limits.AccountBooks != 12 || account.Limits.Projects != 3 || account.ChatTurns != 2 {
		t.Fatalf("account quota with a plan = %+v, %v", account, err)
	}
	sources, err := store.LimitSources(ctx, quotaWriterA)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[quota.Key]quota.Source{
		quota.KeyProjects: quota.SourceOwn, quota.KeyChatTurnsPerDay: quota.SourcePlan, quota.KeyAccountBooks: quota.SourcePlan,
		quota.KeyActiveTurns: quota.SourceDefault,
	} {
		if sources[key] != want {
			t.Fatalf("source of %s = %q, want %q", key, sources[key], want)
		}
	}
	if sources, err := plain.LimitSources(ctx, quotaWriterB); err != nil || sources[quota.KeyChatTurnsPerDay] != quota.SourceDefault {
		t.Fatalf("sources without a plan = %v, %v", sources, err)
	}
}

// specs/2026-11-09-quota-accounting.md Q1.2, R2.3: units of one daily limit started at once — a question and a
// correction, two drawings — never pass the limit: the second waits for the day row of the first and sees its unit; a
// release gives back to the day it is named by and never below zero.
func TestDailyUnitsAtOnceKeepToTheLimitPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_daily_race")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	seed := &sourceSeed{t: t, db: db, now: now}
	seed.user(quotaWriterA, "race@example.test")
	seed.exec(`INSERT INTO writer_quota_overrides (user_id, chat_turns_per_day, updated_at) VALUES ($1, 3, $2)`, quotaWriterA, now)
	ctx := context.Background()
	reserve := func() error {
		return db.WithTx(ctx, func(tx pgx.Tx) error {
			return Fence{}.ReserveDailyInTx(ctx, tx, quotaWriterA, quota.KeyChatTurnsPerDay, 1, now)
		})
	}
	if err := reserve(); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 4)
	for range 4 {
		go func() { results <- reserve() }()
	}
	accepted, refused := 0, 0
	for range 4 {
		err := <-results
		switch exceeded, ok := quota.AsExceeded(err); {
		case err == nil:
			accepted++
		case ok && exceeded.Key == quota.KeyChatTurnsPerDay:
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 2 || refused != 2 {
		t.Fatalf("accepted %d, refused %d at once; want 2 and 2", accepted, refused)
	}
	used := func(day time.Time) int64 {
		t.Helper()
		var value int64
		if err := db.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT chat_turns FROM writer_usage_days WHERE user_id = $1 AND day = $2::date), 0)`,
			quotaWriterA, quota.MoscowDay(day).Format(time.DateOnly)).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if day := used(now); day != 3 {
		t.Fatalf("questions of the day = %d, want 3", day)
	}
	release := func(at time.Time, count int64) {
		t.Helper()
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			return Fence{}.ReleaseDailyInTx(ctx, tx, quotaWriterA, quota.KeyChatTurnsPerDay, count, at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	release(now.Add(24*time.Hour), 1)
	if day := used(now); day != 3 {
		t.Fatalf("a release of another day took from this one: %d", day)
	}
	release(now, 5)
	if day := used(now); day != 0 {
		t.Fatalf("questions after a release over the count = %d, want 0", day)
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return Fence{}.ReserveDailyInTx(ctx, tx, quotaWriterA, quota.KeyActiveTurns, 1, now)
	}); !errors.Is(err, ErrNotDaily) {
		t.Fatalf("a unit of a limit no day counts = %v", err)
	}
}

// elsewhereStub is the turns of a writer that run outside this database — the live permits of his apps on the site
// (specs/2026-11-13-desk-permits.md PM1.4) — and the queriers and moments it was read with.
type elsewhereStub struct {
	turns int64
	seen  []Querier
	at    []time.Time
}

func (elsewhere *elsewhereStub) ActiveTurnsInTx(_ context.Context, db Querier, _ string, at time.Time) (int64, error) {
	elsewhere.seen = append(elsewhere.seen, db)
	elsewhere.at = append(elsewhere.at, at)
	return elsewhere.turns, nil
}

// specs/2026-11-13-desk-permits.md PM1.4, PM3.2: «разом» is one number — the turns of the site and the turns that run
// elsewhere: a new turn is refused when they reach the limit together, the check reads them in its own transaction at its
// own moment, and getAccountQuota shows the same sum. Without Elsewhere — the desktop — the count is the Jobs alone.
func TestActiveTurnsCountTheTurnsElsewherePostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_fence_elsewhere")
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	seed := &sourceSeed{t: t, db: db, now: now}
	seed.user(quotaWriterA, "elsewhere@example.test")
	seed.exec(`INSERT INTO writer_quota_overrides (user_id, active_turns, updated_at) VALUES ($1, 2, $2)`, quotaWriterA, now)
	ctx := context.Background()
	elsewhere := &elsewhereStub{turns: 2}
	fence := Fence{Elsewhere: elsewhere}

	var reserving pgx.Tx
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		reserving = tx
		return fence.ReserveTurnInTx(ctx, tx, quotaWriterA, quota.KeyChatTurnsPerDay, now)
	})
	if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != quota.KeyActiveTurns || exceeded.Limit != 2 {
		t.Fatalf("a turn with two turns elsewhere = %v; want quota.active_turns at 2", err)
	}
	if len(elsewhere.seen) != 1 || elsewhere.seen[0] != Querier(reserving) || !elsewhere.at[0].Equal(now) {
		t.Fatal("the turns elsewhere were not read in the transaction of the check at its moment")
	}
	elsewhere.turns = 1
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return fence.ReserveTurnInTx(ctx, tx, quotaWriterA, quota.KeyChatTurnsPerDay, now)
	}); err != nil {
		t.Fatalf("a turn with one turn elsewhere = %v", err)
	}
	account, err := New(db, fence).AccountQuota(ctx, quotaWriterA, now)
	if err != nil || account.ActiveTurns != 1 || account.Limits.ActiveTurns != 2 {
		t.Fatalf("account quota with one turn elsewhere = %+v, %v", account, err)
	}
	if account, err := New(db, Fence{}).AccountQuota(ctx, quotaWriterA, now); err != nil || account.ActiveTurns != 0 {
		t.Fatalf("account quota without elsewhere = %+v, %v", account, err)
	}
}
