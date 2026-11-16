package quotastore

import (
	"context"
	"errors"
	"fmt"
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

// noProject is the project of a spend of no saga: of a linked app, or of a deleted account (R2.1).
const noProject = "00000000-0000-0000-0000-000000000000"

// UsagePart is what a row of the report by project holds (specs/2026-11-16-desk-admin-apps.md AA3.5).
type UsagePart string

const (
	// UsageOfSaga — a saga of the writer; in the report without projects, the writer.
	UsageOfSaga UsagePart = ""
	// UsageOfApps — the spend of the linked apps of the writer, by their sources whatever the project.
	UsageOfApps UsagePart = "apps"
	// UsageWithoutSaga — the spend of the site under no project: of a deleted account.
	UsageWithoutSaga UsagePart = "without_saga"
)

// UsageRow is the spend of one writer, or of one writer in one project, over the report days.
type UsageRow struct {
	Writer    Writer
	ProjectID string
	// Part is what the row holds in the report by project: a saga, the apps or the spend without a saga.
	Part      UsagePart
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
	// Apps is the work of the linked apps on the models of the site, every source app_* (AA3.5).
	Apps quota.ModelUsage
	// Characters is the live draft text the writer owns (in the project, by project).
	Characters int64
}

// usageColumn is the column of the report a source of the spend goes to: every source of quota.UsageSources has one
// (AA3.5); nil is a source the report does not know.
func usageColumn(row *UsageRow, source quota.UsageSource) *quota.ModelUsage {
	if source.OfApps() {
		return &row.Apps
	}
	switch source {
	case quota.UsageChat:
		return &row.Chat
	case quota.UsageScan:
		return &row.Scan
	// The report has one line for the direct models over the text of a saga.
	case quota.UsageCards, quota.UsageAuthorMaterials:
		return &row.Cards
	case quota.UsageImages:
		return &row.Images
	case quota.UsageLayer:
		return &row.Layer
	case quota.UsageDraft:
		return &row.Draft
	case quota.UsageDrawing:
		return &row.Drawing
	case quota.UsageDraftHelper:
		return &row.DraftHelper
	case quota.UsageVoice:
		return &row.Voice
	}
	return nil
}

// usageKey is a row of the report: a writer, and by project his saga or part.
type usageKey struct {
	user, project string
	part          UsagePart
}

// UsageReport is the operator's report (R9.2): per writer — or per writer and project — the
// questions and uploads since the first day, model spend by source and the text they own; and
// the last reading of the provider's usage window. No question, answer or book text is read. By
// project the spend of the apps is a row of its own and the spend of the site under no project is
// the row without a saga (AA3.5); a source the report has no column for is an error, never dropped.
func UsageReport(ctx context.Context, db Querier, since time.Time, byProject bool) ([]UsageRow, *quota.LimitSample, error) {
	day := since.Format(time.DateOnly)
	rows := map[usageKey]*UsageRow{}
	row := func(userID, projectID string, part UsagePart) *UsageRow {
		if !byProject {
			projectID, part = "", UsageOfSaga
		}
		key := usageKey{userID, projectID, part}
		if rows[key] == nil {
			rows[key] = &UsageRow{Writer: Writer{ID: userID}, ProjectID: projectID, Part: part}
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
		target := row(userID, "", UsageOfSaga)
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
		spent, part := quota.UsageSource(source), UsageOfSaga
		switch {
		case spent.OfApps():
			projectID, part = "", UsageOfApps
		case projectID == noProject:
			part = UsageWithoutSaga
		}
		column := usageColumn(row(userID, projectID, part), spent)
		if column == nil {
			spends.Close()
			return nil, nil, fmt.Errorf("quota: the usage report has no column for the source %q", source)
		}
		*column = column.Add(usage)
	}
	spends.Close()
	if err := spends.Err(); err != nil {
		return nil, nil, err
	}
	writers := map[string]bool{}
	for key := range rows {
		writers[key.user] = true
	}
	for userID := range writers {
		err := db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, userID).Scan(&row(userID, "", UsageOfSaga).Writer.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			row(userID, "", UsageOfSaga).Writer.Email = DeletedWriter
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
			row(userID, project.ProjectID, UsageOfSaga).Characters += project.Sources.Characters
		}
	}
	report := make([]UsageRow, 0, len(rows))
	for _, value := range rows {
		value.Writer.Email = rows[usageKey{value.Writer.ID, "", UsageOfSaga}].Writer.Email
		report = append(report, *value)
	}
	sort.Slice(report, func(i, j int) bool {
		if report[i].Writer.Email != report[j].Writer.Email {
			return report[i].Writer.Email < report[j].Writer.Email
		}
		if report[i].ProjectID != report[j].ProjectID {
			return report[i].ProjectID < report[j].ProjectID
		}
		return report[i].Part < report[j].Part
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
