// Package quotastore checks a writer's quotas inside the transaction that creates model work or
// text (specs/2026-10-28-writer-quotas-and-fair-queue.md UC1–UC4), like the session write fence:
// the caller owns the transaction, a refusal is a *quota.Exceeded and the caller rolls back.
package quotastore

import (
	"context"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is what a transaction and a pool share; reads run in either.
type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Plans is the writer's plan the fence puts between his own limits and the platform's
// (specs/2026-11-08-subscription-tiers.md TR2.3). The fence declares it and a build implements it: it reads the
// writer's plan in the fence's own transaction, keeps its own clock and answers with the keys the plan sets — none
// when the writer has no plan. The open core knows nothing of what makes a plan.
type Plans interface {
	PlanSettingsInTx(ctx context.Context, db Querier, userID string) (quota.Settings, error)
}

// Fence is the quota fence of a build (TR2.4): every limit is read and checked through it. Without Plans — the
// desktop, a test — the limits are the writer's own, the platform's and the code's, as before plans.
type Fence struct {
	Plans Plans
}

// EffectiveLimitsInTx returns the writer's limits in force (R1.1; admin-panel R7.2; TR2.2): his own value, else his
// plan's, else the platform default, else the code's, by quota.Resolve. One query reads his own row and the platform's,
// so the quota checks pay no second round trip for them; nothing is cached, and a new own value, plan or default is
// read by the next operation of any process without a restart (R6.3).
func (fence Fence) EffectiveLimitsInTx(ctx context.Context, db Querier, userID string) (quota.Limits, error) {
	keys := quota.Keys()
	values := make([]*int64, 2*len(keys))
	targets := make([]any, len(values))
	for index := range values {
		targets[index] = &values[index]
	}
	// The columns are named by quota.Keys only, so they are safe to splice.
	err := db.QueryRow(ctx, `
		SELECT `+limitColumns("own", keys)+`, `+limitColumns("platform", keys)+`
		FROM (SELECT $1::uuid AS user_id) AS writer
		LEFT JOIN writer_quota_overrides AS own ON own.user_id = writer.user_id
		LEFT JOIN platform_quota_defaults AS platform ON true
	`, userID).Scan(targets...)
	if err != nil {
		return quota.Limits{}, err
	}
	plan, err := fence.planInTx(ctx, db, userID)
	if err != nil {
		return quota.Limits{}, err
	}
	return quota.Resolve(settingsOf(keys, values[:len(keys)]), plan, settingsOf(keys, values[len(keys):])), nil
}

// planInTx is the writer's plan, none without Plans.
func (fence Fence) planInTx(ctx context.Context, db Querier, userID string) (quota.Settings, error) {
	if fence.Plans == nil {
		return nil, nil
	}
	return fence.Plans.PlanSettingsInTx(ctx, db, userID)
}

// limitColumns lists the columns of the keys of one row as bigint, in the order of the keys.
func limitColumns(alias string, keys []quota.Key) string {
	columns := make([]string, len(keys))
	for index, key := range keys {
		columns[index] = alias + "." + string(key) + "::bigint"
	}
	return strings.Join(columns, ", ")
}

// settingsOf keeps the values that are set; NULL leaves the key to the next source.
func settingsOf(keys []quota.Key, values []*int64) quota.Settings {
	settings := quota.Settings{}
	for index, key := range keys {
		if values[index] != nil {
			settings[key] = *values[index]
		}
	}
	return settings
}

// dailyColumns are the columns of writer_usage_days that count the daily limits of a writer
// (specs/2026-11-09-quota-accounting.md Q1.2): one unit of an action is one more in its column for the Moscow day it
// started on. The columns are named here only, so they are safe to splice.
var dailyColumns = map[quota.Key]string{
	quota.KeyChatTurnsPerDay:     "chat_turns",
	quota.KeyImportsPerDay:       "imports",
	quota.KeyImagesPerDay:        "images",
	quota.KeyImageUploadsPerDay:  "image_uploads",
	quota.KeyChapterDraftsPerDay: "chapter_drafts",
	quota.KeyDrawingTurnsPerDay:  "drawing_turns",
	quota.KeyDraftTurnsPerDay:    "draft_turns",
	quota.KeyDraftRewritesPerDay: "draft_rewrites",
}

// ErrNotDaily means a key that no day counts: a caller's mistake, never a refusal of the writer.
var ErrNotDaily = errors.New("quota: not a daily limit")

// ReserveTurnInTx is the quota step of a new turn of the chat or of a helper (UC2; specs/2026-11-02-image-workshop.md
// IW8.2; specs/2026-11-02-writer-workshop.md W1.5): the turn counts one unit of its key for the writer's Moscow day —
// the day row is incremented first, so its lock makes a second submission of the same writer wait for this
// transaction (R2.3) — and then the writer's active turns in every project are counted: the questions of the chat and
// the turns of the helpers keep active_turns together, as they go to one model. A refusal leaves nothing behind once
// the caller rolls back.
func (fence Fence) ReserveTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, at time.Time) error {
	limits, err := fence.reserveDailyInTx(ctx, tx, actorUserID, key, 1, at)
	if err != nil {
		return err
	}
	active, err := activeTurns(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	if active >= limits.ActiveTurns {
		return &quota.Exceeded{Key: quota.KeyActiveTurns, Limit: limits.ActiveTurns}
	}
	return nil
}

// ReserveDailyInTx counts count units of a daily limit for the writer's Moscow day of at in the caller's transaction
// and refuses the units over the limit (Q1.2): a drawing, an upload, a run of the writing agent, a correction of a
// running turn. The day row lock serialises the units of one writer; the caller rolls back on a refusal.
func (fence Fence) ReserveDailyInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, count int64, at time.Time) error {
	_, err := fence.reserveDailyInTx(ctx, tx, actorUserID, key, count, at)
	return err
}

