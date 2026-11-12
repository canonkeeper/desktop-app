package quotastore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/jackc/pgx/v5"
)

// DeletedWriter names the counters of a deleted account: their id is a pseudonym that no
// account has (specs/2026-10-28-owner-tools-and-counters.md R2.5).
const DeletedWriter = "удалённый аккаунт"

// ErrWriterNotFound means no account has the address.
var ErrWriterNotFound = errors.New("quota: no writer with this address")

// Writer is an account the operator names by its address.
type Writer struct {
	ID    string
	Email string
}

// WriterByEmail finds the account of an address (addresses are stored lower case).
func WriterByEmail(ctx context.Context, db Querier, email string) (Writer, error) {
	var writer Writer
	err := db.QueryRow(ctx, `SELECT id::text, email FROM users WHERE email = $1`,
		strings.ToLower(strings.TrimSpace(email))).Scan(&writer.ID, &writer.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Writer{}, ErrWriterNotFound
	}
	return writer, err
}

// UsageRow is the spend of one writer, or of one writer in one project, over the report days.
type UsageRow struct {
	Writer    Writer
	ProjectID string
	ChatTurns int64
	Imports   int64
	Chat      quota.ModelUsage
	Scan      quota.ModelUsage
	Cards     quota.ModelUsage
	Images    quota.ModelUsage
	Layer     quota.ModelUsage
	Draft     quota.ModelUsage
	Drawing   quota.ModelUsage
	// DraftHelper is the helper of the chapter workshop (specs/2026-11-02-writer-workshop.md W1.5).
	DraftHelper quota.ModelUsage
	// Voice is the assistant of the voice co-author (specs/2026-11-03-voice-assistant.md V4.3).
	Voice quota.ModelUsage
	// Characters is the live draft text the writer owns (in the project, by project).
	Characters int64
}

// UsageReport is the operator's report (R9.2): per writer — or per writer and project — the
// questions and uploads since the first day, model spend by source and the text they own; and
// the last reading of the provider's usage window. No question, answer or book text is read.
func UsageReport(ctx context.Context, db Querier, since time.Time, byProject bool) ([]UsageRow, *quota.LimitSample, error) {
	day := since.Format(time.DateOnly)
	rows := map[[2]string]*UsageRow{}
	row := func(userID, projectID string) *UsageRow {
		if !byProject {
			projectID = ""
		}
		key := [2]string{userID, projectID}
		if rows[key] == nil {
			rows[key] = &UsageRow{Writer: Writer{ID: userID}, ProjectID: projectID}
		}
		return rows[key]
	}
	days, err := db.Query(ctx, `
		SELECT user_id::text, sum(chat_turns)::bigint, sum(imports)::bigint
		FROM writer_usage_days WHERE day >= $1::date GROUP BY user_id
	`, day)
	if err != nil {
		return nil, nil, err
	}
	for days.Next() {
		var userID string
		var turns, imports int64
		if err := days.Scan(&userID, &turns, &imports); err != nil {
			days.Close()
			return nil, nil, err
		}
		// Questions and uploads are counted per writer only; by project they stay on the writer row.
		target := row(userID, "")
		target.ChatTurns, target.Imports = turns, imports
	}
	days.Close()
	if err := days.Err(); err != nil {
		return nil, nil, err
	}
	spends, err := db.Query(ctx, `
		SELECT user_id::text, project_id::text, source, sum(calls)::bigint, sum(input_tokens)::bigint,
		       sum(cached_input_tokens)::bigint, sum(output_tokens)::bigint, sum(reasoning_output_tokens)::bigint
		FROM model_usage_days WHERE day >= $1::date GROUP BY user_id, project_id, source
	`, day)
	if err != nil {
		return nil, nil, err
	}
	for spends.Next() {
		var userID, projectID, source string
		var usage quota.ModelUsage
		if err := spends.Scan(&userID, &projectID, &source, &usage.Calls, &usage.InputTokens,
			&usage.CachedInputTokens, &usage.OutputTokens, &usage.ReasoningOutputTokens); err != nil {
			spends.Close()
			return nil, nil, err
		}
		target := row(userID, projectID)
		switch quota.UsageSource(source) {
		case quota.UsageChat:
			target.Chat = target.Chat.Add(usage)
		case quota.UsageScan:
			target.Scan = target.Scan.Add(usage)
		// The report has one line for the direct models over the text of a saga.
		case quota.UsageCards, quota.UsageAuthorMaterials:
			target.Cards = target.Cards.Add(usage)
		case quota.UsageImages:
			target.Images = target.Images.Add(usage)
		case quota.UsageLayer:
			target.Layer = target.Layer.Add(usage)
		case quota.UsageDraft:
			target.Draft = target.Draft.Add(usage)
		case quota.UsageDrawing:
			target.Drawing = target.Drawing.Add(usage)
		case quota.UsageDraftHelper:
			target.DraftHelper = target.DraftHelper.Add(usage)
		case quota.UsageVoice:
			target.Voice = target.Voice.Add(usage)
		}
	}
	spends.Close()
	if err := spends.Err(); err != nil {
		return nil, nil, err
	}
	writers := map[string]bool{}
	for key := range rows {
		writers[key[0]] = true
	}
	for userID := range writers {
		err := db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, userID).Scan(&row(userID, "").Writer.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			row(userID, "").Writer.Email = DeletedWriter
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		owned, err := ownerSources(ctx, db, userID)
		if err != nil {
			return nil, nil, err
		}
		for _, project := range owned {
			row(userID, project.ProjectID).Characters += project.Sources.Characters
		}
	}
	report := make([]UsageRow, 0, len(rows))
	for _, value := range rows {
		value.Writer.Email = rows[[2]string{value.Writer.ID, ""}].Writer.Email
		report = append(report, *value)
	}
	sort.Slice(report, func(i, j int) bool {
		if report[i].Writer.Email != report[j].Writer.Email {
			return report[i].Writer.Email < report[j].Writer.Email
		}
		return report[i].ProjectID < report[j].ProjectID
	})
	sample, err := lastLimitSample(ctx, db)
	return report, sample, err
}

func lastLimitSample(ctx context.Context, db Querier) (*quota.LimitSample, error) {
	var sample quota.LimitSample
	var limitID *string
	var window *int64
	err := db.QueryRow(ctx, `
		SELECT observed_at, limit_id, window_minutes::bigint, used_percent::double precision, resets_at
		FROM model_limit_samples ORDER BY observed_at DESC LIMIT 1
	`).Scan(&sample.ObservedAt, &limitID, &window, &sample.UsedPercent, &sample.ResetsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if limitID != nil {
		sample.LimitID = *limitID
	}
	if window != nil {
		sample.WindowMinutes = *window
	}
	return &sample, nil
}
