package quotastore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-10-29-saga-illustrations.md R5.1: drawings count per Moscow day against
// images_per_day; the one over the limit is refused until midnight and the account shows the use.
func TestReserveImageCountsDrawingsAgainstTheDailyLimit(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_images")
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	seed := &sourceSeed{t: t, db: db, now: now}
	seed.user(quotaWriterA, "images@example.test")
	seed.exec(`INSERT INTO writer_quota_overrides (user_id, images_per_day, updated_at) VALUES ($1, 2, $2)`, quotaWriterA, now)
	reserve := func() error {
		return db.WithTx(context.Background(), func(tx pgx.Tx) error {
			return ReserveImageInTx(context.Background(), tx, quotaWriterA, now)
		})
	}
	for range 2 {
		if err := reserve(); err != nil {
			t.Fatal(err)
		}
	}
	exceeded, ok := quota.AsExceeded(reserve())
	if !ok || exceeded.Key != quota.KeyImagesPerDay || exceeded.Limit != 2 || exceeded.ResetsAt == nil ||
		!exceeded.ResetsAt.Equal(time.Date(2026, 10, 29, 21, 0, 0, 0, time.UTC)) || exceeded.Code() != "quota.images_daily" {
		t.Fatalf("third drawing = %+v, %v", exceeded, ok)
	}
	account, err := New(db).AccountQuota(context.Background(), quotaWriterA, now)
	if err != nil {
		t.Fatal(err)
	}
	if account.Images != 2 || account.Limits.ImagesPerDay != 2 {
		t.Fatalf("account images = %d of %d, want 2 of 2", account.Images, account.Limits.ImagesPerDay)
	}
	// R11.4: the page of illustrations shows the drawings of the day the same way; another day has none.
	if today, err := New(db).ImagesToday(context.Background(), quotaWriterA, now); err != nil || today != (quota.Counter{Used: 2, Limit: 2}) {
		t.Fatalf("images today = %+v, %v", today, err)
	}
	if tomorrow, err := New(db).ImagesToday(context.Background(), quotaWriterA, now.Add(24*time.Hour)); err != nil || tomorrow != (quota.Counter{Used: 0, Limit: 2}) {
		t.Fatalf("images tomorrow = %+v, %v", tomorrow, err)
	}
	if quota.Defaults().ImagesPerDay != 30 {
		t.Fatalf("default images_per_day = %d, want 30", quota.Defaults().ImagesPerDay)
	}
}