func (fence Fence) reserveDailyInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, count int64, at time.Time) (quota.Limits, error) {
	limits, err := fence.EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return quota.Limits{}, err
	}
	used, err := countDailyInTx(ctx, tx, actorUserID, key, count, at)
	if err != nil {
		return quota.Limits{}, err
	}
	if limit := limits.Value(key); used > limit {
		return quota.Limits{}, quota.DailyExceeded(key, limit, at)
	}
	return limits, nil
}

// CountDailyInTx counts units of a daily limit without its check: a drawing a trial of the writer's tier pays for
// (specs/2026-11-08-subscription-tiers.md TR6.1) shows in the day's drawings, and ReleaseDailyInTx gives it back the
// same way.
func (fence Fence) CountDailyInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, count int64, at time.Time) error {
	_, err := countDailyInTx(ctx, tx, actorUserID, key, count, at)
	return err
}

func countDailyInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, count int64, at time.Time) (int64, error) {
	column, daily := dailyColumns[key]
	if !daily || count < 0 {
		return 0, ErrNotDaily
	}
	var used int64
	err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, `+column+`)
		VALUES ($1::uuid, $2::date, $3)
		ON CONFLICT (user_id, day) DO UPDATE SET `+column+` = usage.`+column+` + EXCLUDED.`+column+`
		RETURNING usage.`+column+`
	`, actorUserID, moscowDate(at), count).Scan(&used)
	return used, err
}

// ReleaseDailyInTx gives back count units of a daily limit counted for the writer's Moscow day of at — the day the
// work started on, never the day it ended — in the caller's transaction, never below zero (Q1.4): the work did not
// reach the author through no fault of his.
func (fence Fence) ReleaseDailyInTx(ctx context.Context, tx pgx.Tx, actorUserID string, key quota.Key, count int64, at time.Time) error {
	column, daily := dailyColumns[key]
	if !daily || count < 0 {
		return ErrNotDaily
	}
	_, err := tx.Exec(ctx, `
		UPDATE writer_usage_days SET `+column+` = GREATEST(`+column+` - $3, 0)
		WHERE user_id = $1::uuid AND day = $2::date AND `+column+` > 0
	`, actorUserID, moscowDate(at), count)
	return err
}

func activeTurns(ctx context.Context, db Querier, userID string) (int64, error) {
	var active int64
	err := db.QueryRow(ctx, `
		SELECT count(*)
		FROM jobs
		WHERE creator_user_id = $1::uuid AND kind = 'research_chat_turn'
		  AND status IN ('queued', 'running', 'canceling')
	`, userID).Scan(&active)
	return active, err
}

// moscowDate is the Moscow day as a date literal: a timestamp would be cast to a date in the
// session time zone.
func moscowDate(at time.Time) string {
	return quota.MoscowDay(at).Format(time.DateOnly)
}
