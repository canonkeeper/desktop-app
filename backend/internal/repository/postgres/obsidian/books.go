// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"cmp"
	"context"
	"slices"
	"time"

	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ service.BookLinks = (*Books)(nil)

// Books keep the links of the books of a saga with the folders of its vault (obsidian_books,
// specs/2026-11-14-desk-obsidian-books.md OK0.2) and read what holds each folder by one rule (OK0.3, Folders).
type Books struct {
	db *storedb.DB
}

func NewBooks(db *storedb.DB) *Books {
	if db == nil {
		panic("obsidian books: the database is required")
	}
	return &Books{db: db}
}

// linkFacts is a link with what the core says of its volume and of its book now.
type linkFacts struct {
	id       string
	folder   string
	linkedAt time.Time
	excluded []string
	nested   []string
	// volume says the link is of a volume of a queue; direct links name their book.
	volume bool
	// sameFile says the volume carries the file the bridge uploaded — not skipped, its SHA-256 the fingerprint of the link;
	// noFile says no file came to it yet.
	sameFile, noFile, skipped bool
	// job is the status of the import Job of the volume ("" for none) and when it ended; invalid says its file did not pass.
	job      string
	jobEnded *time.Time
	invalid  bool
	// queue is the queue of the volume and its status.
	queue, queueStatus string
	// book is the book of the link — its volume's for a volume — "" when there is none; deleted says it lies in
	// «Недавно удалённые», gone that it is no more; applied is the end of its last update from the vault.
	book, title   string
	deleted, gone bool
	applied       *time.Time
}

// Folders read the links of the saga and say what holds each folder (OK0.3).
func (books *Books) Folders(ctx context.Context, projectID string) ([]service.HeldFolder, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	rows, err := books.db.Pool.Query(ctx, `
		WITH applied AS (
			SELECT request.target_book_id AS book_id, max(job.completed_at) AS ended
			FROM import_job_requests AS request
			JOIN jobs AS job ON job.id = request.job_id AND job.project_id = request.project_id
			WHERE request.project_id = $1 AND request.import_mode = 'update_book' AND job.status = 'succeeded'
			  AND starts_with(request.idempotency_key, $2)
			GROUP BY request.target_book_id
		)
		SELECT link.id::text, link.folder, link.linked_at, link.excluded, link.nested, link.item_id IS NOT NULL,
		       COALESCE(item.skipped_at IS NULL AND lower(item.sha256) = link.fingerprint, false), item.id IS NOT NULL AND item.sha256 IS NULL,
		       item.skipped_at IS NOT NULL, COALESCE(job.status, ''), job.completed_at,
		       COALESCE(item.preflight_status = 'invalid', false), COALESCE(queue.id::text, ''), COALESCE(queue.status, ''),
		       COALESCE(book.id::text, ''), COALESCE(book.title, ''), COALESCE(book.deleted_at IS NOT NULL, false),
		       COALESCE(link.book_id, item.book_id) IS NOT NULL AND (book.id IS NULL OR book.hard_purge_started_at IS NOT NULL),
		       applied.ended
		FROM obsidian_books AS link
		LEFT JOIN import_queue_items AS item ON item.id = link.item_id AND item.project_id = link.project_id
		LEFT JOIN import_queues AS queue ON queue.id = item.queue_id AND queue.project_id = item.project_id
		LEFT JOIN jobs AS job ON job.id = item.import_job_id AND job.project_id = item.project_id
		LEFT JOIN books AS book ON book.id = COALESCE(link.book_id, item.book_id) AND book.project_id = link.project_id
		LEFT JOIN applied ON applied.book_id = book.id
		WHERE link.project_id = $1
	`, project, service.ApplyKeyPrefix)
	if err != nil {
		return nil, err
	}
	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (linkFacts, error) {
		var facts linkFacts
		var jobEnded, applied pgtype.Timestamptz
		err := row.Scan(&facts.id, &facts.folder, &facts.linkedAt, &facts.excluded, &facts.nested, &facts.volume,
			&facts.sameFile, &facts.noFile, &facts.skipped, &facts.job, &jobEnded, &facts.invalid, &facts.queue,
			&facts.queueStatus, &facts.book, &facts.title, &facts.deleted, &facts.gone, &applied)
		facts.jobEnded, facts.applied = storedb.OptionalTime(jobEnded), storedb.OptionalTime(applied)
		return facts, err
	})
	if err != nil {
		return nil, err
	}
	return hold(links), nil
}

