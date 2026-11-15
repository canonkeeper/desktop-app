// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/modeljournal"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// group is a row as one day of the records gives it.
type group struct {
	row     domain.Row
	ids     []string
	day     string
	purpose string
}

// Rows is a page of the rows of the journal from the newest (JR2.1, JR2.2): the days of the zone go one after another from
// the day of the cursor — or of the latest record — back, each read by the index of time alone; a row is a Job or a check
// within its day, by its first sending. A record whose request no record named does not exist here: the rows are the
// records'.
func (journal *Journal) Rows(ctx context.Context, query domain.RowsQuery) ([]domain.Row, *domain.Cursor, error) {
	zone, err := journal.zoneOf(ctx, query.Zone)
	if err != nil {
		return nil, nil, err
	}
	query.Zone = zone
	purposes := domain.PurposesOf(query.Kind)
	if query.Kind == "" {
		purposes = nil
	}
	var anchor *time.Time
	if query.After != nil {
		anchor = &query.After.Start
	} else {
		latest, err := journal.latestBefore(ctx, nil, purposes)
		if err != nil || latest == nil {
			return nil, nil, err
		}
		anchor = latest
	}
	var rows []group
	for anchor != nil && len(rows) <= query.Limit {
		var start, end time.Time
		var day string
		if err := journal.db.Pool.QueryRow(ctx, `
			SELECT date_trunc('day', $1::timestamptz AT TIME ZONE $2) AT TIME ZONE $2,
			  (date_trunc('day', $1::timestamptz AT TIME ZONE $2) + interval '1 day') AT TIME ZONE $2,
			  to_char(date_trunc('day', $1::timestamptz AT TIME ZONE $2), 'YYYY-MM-DD')`, *anchor, query.Zone).Scan(&start, &end, &day); err != nil {
			return nil, nil, err
		}
		groups, err := journal.dayGroups(ctx, start, end, day, purposes)
		if err != nil {
			return nil, nil, err
		}
		for _, found := range groups {
			if query.After != nil && !before(found.row, *query.After) {
				continue
			}
			rows = append(rows, found)
		}
		if anchor, err = journal.latestBefore(ctx, &start, purposes); err != nil {
			return nil, nil, err
		}
	}
	slices.SortStableFunc(rows, func(a, b group) int {
		if !a.row.Start.Equal(b.row.Start) {
			return b.row.Start.Compare(a.row.Start)
		}
		return strings.Compare(b.row.Key, a.row.Key)
	})
	var next *domain.Cursor
	if len(rows) > query.Limit {
		rows = rows[:query.Limit]
		last := rows[len(rows)-1].row
		next = &domain.Cursor{Start: last.Start, Key: last.Key}
	}
	page := make([]domain.Row, len(rows))
	for index := range rows {
		page[index] = rows[index].row
	}
	if err := journal.fill(ctx, rows, page); err != nil {
		return nil, nil, err
	}
	return page, next, nil
}

// before says a row comes after the cursor in the order of the page: an earlier start, or the same start and a smaller key.
func before(row domain.Row, cursor domain.Cursor) bool {
	return row.Start.Before(cursor.Start) || row.Start.Equal(cursor.Start) && row.Key < cursor.Key
}

