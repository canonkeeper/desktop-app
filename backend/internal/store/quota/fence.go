// Package quotastore checks a writer's quotas inside the transaction that creates model work or
// text (specs/2026-10-28-writer-quotas-and-fair-queue.md UC1–UC4), like the session write fence:
// the caller owns the transaction, a refusal is a *quota.Exceeded and the caller rolls back.
package quotastore

import (
	"context"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is what a transaction and a pool share; reads run in either.
type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// EffectiveLimitsInTx returns the writer's limits in force (R1.1; admin-panel R7.2): his own
// value, else the platform default, else the code's, by quota.Resolve. One query reads both rows,
// so the quota checks pay no second round trip; nothing is cached, and a new own value or default
// is read by the next operation of any process without a restart (R6.3).
func EffectiveLimitsInTx(ctx context.Context, db querier, userID string) (quota.Limits, error) {
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
	return quota.Resolve(settingsOf(keys, values[:len(keys)]), settingsOf(keys, values[len(keys):])), nil
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

// ReserveChatTurnInTx is the quota step of UC2 «Вопросы чата: за сутки и одновременно». It runs
// in the createChatTurn transaction only for a new idempotency key, after the session fence:
// the writer's day row is incremented first (the row lock makes a second submission of the same
// writer wait for this transaction, R2.3), then the writer's active turns in every project are
// counted. A refusal leaves nothing behind once the caller rolls back.
func ReserveChatTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	limits, err := countChatTurnInTx(ctx, tx, actorUserID, at)
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

// ReserveDrawingTurnInTx is the quota step of a turn of the helper of the drawing workshop
// (specs/2026-11-02-image-workshop.md IW8.2): it counts the turn for its writer's Moscow day apart from the
// questions of the chat and refuses one over drawing_turns_per_day; the turns of the helper and the questions of
// the chat keep active_turns together, as they go to one model.
func ReserveDrawingTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	return reserveHelperTurnInTx(ctx, tx, actorUserID, at, quota.KeyDrawingTurnsPerDay, "drawing_turns")
}

// ReserveDraftTurnInTx is the quota step of a turn of the helper of the chapter workshop
// (specs/2026-11-02-writer-workshop.md W1.5): the same as a turn of the drawing helper, counted against
// draft_turns_per_day.
func ReserveDraftTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	return reserveHelperTurnInTx(ctx, tx, actorUserID, at, quota.KeyDraftTurnsPerDay, "draft_turns")
}

// reserveHelperTurnInTx counts a turn of a helper in its own column of the writer's day; the column is one of the
// two constants above, so it is safe to splice.
func reserveHelperTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time, key quota.Key, column string) error {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var turns int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, `+column+`)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET `+column+` = usage.`+column+` + 1
		RETURNING usage.`+column+`
	`, actorUserID, moscowDate(at)).Scan(&turns); err != nil {
		return err
	}
	if limit := limits.Value(key); turns > limit {
		return quota.DailyExceeded(key, limit, at)
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

// countChatTurnInTx counts a chat turn for its writer's Moscow day and refuses one over the limit.
func countChatTurnInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) (quota.Limits, error) {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return quota.Limits{}, err
	}
	var turns int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, chat_turns)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET chat_turns = usage.chat_turns + 1
		RETURNING usage.chat_turns
	`, actorUserID, moscowDate(at)).Scan(&turns); err != nil {
		return quota.Limits{}, err
	}
	if turns > limits.ChatTurnsPerDay {
		return quota.Limits{}, quota.DailyExceeded(quota.KeyChatTurnsPerDay, limits.ChatTurnsPerDay, at)
	}
	return limits, nil
}

// ReserveImageInTx is the quota step of a drawing (specs/2026-10-29-saga-illustrations.md R5.1):
// it counts the drawing for its writer's Moscow day in the caller's transaction and refuses one
// over images_per_day; the caller rolls back on a refusal.
func ReserveImageInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var images int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, images)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET images = usage.images + 1
		RETURNING usage.images
	`, actorUserID, moscowDate(at)).Scan(&images); err != nil {
		return err
	}
	if images > limits.ImagesPerDay {
		return quota.DailyExceeded(quota.KeyImagesPerDay, limits.ImagesPerDay, at)
	}
	return nil
}

// ReserveImageUploadInTx is the quota step of an upload into the saga gallery
// (specs/2026-10-29-saga-illustrations.md R15.7): it counts the upload for its writer's Moscow day
// in the caller's transaction and refuses one over image_uploads_per_day; the caller rolls back on
// a refusal.
func ReserveImageUploadInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var uploads int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, image_uploads)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET image_uploads = usage.image_uploads + 1
		RETURNING usage.image_uploads
	`, actorUserID, moscowDate(at)).Scan(&uploads); err != nil {
		return err
	}
	if uploads > limits.ImageUploadsPerDay {
		return quota.DailyExceeded(quota.KeyImageUploadsPerDay, limits.ImageUploadsPerDay, at)
	}
	return nil
}

// ReserveChapterDraftInTx is the quota step of a run of a plan or a chapter
// (specs/2026-11-01-writer-assistant-index.md, R7.6): it counts the run for its writer's Moscow day in the
// caller's transaction and refuses one over chapter_drafts_per_day; the caller rolls back on a refusal.
func ReserveChapterDraftInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var runs int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, chapter_drafts)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET chapter_drafts = usage.chapter_drafts + 1
		RETURNING usage.chapter_drafts
	`, actorUserID, moscowDate(at)).Scan(&runs); err != nil {
		return err
	}
	if runs > limits.ChapterDraftsPerDay {
		return quota.DailyExceeded(quota.KeyChapterDraftsPerDay, limits.ChapterDraftsPerDay, at)
	}
	return nil
}

// ReserveDraftRewriteInTx counts a rewrite of a selection or a check of the seams of the writing agent for its writer's
// Moscow day and refuses one over draft_rewrites_per_day (specs/2026-11-06-writing-agent.md, WA5.13).
func ReserveDraftRewriteInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	limits, err := EffectiveLimitsInTx(ctx, tx, actorUserID)
	if err != nil {
		return err
	}
	var rewrites int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO writer_usage_days AS usage (user_id, day, draft_rewrites)
		VALUES ($1::uuid, $2::date, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET draft_rewrites = usage.draft_rewrites + 1
		RETURNING usage.draft_rewrites
	`, actorUserID, moscowDate(at)).Scan(&rewrites); err != nil {
		return err
	}
	if rewrites > limits.DraftRewritesPerDay {
		return quota.DailyExceeded(quota.KeyDraftRewritesPerDay, limits.DraftRewritesPerDay, at)
	}
	return nil
}

// ReleaseImageInTx gives back the drawing ReserveImageInTx counted for its writer's Moscow day of at
// (specs/2026-10-29-saga-illustrations.md R13.6) in the caller's transaction, never below zero.
func ReleaseImageInTx(ctx context.Context, tx pgx.Tx, actorUserID string, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE writer_usage_days SET images = images - 1
		WHERE user_id = $1::uuid AND day = $2::date AND images > 0
	`, actorUserID, moscowDate(at))
	return err
}

func activeTurns(ctx context.Context, db querier, userID string) (int64, error) {
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
