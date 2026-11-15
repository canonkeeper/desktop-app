// SPDX-License-Identifier: AGPL-3.0-only

// Package appwork keeps the work of the desktop app a site counts (specs/2026-11-13-desk-account-ai.md AI1.4, AI2.3, AI2.9):
// the row of a Job — its kind, the source it took at its start, the number of its permit and whether the unit of a
// question came back. The id of a permit is never kept: the ref is derived from the Job and the number.
package appwork

import (
	"context"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/appwork"
	service "canonkeeper/backend/internal/services/appwork"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// RecordInTx writes the source a work took in the transaction of its start (AI1.4): it serves the work to its end; a work
// of the account keeps the mark of the link it started under (AI2.3).
func RecordInTx(ctx context.Context, tx pgx.Tx, jobID string, kind domain.Kind, source domain.Source, link string, at time.Time) error {
	job, err := storedb.ParseUUID(jobID)
	if err != nil {
		return err
	}
	var mark *string
	if link != "" {
		mark = &link
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app_work (job_id, kind, source, link_mark, permit_number, created_at) VALUES ($1, $2, $3, $4, 0, $5)
	`, job, string(kind), string(source), mark, at)
	return err
}

// Store reads and writes the rows of the work outside the transactions of their starts.
type Store struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Store { return &Store{db: db} }

// Work is the row of a Job; false — the Job has none: nobody but the computer counts it.
func (store *Store) Work(ctx context.Context, jobID string) (service.Row, bool, error) {
	job, err := storedb.ParseUUID(jobID)
	if err != nil {
		return service.Row{}, false, nil
	}
	var row service.Row
	var kind, source string
	var link, returned *string
	err = store.db.Pool.QueryRow(ctx, `SELECT kind, source, link_mark, permit_number, returned FROM app_work WHERE job_id = $1`, job).
		Scan(&kind, &source, &link, &row.Number, &returned)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Row{}, false, nil
	}
	if err != nil {
		return service.Row{}, false, err
	}
	row.Kind, row.Source = domain.Kind(kind), domain.Source(source)
	if link != nil {
		row.Link = *link
	}
	if returned != nil {
		row.Returned = domain.Returned(*returned)
	}
	return row, true, nil
}

// KeepNumber keeps the number of the permit of a Job the site answered for its next ref: the one before went by unused while
// the work waited (AI2.3). The number only grows, so the ref of the row is always one the site knows.
func (store *Store) KeepNumber(ctx context.Context, jobID string, number int) error {
	job, err := storedb.ParseUUID(jobID)
	if err != nil {
		return err
	}
	_, err = store.db.Pool.Exec(ctx, `UPDATE app_work SET permit_number = $2 WHERE job_id = $1 AND permit_number < $2`, job, number)
	return err
}

// KeepReturned writes whether the unit of the question of a turn came back, once its permit is closed (AI2.9).
func (store *Store) KeepReturned(ctx context.Context, jobID string, returned domain.Returned) error {
	job, err := storedb.ParseUUID(jobID)
	if err != nil {
		return err
	}
	_, err = store.db.Pool.Exec(ctx, `UPDATE app_work SET returned = $2 WHERE job_id = $1`, job, string(returned))
	return err
}

// Answering counts the turns of the account of the link whose Job is not over, of any saga
// (specs/2026-11-15-desk-turns-here.md TH2.1): queued, running or canceling, the set the quota fence counts as not over.
func (store *Store) Answering(ctx context.Context, link string) (int64, error) {
	var running int64
	err := store.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM app_work AS work JOIN jobs AS job ON job.id = work.job_id
		WHERE work.kind = $1 AND work.source = $2 AND work.link_mark = $3 AND job.status IN ('queued', 'running', 'canceling')
	`, string(domain.KindTurn), string(domain.SourceAccount), link).Scan(&running)
	return running, err
}

// Pending are the Jobs whose question is pending: the app asks the word of the site on them once more (AI2.9).
func (store *Store) Pending(ctx context.Context) ([]string, error) {
	rows, err := store.db.Pool.Query(ctx, `SELECT job_id::text FROM app_work WHERE returned = 'pending'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
