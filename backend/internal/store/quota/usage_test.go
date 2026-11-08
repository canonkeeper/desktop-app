package quotastore

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/testutil"
)

// R7.2, R7.3: cards count for the project owner and add up per day; a spend without a writer is
// logged, not written.
func TestRecordModelUsageCountsCardsForProjectOwner(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_usage")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "usage-a@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	var logs bytes.Buffer
	recorder := NewUsageRecorder(db, log.New(&logs, "", 0))
	recorder.now = func() time.Time { return seed.now }
	card := quota.UsageRecord{Source: quota.UsageCards, ProjectID: quotaProjectA, Model: "gpt-6-luna",
		Usage: quota.ModelUsage{Calls: 1, InputTokens: 9000, CachedInputTokens: 100, OutputTokens: 700, ReasoningOutputTokens: 50}}
	for range 3 {
		recorder.RecordModelUsage(context.Background(), card)
	}
	var calls, input, cached, output, reasoning int64
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT calls, input_tokens, cached_input_tokens, output_tokens, reasoning_output_tokens
		FROM model_usage_days
		WHERE day = '2026-10-28' AND user_id = $1 AND project_id = $2 AND source = 'cards' AND model = 'gpt-6-luna'
	`, quotaWriterA, quotaProjectA).Scan(&calls, &input, &cached, &output, &reasoning); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || input != 27000 || cached != 300 || output != 2100 || reasoning != 150 {
		t.Fatalf("cards row = %d calls, %d/%d/%d/%d tokens", calls, input, cached, output, reasoning)
	}
	recorder.RecordModelUsage(context.Background(), quota.UsageRecord{
		Source: quota.UsageScan, ProjectID: quotaProjectA, JobID: "93000000-0000-4000-8000-0000000000ff",
		Model: "gpt-6-luna", Usage: quota.ModelUsage{Calls: 1},
	})
	if !strings.Contains(logs.String(), `"event":"model_usage_record_failed"`) || !strings.Contains(logs.String(), `"source":"scan"`) {
		t.Fatalf("log = %q, want model_usage_record_failed", logs.String())
	}
}

// specs/2026-11-03-voice-assistant.md V4.3: the assistant of the voice spends for the author it talks with, a source of
// its own that the operator's report shows apart; nothing is written for an empty spend.
func TestRecordWriterUsageKeepsTheVoiceForTheAuthor(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_voice")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "voice-a@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	var logs bytes.Buffer
	recorder := NewUsageRecorder(db, log.New(&logs, "", 0))
	recorder.now = func() time.Time { return seed.now }
	spend := quota.UsageRecord{Source: quota.UsageVoice, ProjectID: quotaProjectA, Model: "gpt-6.1-sol",
		Usage: quota.ModelUsage{Calls: 2, InputTokens: 4000, CachedInputTokens: 1000, OutputTokens: 300}}
	recorder.RecordWriterUsage(context.Background(), quotaWriterA, spend)
	recorder.RecordWriterUsage(context.Background(), quotaWriterA, quota.UsageRecord{Source: quota.UsageVoice, ProjectID: quotaProjectA, Model: "gpt-6.1-sol"})
	if logs.Len() != 0 {
		t.Fatalf("log = %q", logs.String())
	}
	rows, _, err := UsageReport(context.Background(), db.Pool, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ProjectID == quotaProjectA && row.Voice == spend.Usage {
			return
		}
	}
	t.Fatalf("the report = %+v", rows)
}

// R9.1: a reading is kept when the window moved by a point, its reset changed or ten minutes
// passed; others are dropped.
func TestRecordLimitSampleThinsReadings(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_limits")
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	resets := at.Add(72 * time.Hour)
	sample := func(offset time.Duration, percent float64, reset time.Time) quota.LimitSample {
		return quota.LimitSample{ObservedAt: at.Add(offset), LimitID: "codex", WindowMinutes: 10080, UsedPercent: percent, ResetsAt: &reset}
	}
	for _, step := range []struct {
		sample quota.LimitSample
		kept   bool
	}{
		{sample(0, 34, resets), true},
		{sample(time.Minute, 34.5, resets), false},
		{sample(2*time.Minute, 35, resets), true},
		{sample(3*time.Minute, 35, resets.Add(time.Hour)), true},
		{sample(14*time.Minute, 35, resets.Add(time.Hour)), true},
	} {
		kept, err := RecordLimitSample(context.Background(), db.Pool, step.sample)
		if err != nil || kept != step.kept {
			t.Fatalf("sample at %v = %v, %v; want kept %v", step.sample.ObservedAt, kept, err, step.kept)
		}
	}
}

// specs/2026-10-28-owner-tools-and-counters.md R2.4, R2.5, R3.3: a blocked writer's spend is
// written — it is real cost; a deleted account's spend never carries its id: what it spent
// before is moved to a pseudonym, what arrives after goes to a new one without the project; the
// report names such rows «удалённый аккаунт».
func TestUsageOfBlockedAndDeletedWriters(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_usage_deleted")
	ctx := context.Background()
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "usage-a@example.test")
	seed.user(quotaWriterB, "usage-b@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	spend := quota.UsageRecord{Source: quota.UsageChat, ProjectID: quotaProjectA, Model: "research-chat",
		Usage: quota.ModelUsage{Calls: 2, InputTokens: 500}}
	for _, writer := range []string{quotaWriterA, quotaWriterB} {
		if err := AddModelUsage(ctx, db.Pool, writer, spend, seed.now); err != nil {
			t.Fatal(err)
		}
	}
	seed.exec(`UPDATE users SET disabled_at = $2 WHERE id = $1`, quotaWriterB, seed.now)
	if err := AddModelUsage(ctx, db.Pool, quotaWriterB, spend, seed.now); err != nil {
		t.Fatal(err)
	}
	var blockedCalls int64
	if err := db.Pool.QueryRow(ctx, `SELECT calls FROM model_usage_days WHERE user_id = $1`, quotaWriterB).Scan(&blockedCalls); err != nil || blockedCalls != 4 {
		t.Fatalf("blocked writer's calls = %d, %v; want 4", blockedCalls, err)
	}
	var moved int64
	if err := db.Pool.QueryRow(ctx, `SELECT ck_anonymize_writer_usage($1)`, quotaWriterA).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("anonymized rows = %d, %v", moved, err)
	}
	seed.exec(`UPDATE users SET disabled_at = $2, deleted_at = $2 WHERE id = $1`, quotaWriterA, seed.now)
	if err := AddModelUsage(ctx, db.Pool, quotaWriterA, spend, seed.now); err != nil {
		t.Fatal(err)
	}
	var withID, pseudonyms, calls int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE user_id = $1),
		       count(DISTINCT user_id) FILTER (WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = model_usage_days.user_id)
		                                         AND project_id = '00000000-0000-0000-0000-000000000000'),
		       COALESCE(sum(calls), 0)
		FROM model_usage_days
	`, quotaWriterA).Scan(&withID, &pseudonyms, &calls); err != nil || withID != 0 || pseudonyms != 2 || calls != 8 {
		t.Fatalf("deleted writer's spend: %d rows with its id, %d pseudonyms, %d calls in all, %v", withID, pseudonyms, calls, err)
	}
	rows, _, err := UsageReport(ctx, db.Pool, seed.now.AddDate(0, 0, -1), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Writer.Email != "usage-b@example.test" || rows[0].Chat.Calls != 4 ||
		rows[1].Writer.Email != DeletedWriter || rows[2].Writer.Email != DeletedWriter ||
		rows[1].Chat.Calls != 2 || rows[2].Chat.Calls != 2 {
		t.Fatalf("report = %+v", rows)
	}
}
