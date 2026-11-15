// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// sendingsSQL are the sendings of the rows ($1 works, $2 their days in the zone $3) in their order: each with the row of
// its request — its outcome — and whether it repeats the one before it in its row: the same purpose, helper, bytes and
// naming. A reading of a program goes to its program with its page and has no outcome and no repeat. $4, $5 are the cursor of
// a page of one row (none — from the first), $6 how many at most (none — all).
const sendingsSQL = `
	WITH wanted AS (
	  SELECT work::uuid AS work, day, (day::date)::timestamp AT TIME ZONE $3 AS start, (day::date + 1)::timestamp AT TIME ZONE $3 AS stop
	  FROM unnest($1::text[], $2::text[]) AS wanted(work, day)
	), records AS (
	  SELECT wanted.work::text || '|' || wanted.day AS key, record.id, record.at, record.purpose, coalesce(record.profile_name, '') AS profile_name,
	    coalesce(record.provider_host, '') AS provider_host, coalesce(record.client, '') AS client, coalesce(record.page, 0) AS page,
	    coalesce(record.pages, 0) AS pages, record.request_bytes, record.named, record.first_time,
	    coalesce(lag(record.purpose) OVER w = record.purpose AND lag(record.profile_name) OVER w = record.profile_name
	      AND lag(record.provider_host) OVER w = record.provider_host AND lag(record.request_bytes) OVER w = record.request_bytes
	      AND lag(record.named) OVER w = record.named, false) AS repeat
	  FROM wanted JOIN model_disclosures AS record ON record.at >= wanted.start AND record.at < wanted.stop
	    AND ` + workOf + ` = wanted.work
	  WINDOW w AS (PARTITION BY wanted.work, wanted.day ORDER BY record.at, record.id)
	)
	SELECT records.key, records.id::text, records.at, records.purpose, records.profile_name, records.provider_host, records.client,
	  records.page, records.pages, records.request_bytes, records.named, records.first_time, records.repeat, request.id IS NOT NULL,
	  coalesce(request.outcome, ''), coalesce(request.failure, '')
	FROM records
	LEFT JOIN model_requests AS request ON request.id = records.id
	WHERE $4::timestamptz IS NULL OR (records.at, records.id::text) > ($4, $5)
	ORDER BY records.key, records.at, records.id
	LIMIT $6`

// Sendings are a page of the sendings of a row in their order (JR2.4).
func (journal *Journal) Sendings(ctx context.Context, key, zone string, after *domain.SendingCursor, limit int) (domain.Kind, []domain.SendingLine, *domain.SendingCursor, error) {
	zone, err := journal.zoneOf(ctx, zone)
	if err != nil {
		return "", nil, nil, err
	}
	works, days, err := keysOf([]string{key})
	if err != nil {
		return "", nil, nil, err
	}
	var afterAt pgtype.Timestamptz
	afterID := ""
	if after != nil {
		afterAt, afterID = pgtype.Timestamptz{Time: after.At, Valid: true}, after.ID
	}
	var kind domain.Kind
	var lines []domain.SendingLine
	if err := journal.sendings(ctx, func(_ string, purpose string, line domain.SendingLine) {
		kind = domain.KindOf(purpose)
		lines = append(lines, line)
	}, works, days, zone, afterAt, afterID, limit+1); err != nil {
		return "", nil, nil, err
	}
	var next *domain.SendingCursor
	if len(lines) > limit {
		lines = lines[:limit]
		last := lines[len(lines)-1]
		next = &domain.SendingCursor{At: last.At, ID: last.ID}
	}
	return kind, lines, next, nil
}

// SendingsOf are all the sendings of the rows of the keys by the key, in one read (the file of the journal, JR2.5).
func (journal *Journal) SendingsOf(ctx context.Context, keys []string, zone string) (map[string][]domain.SendingLine, error) {
	zone, err := journal.zoneOf(ctx, zone)
	if err != nil {
		return nil, err
	}
	works, days, err := keysOf(keys)
	if err != nil {
		return nil, err
	}
	sendings := make(map[string][]domain.SendingLine, len(keys))
	err = journal.sendings(ctx, func(key string, _ string, line domain.SendingLine) {
		sendings[key] = append(sendings[key], line)
	}, works, days, zone, pgtype.Timestamptz{}, "", nil)
	return sendings, err
}

// keysOf are the works and the days of the keys of rows; a key of another form is ErrZone.
func keysOf(keys []string) (works, days []string, err error) {
	for _, key := range keys {
		work, day, ok := strings.Cut(key, "|")
		if _, err := storedb.ParseUUID(work); err != nil || !ok {
			return nil, nil, domain.ErrZone
		}
		if _, err := time.Parse(time.DateOnly, day); err != nil {
			return nil, nil, domain.ErrZone
		}
		works, days = append(works, work), append(days, day)
	}
	return works, days, nil
}

// sendings hands every sending of the rows to take with the key of its row and its purpose.
func (journal *Journal) sendings(ctx context.Context, take func(key, purpose string, line domain.SendingLine), works, days []string, zone string,
	afterAt pgtype.Timestamptz, afterID string, limit any) error {
	rows, err := journal.db.Pool.Query(ctx, sendingsSQL, works, days, zone, afterAt, afterID, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var line domain.SendingLine
		var key, purpose, profile, host, client, outcome, failure string
		var raw []byte
		var recorded bool
		if err := rows.Scan(&key, &line.ID, &line.At, &purpose, &profile, &host, &client, &line.Page, &line.Pages, &line.Bytes, &raw,
			&line.FirstTime, &line.Repeat, &recorded, &outcome, &failure); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &line.Named); err != nil {
			return err
		}
		if purpose == domain.PurposeProgram {
			// A reading has no outcome: whether its line reached the program the journal does not know (JR1.1).
			line.Recipient = client
		} else {
			line.Recipient = domain.RecipientWords(profile, host)
			line.Outcome = domain.OutcomeWords(domain.Outcome(outcome), failure, recorded)
		}
		take(key, purpose, line)
	}
	return rows.Err()
}
