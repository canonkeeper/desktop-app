package quotastore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// Store reads a writer's quotas outside a write transaction through the fence of the build
// (specs/2026-11-08-subscription-tiers.md TR2.4).
type Store struct {
	db    *storedb.DB
	fence Fence
}

func New(db *storedb.DB, fence Fence) *Store {
	if db == nil {
		panic("quota store: database is required")
	}
	return &Store{db: db, fence: fence}
}

// AccountQuota is getAccountQuota (R5.1): limits, the day's use, active questions and the text of
// the projects the writer owns. Only the writer's own rows are read.
func (store *Store) AccountQuota(ctx context.Context, userID string, at time.Time) (quota.Account, error) {
	if _, err := storedb.ParseUUID(userID); err != nil {
		return quota.Account{}, quota.ErrNotFound
	}
	limits, err := store.fence.EffectiveLimitsInTx(ctx, store.db.Pool, userID)
	if err != nil {
		return quota.Account{}, err
	}
	account := quota.Account{Limits: limits, Day: quota.MoscowDay(at), ResetsAt: quota.NextMoscowMidnight(at)}
	err = store.db.Pool.QueryRow(ctx, `
		SELECT chat_turns::bigint, imports::bigint, images::bigint, image_uploads::bigint, chapter_drafts::bigint, drawing_turns::bigint, draft_turns::bigint, draft_rewrites::bigint
		FROM writer_usage_days WHERE user_id = $1::uuid AND day = $2::date
	`, userID, moscowDate(at)).Scan(&account.ChatTurns, &account.Imports, &account.Images, &account.ImageUploads, &account.ChapterDrafts, &account.DrawingTurns, &account.DraftTurns, &account.DraftRewrites)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return quota.Account{}, err
	}
	if account.ActiveTurns, err = activeTurns(ctx, store.db.Pool, userID); err != nil {
		return quota.Account{}, err
	}
	if account.OwnedProjects, err = ownerSources(ctx, store.db.Pool, userID); err != nil {
		return quota.Account{}, err
	}
	account.Projects = int64(len(account.OwnedProjects))
	account.Sources = quota.Sum(account.OwnedProjects)
	return account, nil
}

// ImagesToday is the drawings of the writer's Moscow day against images_per_day
// (specs/2026-10-29-saga-illustrations.md R11.4).
func (store *Store) ImagesToday(ctx context.Context, userID string, at time.Time) (quota.Counter, error) {
	if _, err := storedb.ParseUUID(userID); err != nil {
		return quota.Counter{}, quota.ErrNotFound
	}
	limits, err := store.fence.EffectiveLimitsInTx(ctx, store.db.Pool, userID)
	if err != nil {
		return quota.Counter{}, err
	}
	counter := quota.Counter{Limit: limits.ImagesPerDay}
	err = store.db.Pool.QueryRow(ctx, `
		SELECT images::bigint FROM writer_usage_days WHERE user_id = $1::uuid AND day = $2::date
	`, userID, moscowDate(at)).Scan(&counter.Used)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return quota.Counter{}, err
	}
	return counter, nil
}

// ImageUploadsToday is the uploads of the writer's Moscow day against image_uploads_per_day
// (specs/2026-10-29-saga-illustrations.md R15.7).
func (store *Store) ImageUploadsToday(ctx context.Context, userID string, at time.Time) (quota.Counter, error) {
	if _, err := storedb.ParseUUID(userID); err != nil {
		return quota.Counter{}, quota.ErrNotFound
	}
	limits, err := store.fence.EffectiveLimitsInTx(ctx, store.db.Pool, userID)
	if err != nil {
		return quota.Counter{}, err
	}
	counter := quota.Counter{Limit: limits.ImageUploadsPerDay}
	err = store.db.Pool.QueryRow(ctx, `
		SELECT image_uploads::bigint FROM writer_usage_days WHERE user_id = $1::uuid AND day = $2::date
	`, userID, moscowDate(at)).Scan(&counter.Used)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return quota.Counter{}, err
	}
	return counter, nil
}

// ownerSources counts, per live project of the owner, live books, live chapters of live books
// and the characters of their current draft text (R3.5); deleted sources do not count, and neither does the demo
// saga: its text is not the author's (specs/2026-11-09-demo-saga.md DS3.2).
func ownerSources(ctx context.Context, db Querier, ownerID string) ([]quota.ProjectSources, error) {
	rows, err := db.Query(ctx, `
		SELECT project.id::text,
		       (SELECT count(*) FROM books AS book
		        WHERE book.project_id = project.id AND book.deleted_at IS NULL),
		       (SELECT count(*) FROM chapters AS chapter
		        JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id
		        WHERE chapter.project_id = project.id AND chapter.deleted_at IS NULL AND book.deleted_at IS NULL),
		       (SELECT COALESCE(sum(length(paragraph.plain_text)), 0)::bigint
		        FROM chapters AS chapter
		        JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id
		        JOIN chapter_paragraphs AS paragraph
		          ON paragraph.chapter_id = chapter.id AND paragraph.revision = chapter.revision
		         AND paragraph.variant = 'draft'
		        WHERE chapter.project_id = project.id AND chapter.deleted_at IS NULL AND book.deleted_at IS NULL)
		FROM projects AS project
		WHERE project.owner_user_id = $1::uuid AND project.deleted_at IS NULL AND NOT project.demo
		ORDER BY project.created_at, project.id
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []quota.ProjectSources{}
	for rows.Next() {
		var project quota.ProjectSources
		if err := rows.Scan(&project.ProjectID, &project.Sources.Books, &project.Sources.Chapters, &project.Sources.Characters); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}
