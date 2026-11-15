// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"encoding/json"
	"errors"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Send writes the record of one sending before its body goes (specs/2026-11-14-desk-journal.md JR1.1). Its saga is the saga of
// its Job. A sending of a thread of Codex takes in the same transaction what the tools of the thread named and it has not
// sent (JR1.4), and the helpers the thread went to say whether this is the first time to this one while it went to another
// (JR1.5). A record that does not lie leaves what the thread named for its next sending.
func (journal *Journal) Send(ctx context.Context, sending domain.Sending) error {
	if sending.ID == "" || sending.At.IsZero() || sending.Purpose == "" || sending.Profile == "" || sending.Host == "" {
		return errors.New("model journal: a sending without its number, time, purpose or helper")
	}
	id, err := storedb.ParseUUID(sending.ID)
	if err != nil {
		return errors.New("model journal: the number of a sending is no uuid")
	}
	var job, check pgtype.UUID
	if sending.JobID != "" {
		if parsed, err := storedb.ParseUUID(sending.JobID); err == nil {
			job = parsed
		}
	}
	if sending.CheckID != "" {
		if check, err = storedb.ParseUUID(sending.CheckID); err != nil {
			return errors.New("model journal: the number of a check is no uuid")
		}
	}
	var thread pgtype.Text
	if sending.ThreadID != "" {
		thread = pgtype.Text{String: sending.ThreadID, Valid: true}
	}
	return pgx.BeginFunc(ctx, journal.db.Pool, func(tx pgx.Tx) error {
		named, firstTime := sending.Named, false
		if thread.Valid {
			rows, err := tx.Query(ctx, `DELETE FROM model_disclosures_pending WHERE thread_id = $1 RETURNING named`, thread)
			if err != nil {
				return err
			}
			pending, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
			if err != nil {
				return err
			}
			for _, raw := range pending {
				var earlier disclosure.Named
				if err := json.Unmarshal(raw, &earlier); err != nil {
					return err
				}
				named = disclosure.Merge(earlier, named)
			}
			var others, same bool
			if err := tx.QueryRow(ctx, `
				SELECT coalesce(bool_or(NOT (profile_name = $2 AND provider_host = $3)), false),
				  coalesce(bool_or(profile_name = $2 AND provider_host = $3), false)
				FROM model_thread_recipients WHERE thread_id = $1`, thread, sending.Profile, sending.Host).Scan(&others, &same); err != nil {
				return err
			}
			firstTime = others && !same
			if !same {
				if _, err := tx.Exec(ctx, `
					INSERT INTO model_thread_recipients (thread_id, profile_name, provider_host, project_id, at)
					SELECT $1, $2, $3, job.project_id, $5 FROM jobs AS job WHERE job.id = $4
					ON CONFLICT DO NOTHING`, thread, sending.Profile, sending.Host, job, sending.At); err != nil {
					return err
				}
			}
		}
		encoded, err := named.Encode()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO model_disclosures (id, at, project_id, job_id, thread_id, check_id, purpose, profile_name, provider_host,
			  request_bytes, named, first_time)
			SELECT $1, $2, job.project_id, job.id, $4, $5, $6, $7, $8, $9, $10::jsonb, $11
			FROM (SELECT 1) AS one
			LEFT JOIN jobs AS job ON job.id = $3`,
			id, sending.At, job, thread, check, sending.Purpose, sending.Profile, sending.Host, max(sending.Bytes, 0), encoded, firstTime)
		return err
	})
}

// Name keeps what a tool of a turn gave Codex for the next sending of its thread (JR1.4) and gives its number.
func (journal *Journal) Name(ctx context.Context, threadID, projectID string, named disclosure.Named) (string, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil || threadID == "" {
		return "", errors.New("model journal: a naming without its thread or saga")
	}
	encoded, err := named.Encode()
	if err != nil {
		return "", err
	}
	var id pgtype.UUID
	err = journal.db.Pool.QueryRow(ctx, `
		INSERT INTO model_disclosures_pending (at, thread_id, project_id, named) VALUES (clock_timestamp(), $1, $2, $3::jsonb)
		RETURNING id`, threadID, project, encoded).Scan(&id)
	return storedb.UUIDString(id), err
}

// Withdraw takes back a naming Codex did not take — a correction it refused — while no sending took it.
func (journal *Journal) Withdraw(ctx context.Context, id string) error {
	parsed, err := storedb.ParseUUID(id)
	if err != nil {
		return nil
	}
	_, err = journal.db.Pool.Exec(ctx, `DELETE FROM model_disclosures_pending WHERE id = $1`, parsed)
	return err
}
