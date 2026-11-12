package quotastore

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownWriter means no writer owns the spend: the project or the chat turn is gone.
var ErrUnknownWriter = errors.New("quota: the spend has no writer")

// RecordModelUsage adds a direct model spend to the day's aggregate (UC7): cards and the thread layer
// count for the project owner, a scan for the creator of its chat turn Job, a drawing for the creator of its
// illustration Job (R7.2) a plan or a chapter for the creator of its chapter_draft Job and an import for the creator of its Job.
func RecordModelUsage(ctx context.Context, db Querier, record quota.UsageRecord, at time.Time) error {
	if record.Usage.Empty() {
		return nil
	}
	var writer string
	var err error
	switch record.Source {
	case quota.UsageCards, quota.UsageLayer:
		err = db.QueryRow(ctx, `SELECT owner_user_id::text FROM projects WHERE id = $1::uuid`, record.ProjectID).Scan(&writer)
	case quota.UsageScan, quota.UsageImages, quota.UsageDraft, quota.UsageAuthorMaterials:
		err = db.QueryRow(ctx, `
			SELECT creator_user_id::text FROM jobs
			WHERE id = $1::uuid AND project_id = $2::uuid AND creator_user_id IS NOT NULL
		`, record.JobID, record.ProjectID).Scan(&writer)
	default:
		return ErrUnknownWriter
	}
	if err != nil {
		return errors.Join(ErrUnknownWriter, err)
	}
	return AddModelUsage(ctx, db, writer, record, at)
}

// AddModelUsage adds spend of a known writer to the day's aggregate. All spend is real cost and
// is written, a blocked writer's too (specs/2026-10-28-owner-tools-and-counters.md R2.4, R3.3);
// a deleted account's spend goes to a new pseudonym without the project, never under its id
// (R2.1). The writer's users row is held FOR KEY SHARE — the lock the dropped foreign key took —
// so the spend is written either before the account deletion, which then anonymizes it, or after
// it, under a pseudonym.
func AddModelUsage(ctx context.Context, db Querier, writer string, record quota.UsageRecord, at time.Time) error {
	usage := record.Usage
	_, err := db.Exec(ctx, `
		INSERT INTO model_usage_days AS spent (
		  day, user_id, project_id, source, model,
		  calls, input_tokens, cached_input_tokens, output_tokens, reasoning_output_tokens
		)
		SELECT $1::date,
		       CASE WHEN account.deleted_at IS NULL THEN account.id ELSE gen_random_uuid() END,
		       CASE WHEN account.deleted_at IS NULL THEN $3::uuid ELSE '00000000-0000-0000-0000-000000000000'::uuid END,
		       $4, $5, $6, $7, $8, $9, $10
		FROM (SELECT id, deleted_at FROM users WHERE id = $2::uuid FOR KEY SHARE) AS account
		ON CONFLICT (day, user_id, project_id, source, model) DO UPDATE SET
		  calls = spent.calls + EXCLUDED.calls,
		  input_tokens = spent.input_tokens + EXCLUDED.input_tokens,
		  cached_input_tokens = spent.cached_input_tokens + EXCLUDED.cached_input_tokens,
		  output_tokens = spent.output_tokens + EXCLUDED.output_tokens,
		  reasoning_output_tokens = spent.reasoning_output_tokens + EXCLUDED.reasoning_output_tokens
	`, moscowDate(at), writer, record.ProjectID, string(record.Source), record.Model,
		usage.Calls, usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningOutputTokens)
	return err
}

// UsageRecorder writes direct model spend for the cards and scan services. A failed write never
// fails their work: it becomes a model_usage_record_failed log line (R7.3).
type UsageRecorder struct {
	db     *storedb.DB
	logger *log.Logger
	now    func() time.Time
}

func NewUsageRecorder(db *storedb.DB, logger *log.Logger) *UsageRecorder {
	if db == nil || logger == nil {
		panic("quota usage recorder: database and logger are required")
	}
	return &UsageRecorder{db: db, logger: logger, now: time.Now}
}

func (recorder *UsageRecorder) RecordModelUsage(ctx context.Context, record quota.UsageRecord) {
	if err := RecordModelUsage(ctx, recorder.db.Pool, record, recorder.now()); err != nil {
		line, _ := json.Marshal(struct {
			Event     string `json:"event"`
			Source    string `json:"source"`
			ProjectID string `json:"projectId"`
			JobID     string `json:"jobId,omitempty"`
			Calls     int64  `json:"calls"`
		}{"model_usage_record_failed", string(record.Source), record.ProjectID, record.JobID, record.Usage.Calls})
		recorder.logger.Print(string(line))
	}
}

// RecordWriterUsage writes spend of a known writer, the voice co-author's assistant spending for the author it talks
// with (specs/2026-11-03-voice-assistant.md V4.3); a failed write is a model_usage_record_failed log line too.
func (recorder *UsageRecorder) RecordWriterUsage(ctx context.Context, writer string, record quota.UsageRecord) {
	if record.Usage.Empty() {
		return
	}
	if err := AddModelUsage(ctx, recorder.db.Pool, writer, record, recorder.now()); err != nil {
		line, _ := json.Marshal(struct {
			Event     string `json:"event"`
			Source    string `json:"source"`
			ProjectID string `json:"projectId"`
			Calls     int64  `json:"calls"`
		}{"model_usage_record_failed", string(record.Source), record.ProjectID, record.Usage.Calls})
		recorder.logger.Print(string(line))
	}
}

// limitSampleEvery is how often an unchanged usage window is still written (R9.1).
const limitSampleEvery = 10 * time.Minute

// RecordLimitSample keeps a reading of the provider's usage window when it moved by at least one
// percentage point, its reset time changed or ten minutes passed since the last one; readings
// older than 60 days are removed (R9.1). It reports whether the reading was written.
func RecordLimitSample(ctx context.Context, db Querier, sample quota.LimitSample) (bool, error) {
	var lastAt time.Time
	var lastPercent float64
	var lastResets *time.Time
	err := db.QueryRow(ctx, `
		SELECT observed_at, used_percent::double precision, resets_at
		FROM model_limit_samples ORDER BY observed_at DESC LIMIT 1
	`).Scan(&lastAt, &lastPercent, &lastResets)
	switch {
	case err == nil:
		sameReset := lastResets == nil && sample.ResetsAt == nil ||
			lastResets != nil && sample.ResetsAt != nil && lastResets.Equal(*sample.ResetsAt)
		if sameReset && math.Abs(sample.UsedPercent-lastPercent) < 1 && sample.ObservedAt.Sub(lastAt) < limitSampleEvery {
			return false, nil
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return false, err
	}
	var limitID, windowMinutes any
	if sample.LimitID != "" {
		limitID = sample.LimitID
	}
	if sample.WindowMinutes > 0 {
		windowMinutes = sample.WindowMinutes
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO model_limit_samples (observed_at, limit_id, window_minutes, used_percent, resets_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (observed_at) DO NOTHING
	`, sample.ObservedAt, limitID, windowMinutes, sample.UsedPercent, sample.ResetsAt); err != nil {
		return false, err
	}
	_, err = db.Exec(ctx, `DELETE FROM model_limit_samples WHERE observed_at < $1`, sample.ObservedAt.Add(-60*24*time.Hour))
	return err == nil, err
}