// The states of a link by the rule of reading (OK0.3).
type linkState int

const (
	// inert is a link that touches nothing: a canceled draft, a volume skipped, given another file or not read, a book that
	// is no more.
	inert linkState = iota
	// acts is a link whose live book may hold its folder.
	acts
	// trashed is a link whose book lies in «Недавно удалённые».
	trashed
	// queued is a link whose volume is in work of its live queue.
	queued
)

// stateOf is the one rule of what a link is (OK0.3): a direct link is its book; a volume is its book only while its file is
// the one the bridge uploaded, it is not skipped and it gave its book. That it gave its book is a fact the core does not
// change after the success: the finalization of its import Job alone sets the book of the volume, in the transaction of the
// success. The status of the Job now is read only while the volume has no book — it is in work or it failed —: the core
// changes it later (the deletion of the book detaches the Job of its volume as expired, and its giving back leaves it so).
// A volume of a live queue with that file — or none yet — is in work until its Job ends.
func stateOf(link linkFacts) linkState {
	if link.volume {
		if link.skipped || !link.sameFile && !link.noFile {
			return inert
		}
		if link.book == "" {
			if link.job == "" || link.job == "queued" || link.job == "running" || link.job == "canceling" {
				if link.queueStatus == "uploading" || link.queueStatus == "draft" || link.queueStatus == "running" {
					if link.job != "" || !link.invalid {
						return queued
					}
				}
			}
			return inert
		}
		if !link.sameFile {
			return inert
		}
	}
	switch {
	case link.book == "" || link.gone:
		return inert
	case link.deleted:
		return trashed
	}
	return acts
}

// hold says what holds each folder (OK0.3): the live book of the latest link that acts — the others that act are the books
// another holds their folder for —, else the book of the latest link in «Недавно удалённые», else the queue of the latest
// volume in work. A folder of inert links only is none.
func hold(links []linkFacts) []service.HeldFolder {
	slices.SortStableFunc(links, func(left, right linkFacts) int {
		return cmp.Or(cmp.Compare(left.folder, right.folder), right.linkedAt.Compare(left.linkedAt), cmp.Compare(right.id, left.id))
	})
	var folders []service.HeldFolder
	for start := 0; start < len(links); {
		end := start
		for end < len(links) && links[end].folder == links[start].folder {
			end++
		}
		folder := service.HeldFolder{Folder: links[start].folder}
		for _, link := range links[start:end] {
			switch stateOf(link) {
			case acts:
				book := linkedBook(link)
				if folder.Book == nil {
					folder.Book = &book
				} else if book.BookID != folder.Book.BookID && !slices.ContainsFunc(folder.Others, func(other service.LinkedBook) bool { return other.BookID == book.BookID }) {
					folder.Others = append(folder.Others, book)
				}
			case trashed:
				if folder.Trash == nil {
					book := linkedBook(link)
					folder.Trash = &book
				}
			case queued:
				if folder.Queue == "" {
					folder.Queue = link.queue
				}
			}
		}
		if folder.Book != nil {
			folder.Trash, folder.Queue = nil, ""
		} else if folder.Trash != nil {
			folder.Queue = ""
		}
		if folder.Book != nil || folder.Trash != nil || folder.Queue != "" {
			folders = append(folders, folder)
		}
		start = end
	}
	return folders
}

// linkedBook is the book of a link with its choice and the end of its last load or update from the vault.
func linkedBook(link linkFacts) service.LinkedBook {
	book := service.LinkedBook{
		BookID: link.book, Title: link.title,
		Choice: service.BookChoice{Excluded: slices.Clone(link.excluded), Nested: slices.Clone(link.nested)},
	}
	for _, ended := range []*time.Time{link.jobEnded, link.applied} {
		if ended != nil && (book.LoadedAt == nil || ended.After(*book.LoadedAt)) {
			book.LoadedAt = ended
		}
	}
	return book
}

// Books are the folders the books hold, one record a folder (the port of the look and of the preview).
func (books *Books) Books(ctx context.Context, projectID string) ([]service.BookFolder, error) {
	return service.BooksOfFolders(ctx, books, projectID)
}