// zoneOf is the zone the days of the journal go by: the zone of the computer of the author, or UTC when PostgreSQL does not
// know it — the journal opens all the same, its days by UTC (specs/2026-11-14-desk-journal.md JR2.1). A zone of no name
// or of another form is ErrZone.
func (journal *Journal) zoneOf(ctx context.Context, zone string) (string, error) {
	if zone == "" || len(zone) > 64 {
		return "", domain.ErrZone
	}
	var now time.Time
	err := journal.db.Pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE $1) AT TIME ZONE 'UTC'`, zone).Scan(&now)
	var failure *pgconn.PgError
	if errors.As(err, &failure) && failure.Code == "22023" {
		return "UTC", nil
	}
	return zone, err
}

// latestBefore is the time of the latest record before a moment — of the purposes, by the index of each —, nil when there
// is none.
func (journal *Journal) latestBefore(ctx context.Context, moment *time.Time, purposes []string) (*time.Time, error) {
	var bound pgtype.Timestamptz
	if moment != nil {
		bound = pgtype.Timestamptz{Time: *moment, Valid: true}
	}
	var latest pgtype.Timestamptz
	var err error
	if len(purposes) == 0 {
		err = journal.db.Pool.QueryRow(ctx, `SELECT max(at) FROM model_disclosures WHERE $1::timestamptz IS NULL OR at < $1`, bound).Scan(&latest)
	} else {
		err = journal.db.Pool.QueryRow(ctx, `
			SELECT max(latest) FROM unnest($2::text[]) AS wanted(purpose)
			CROSS JOIN LATERAL (
			  SELECT max(at) AS latest FROM model_disclosures AS record
			  WHERE record.purpose = wanted.purpose AND ($1::timestamptz IS NULL OR record.at < $1)
			) AS found`, bound, purposes).Scan(&latest)
	}
	if err != nil || !latest.Valid {
		return nil, err
	}
	return &latest.Time, nil
}

// workOf is the work of a record named record: its Job, its check, the program and the saga of a reading of a program
// (specs/2026-11-15-desk-mcp.md MC4.4) — a row is a program × a saga × a day, sagas a program × a day —, or the record itself.
// One expression for the rows, their details and the cursor; the key of a row stays work|day.
const workOf = `(CASE WHEN record.purpose = 'mcp' THEN md5('mcp|' || record.client || '|' || coalesce(record.project_id::text, ''))::uuid
	ELSE coalesce(record.job_id, record.check_id, record.id) END)`

// dayGroups are the rows of one day: the records of the day by their work. To whom a row went is the helpers of its
// sendings, or the program that read.
func (journal *Journal) dayGroups(ctx context.Context, start, end time.Time, day string, purposes []string) ([]group, error) {
	if purposes == nil {
		purposes = []string{}
	}
	rows, err := journal.db.Pool.Query(ctx, `
		SELECT `+workOf+`::text AS work, min(at), max(at), count(*), sum(request_bytes), bool_or(first_time),
		  min(purpose), coalesce((array_agg(project_id::text) FILTER (WHERE project_id IS NOT NULL))[1], ''),
		  array_agg(DISTINCT CASE WHEN purpose = 'mcp' THEN client ELSE profile_name || ' ' || provider_host END), array_agg(named),
		  array_agg(id::text ORDER BY at, id), coalesce(bool_or(pages > 1), false)
		FROM model_disclosures AS record
		WHERE at >= $1 AND at < $2 AND (cardinality($3::text[]) = 0 OR purpose = ANY($3))
		GROUP BY 1`, start, end, purposes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []group
	for rows.Next() {
		var found group
		var work string
		var recipients []string
		var named [][]byte
		var bytes int64
		if err := rows.Scan(&work, &found.row.Start, &found.row.Last, &found.row.Sendings, &bytes, &found.row.FirstTime,
			&found.purpose, &found.row.ProjectID, &recipients, &named, &found.ids, &found.row.Paged); err != nil {
			return nil, err
		}
		found.day, found.row.Key, found.row.Bytes = day, work+"|"+day, bytes
		found.row.Kind = domain.KindOf(found.purpose)
		found.row.Protocol = true
		namings := make([]disclosure.Named, len(named))
		for index, raw := range named {
			if err := json.Unmarshal(raw, &namings[index]); err != nil {
				return nil, err
			}
			found.row.Protocol = found.row.Protocol && namings[index].Protocol && namings[index].Empty()
		}
		found.row.Named = disclosure.Merge(namings...)
		slices.Sort(recipients)
		for _, recipient := range recipients {
			if found.row.Kind == domain.KindProgram {
				found.row.Recipients = append(found.row.Recipients, recipient)
				continue
			}
			profile, host, _ := strings.Cut(recipient, " ")
			found.row.Recipients = append(found.row.Recipients, domain.RecipientWords(profile, host))
		}
		groups = append(groups, found)
	}
	return groups, rows.Err()
}

// fill names the saga, the chat and the state of the work of the rows of a page, and the numbers of «Свой ИИ».
func (journal *Journal) fill(ctx context.Context, groups []group, rows []domain.Row) error {
	var works, projects, ids []string
	for index, found := range groups {
		work, _, _ := strings.Cut(rows[index].Key, "|")
		works = append(works, work)
		if rows[index].ProjectID != "" {
			projects = append(projects, rows[index].ProjectID)
		}
		ids = append(ids, found.ids...)
	}
	type saga struct {
		title   string
		deleted bool
	}
	sagas := map[string]saga{}
	if err := collect(ctx, journal.db.Pool, `SELECT id::text, name, deleted_at IS NOT NULL FROM projects WHERE id = ANY($1::uuid[])`, func(row pgx.Rows) error {
		var id string
		var found saga
		if err := row.Scan(&id, &found.title, &found.deleted); err != nil {
			return err
		}
		sagas[id] = found
		return nil
	}, projects); err != nil {
		return err
	}
	type job struct {
		live      bool
		chatTitle pgtype.Text
		chatGone  bool
	}
	jobs := map[string]job{}
	if err := collect(ctx, journal.db.Pool, `
		SELECT job.id::text, job.status IN ('queued', 'running', 'canceling'), chat.title, chat.id IS NULL OR chat.deleted_at IS NOT NULL
		FROM jobs AS job
		LEFT JOIN chat_turns AS turn ON turn.job_id = job.id
		LEFT JOIN chat_sessions AS chat ON chat.id = turn.chat_id
		WHERE job.id::text = ANY($1)`, func(row pgx.Rows) error {
		var id string
		var found job
		if err := row.Scan(&id, &found.live, &found.chatTitle, &found.chatGone); err != nil {
			return err
		}
		jobs[id] = found
		return nil
	}, works); err != nil {
		return err
	}
	type request struct {
		tokens    int64
		estimated bool
		micros    int64
		currency  string
	}
	requests := map[string]request{}
	if err := collect(ctx, journal.db.Pool, `
		SELECT id::text, coalesce(input_tokens, 0) + coalesce(output_tokens, 0), estimated OR input_tokens IS NULL,
		  coalesce(cost_micros, 0), coalesce(currency, '')
		FROM model_requests WHERE id = ANY($1::uuid[]) AND profile_name = 'own'`, func(row pgx.Rows) error {
		var id string
		var found request
		if err := row.Scan(&id, &found.tokens, &found.estimated, &found.micros, &found.currency); err != nil {
			return err
		}
		requests[id] = found
		return nil
	}, ids); err != nil {
		return err
	}
	for index, found := range groups {
		row := &rows[index]
		if known, ok := sagas[row.ProjectID]; ok {
			row.SagaTitle, row.SagaDeleted = known.title, known.deleted
		}
		work, _, _ := strings.Cut(row.Key, "|")
		if known, ok := jobs[work]; ok {
			row.Running = known.live
			row.ChatTitle, row.ChatGone = known.chatTitle.String, known.chatGone
		} else if row.Kind == domain.KindQuestion {
			row.ChatGone = true
		}
		for _, id := range found.ids {
			counted, ok := requests[id]
			if !ok {
				continue
			}
			if row.Own == nil {
				row.Own = &domain.OwnNumbers{}
			}
			row.Own.Requests++
			row.Own.Tokens += counted.tokens
			row.Own.Estimated = row.Own.Estimated || counted.estimated
			if counted.currency == "" {
				continue
			}
			at := slices.IndexFunc(row.Own.Costs, func(cost domain.Cost) bool { return cost.Currency == counted.currency })
			if at < 0 {
				row.Own.Costs = append(row.Own.Costs, domain.Cost{Currency: counted.currency})
				at = len(row.Own.Costs) - 1
			}
			row.Own.Costs[at].Micros += counted.micros
		}
	}
	return nil
}

// collect runs a query and hands every row to scan.
func collect(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, sql string, scan func(pgx.Rows) error, arguments ...any) error {
	rows, err := pool.Query(ctx, sql, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Clear deletes the records and the rows of the requests there are at the moment of the call in one transaction
// (JR2.6): what a thread named and has not sent, the helpers of the threads and the requests of the sample stay.
func (journal *Journal) Clear(ctx context.Context) error {
	return pgx.BeginFunc(ctx, journal.db.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM model_disclosures WHERE at <= clock_timestamp()`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM model_requests WHERE at <= clock_timestamp()`)
		return err
	})
}

// Sagas is how many sagas the journal names.
func (journal *Journal) Sagas(ctx context.Context) (int, error) {
	var count int
	err := journal.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM projects AS project
		WHERE EXISTS (SELECT 1 FROM model_disclosures AS record WHERE record.project_id = project.id)`).Scan(&count)
	return count, err
}
