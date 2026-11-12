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

const quotaAdmin = "93000000-0000-4000-8000-0000000000ad"

func limit(value int64) *int64 { return &value }

// specs/2026-10-29-admin-panel.md R7.1, R7.2: a writer's limit in force is his own value, else the
// platform default, else the code's; the next quota check reads a new default without a restart.
func TestPlatformDefaultsHoldForWritersWithoutTheirOwnLimitPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_defaults")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "own-limit@example.test")
	seed.user(quotaWriterB, "no-own-limit@example.test")
	seed.user(quotaAdmin, "admin@example.test")
	store := New(db, Fence{})
	ctx := context.Background()
	effective := func(userID string) quota.Limits {
		t.Helper()
		limits, err := Fence{}.EffectiveLimitsInTx(ctx, db.Pool, userID)
		if err != nil {
			t.Fatal(err)
		}
		return limits
	}

	if defaults, err := store.PlatformDefaults(ctx); err != nil || len(defaults.Values) != 0 || defaults.UpdatedAt != nil {
		t.Fatalf("platform defaults before a change = %+v, %v", defaults, err)
	}
	if effective(quotaWriterB) != quota.Defaults() {
		t.Fatal("without own and platform values the limits are the code's")
	}
	if _, err := store.SetOverrides(ctx, quotaWriterA, []quota.OverrideChange{{Key: quota.KeyChatTurnsPerDay, Value: limit(60)}}, seed.now); err != nil {
		t.Fatal(err)
	}
	changed, err := store.SetPlatformDefaults(ctx, quotaAdmin, []quota.OverrideChange{
		{Key: quota.KeyChatTurnsPerDay, Value: limit(2)}, {Key: quota.KeyActiveTurns, Value: limit(1)},
		{Key: quota.KeyAccountCharacters, Value: limit(40_000_000)}, {Key: quota.KeyDraftTurnsPerDay, Value: limit(9)},
	}, seed.now)
	if err != nil || len(changed) != 4 || changed[0].Before != nil || *changed[0].After != 2 || *changed[3].After != 9 {
		t.Fatalf("changed = %+v, %v", changed, err)
	}

	own, platform := effective(quotaWriterA), effective(quotaWriterB)
	if own.ChatTurnsPerDay != 60 || own.ActiveTurns != 1 || own.AccountCharacters != 40_000_000 || own.Projects != quota.Defaults().Projects {
		t.Fatalf("writer with his own limit = %+v", own)
	}
	if platform.ChatTurnsPerDay != 2 || platform.ActiveTurns != 1 || platform.ImportsPerDay != quota.Defaults().ImportsPerDay {
		t.Fatalf("writer without his own limit = %+v", platform)
	}
	reserve := func(at time.Time) error {
		return db.WithTx(ctx, func(tx pgx.Tx) error {
			return Fence{}.ReserveTurnInTx(ctx, tx, quotaWriterB, quota.KeyChatTurnsPerDay, at)
		})
	}
	for turn := range 2 {
		if err := reserve(seed.now.Add(time.Duration(turn) * time.Minute)); err != nil {
			t.Fatalf("question %d: %v", turn+1, err)
		}
	}
	if exceeded, ok := quota.AsExceeded(reserve(seed.now.Add(time.Hour))); !ok || exceeded.Key != quota.KeyChatTurnsPerDay || exceeded.Limit != 2 {
		t.Fatalf("third question = %+v, want the platform's 2 a day", exceeded)
	}

	// R7.5: a value that does not change is neither written nor returned; a repeat changes nothing.
	later := seed.now.Add(2 * time.Hour)
	if changed, err := store.SetPlatformDefaults(ctx, quotaAdmin, []quota.OverrideChange{{Key: quota.KeyChatTurnsPerDay, Value: limit(2)}}, later); err != nil || len(changed) != 0 {
		t.Fatalf("repeat = %+v, %v; want no change", changed, err)
	}
	defaults, err := store.PlatformDefaults(ctx)
	if err != nil || defaults.UpdatedAt == nil || !defaults.UpdatedAt.Equal(seed.now) || defaults.UpdatedBy != quotaAdmin ||
		defaults.Values[quota.KeyChatTurnsPerDay] != 2 || defaults.Values[quota.KeyDraftTurnsPerDay] != 9 || len(defaults.Values) != 4 {
		t.Fatalf("platform defaults = %+v, %v", defaults, err)
	}

	// «Вернуть исходное»: the code's value again, for the next operation.
	changed, err = store.SetPlatformDefaults(ctx, quotaAdmin, []quota.OverrideChange{
		{Key: quota.KeyChatTurnsPerDay}, {Key: quota.KeyProjects},
	}, later)
	if err != nil || len(changed) != 1 || changed[0].Key != quota.KeyChatTurnsPerDay || *changed[0].Before != 2 || changed[0].After != nil {
		t.Fatalf("return to the code's value = %+v, %v", changed, err)
	}
	if effective(quotaWriterB).ChatTurnsPerDay != quota.Defaults().ChatTurnsPerDay {
		t.Fatal("a value returned to the code's must hold from the next operation")
	}
	if err := reserve(seed.now.Add(3 * time.Hour)); err != nil {
		t.Fatalf("question after the default returned: %v", err)
	}
	if defaults, err := store.PlatformDefaults(ctx); err != nil || !defaults.UpdatedAt.Equal(later) {
		t.Fatalf("updated at = %v, %v; want the moment of the last change", defaults.UpdatedAt, err)
	}

	if _, err := store.SetPlatformDefaults(ctx, quotaAdmin, []quota.OverrideChange{{Key: quota.KeyProjects, Value: limit(-1)}}, later); !errors.Is(err, quota.ErrInvalidOverride) {
		t.Fatalf("negative value: err = %v", err)
	}
}
