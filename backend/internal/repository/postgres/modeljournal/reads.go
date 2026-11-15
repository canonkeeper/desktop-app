// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecordRead writes the record of one reading of a program of the author before its answer goes
// (specs/2026-11-15-desk-mcp.md MC4.2): in one transaction the author is a member of the saga and the saga is not in
// «Недавно удалённые» — the rule of the authority of a reading without a turn, GetActiveProjectRoleForActor —, then the record
// lies with the saga of the request, the program, the page and the number of the pages; no Job, thread, helper or host. The
// saga is held for share, so its deletion waits for the record. A saga that is no more the author's is domain.ErrSagaGone and
// no record; sagas has no saga.
func (journal *Journal) RecordRead(ctx context.Context, read domain.ProgramRead) error {
	if read.ID == "" || read.At.IsZero() || read.ActorUserID == "" || read.Client == "" || read.Page < 1 || read.Pages < read.Page {
		return errors.New("model journal: a reading without its number, time, author, program or page")
	}
	id, err := storedb.ParseUUID(read.ID)
	if err != nil {
		return errors.New("model journal: the number of a reading is no uuid")
	}
	var project pgtype.UUID
	if read.ProjectID != "" {
		if project, err = storedb.ParseUUID(read.ProjectID); err != nil {
			return domain.ErrSagaGone
		}
	}
	encoded, err := read.Named.Encode()
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, journal.db.Pool, func(tx pgx.Tx) error {
		if project.Valid {
			actor, err := storedb.ParseUUID(read.ActorUserID)
			if err != nil {
				return domain.ErrSagaGone
			}
			// The saga stays as it is until the record lies: its deletion waits for this transaction.
			if _, err := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id = $1 FOR SHARE`, project); err != nil {
				return err
			}
			_, err = sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrSagaGone
			}
			if err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO model_disclosures (id, at, project_id, purpose, client, page, pages, request_bytes, named, first_time)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, false)`,
			id, read.At, project, domain.PurposeProgram, read.Client, read.Page, read.Pages, max(read.Bytes, 0), encoded)
		return err
	})
}

// lastReadQuery is the query of LastRead; its plan is the test's to read.
const lastReadQuery = `SELECT client, at FROM model_disclosures WHERE purpose = $1 ORDER BY at DESC LIMIT 1`

// LastRead is the latest reading of a program the journal keeps — the program and the time —, by the index of the purposes
// and the time of the journal (MC3.1); ok is false when the journal has none.
func (journal *Journal) LastRead(ctx context.Context) (client string, at time.Time, ok bool, err error) {
	err = journal.db.Pool.QueryRow(ctx, lastReadQuery, domain.PurposeProgram).Scan(&client, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	return client, at, err == nil, err
}
