// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"
	"time"

	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AddVolumes keeps the links of the volumes of a queue with their folders in one transaction
// (specs/2026-11-14-desk-obsidian-books.md OK1.3): each with the fingerprint of the glued text it carries and the choice of
// its book. A saga that is not linked with its vault any more keeps none: obsidian.not_linked.
func (books *Books) AddVolumes(ctx context.Context, projectID string, volumes []service.VolumeLink, at time.Time) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	err = books.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, volume := range volumes {
			item, err := storedb.ParseUUID(volume.ItemID)
			if err != nil {
				return service.ErrNotFound
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO obsidian_books (project_id, folder, item_id, fingerprint, excluded, nested, linked_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, project, volume.Folder, item, volume.Fingerprint, nonNil(volume.Choice.Excluded), nonNil(volume.Choice.Nested), at); err != nil {
				return err
			}
		}
		return nil
	})
	return notLinked(err)
}

// LinkBook links a live book of the saga with a folder without a load (OK2.3): the links the book had go — the book has one
// link —, and the new one is the latest of its folder.
func (books *Books) LinkBook(ctx context.Context, projectID, bookID, folder string, at time.Time) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	book, err := storedb.ParseUUID(bookID)
	if err != nil {
		return service.ErrNotFound
	}
	err = books.db.WithTx(ctx, func(tx pgx.Tx) error {
		var live bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL)
		`, book, project).Scan(&live); err != nil {
			return err
		}
		if !live {
			return service.ErrNotFound
		}
		if err := unlinkBookInTx(ctx, tx, project, book); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO obsidian_books (project_id, folder, book_id, linked_at) VALUES ($1, $2, $3, $4)
		`, project, folder, book, at)
		return err
	})
	return notLinked(err)
}

// UnlinkBook removes the links of a book — its direct links and those of the volumes it came from —, whether it is live or
// lies in «Недавно удалённые» (OK2.3).
func (books *Books) UnlinkBook(ctx context.Context, projectID, bookID string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	book, err := storedb.ParseUUID(bookID)
	if err != nil {
		return service.ErrNotFound
	}
	return books.db.WithTx(ctx, func(tx pgx.Tx) error {
		return unlinkBookInTx(ctx, tx, project, book)
	})
}

func unlinkBookInTx(ctx context.Context, tx pgx.Tx, project, book any) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM obsidian_books AS link
		WHERE link.project_id = $1 AND (link.book_id = $2 OR EXISTS (
			SELECT 1 FROM import_queue_items AS item
			WHERE item.id = link.item_id AND item.project_id = link.project_id AND item.book_id = $2
		))
	`, project, book)
	return err
}

// Drafts are the drafts of the bridge (OK1.4): queues of the saga — of every saga for "" — in uploading or draft the bridge
// made, with the author who made each. The bridge makes every queue under the key it derives for the step «queue» of a load
// (service.QueueKeyPrefix), before the links of its volumes: a draft left between its queue and its links — the process fell,
// the answer of the queue was lost, the links and the cancel failed — carries no link, and its key names it all the same.
func (books *Books) Drafts(ctx context.Context, projectID string) ([]service.Draft, error) {
	var project any
	if projectID != "" {
		parsed, err := storedb.ParseUUID(projectID)
		if err != nil {
			return nil, service.ErrNotFound
		}
		project = parsed
	}
	rows, err := books.db.Pool.Query(ctx, `
		SELECT queue.project_id::text, queue.id::text, queue.created_by_user_id::text
		FROM import_queues AS queue
		WHERE ($1::uuid IS NULL OR queue.project_id = $1) AND queue.status IN ('uploading', 'draft')
		  AND starts_with(queue.idempotency_key, $2)
	`, project, service.QueueKeyPrefix)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (service.Draft, error) {
		var draft service.Draft
		err := row.Scan(&draft.ProjectID, &draft.QueueID, &draft.ActorUserID)
		return draft, err
	})
}

// notLinked is a link the saga cannot keep without its row of the vault: the vault was unlinked meanwhile.
func notLinked(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "obsidian_books_project_id_fkey" {
		return service.ErrNotLinked
	}
	return err
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
