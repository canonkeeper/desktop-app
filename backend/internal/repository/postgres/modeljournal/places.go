// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/domain/shelf"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Places are where the chapters and the attachments a journal names stand now (JR2.3): a living chapter by the place of its
// book on the shelf and its number in the contents of the reader, a chapter in «Недавно удалённые» by the number its title
// writes, a chapter of a deleted book by the title of its book; a chapter or an attachment that is no more is not there.
func (journal *Journal) Places(ctx context.Context, named disclosure.Named) (domain.Places, error) {
	places := domain.Places{Chapters: map[string]domain.ChapterPlace{}, Attachments: map[string]domain.AttachmentPlace{}}
	var chapters, chats []string
	var numbers []int32
	for _, chapter := range named.Chapters {
		if _, err := storedb.ParseUUID(chapter.ID); err == nil {
			chapters = append(chapters, chapter.ID)
		}
	}
	for _, attachment := range named.Attachments {
		chats, numbers = append(chats, attachment.Chat), append(numbers, int32(attachment.Number))
	}
	type found struct {
		id        string
		place     domain.ChapterPlace
		title     string
		inBook    int
		chapterGo bool
		project   string
	}
	var all []found
	if err := collect(ctx, journal.db.Pool, `
		WITH wanted AS (SELECT DISTINCT book_id FROM chapters WHERE id = ANY($1::uuid[])),
		places AS (
		  SELECT chapter.id, (1 + count(*) FILTER (WHERE chapter.published_revision IS NOT NULL AND chapter.deleted_at IS NULL)
		    OVER (PARTITION BY chapter.book_id ORDER BY chapter."order", chapter.id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING))::int AS place
		  FROM chapters AS chapter WHERE chapter.book_id IN (SELECT book_id FROM wanted)
		)
		SELECT chapter.id::text, chapter.title, chapter.deleted_at IS NOT NULL, book.id::text, book.title, book.deleted_at IS NOT NULL,
		  book.project_id::text, places.place
		FROM chapters AS chapter JOIN books AS book ON book.id = chapter.book_id JOIN places ON places.id = chapter.id
		WHERE chapter.id = ANY($1::uuid[])`, func(row pgx.Rows) error {
		var item found
		var bookDeleted bool
		if err := row.Scan(&item.id, &item.title, &item.chapterGo, &item.place.BookID, &item.place.BookTitle, &bookDeleted, &item.project, &item.inBook); err != nil {
			return err
		}
		switch {
		case bookDeleted:
			item.place.State = domain.PlaceBookDeleted
		case item.chapterGo:
			item.place.State = domain.PlaceChapterDeleted
		default:
			item.place.State = domain.PlaceLive
		}
		all = append(all, item)
		return nil
	}, chapters); err != nil {
		return domain.Places{}, err
	}
	shelves := map[string]map[string]int{}
	for _, item := range all {
		if _, ok := shelves[item.project]; !ok {
			shelves[item.project] = nil
		}
	}
	for project := range shelves {
		places, err := journal.shelf(ctx, project)
		if err != nil {
			return domain.Places{}, err
		}
		shelves[project] = places
	}
	for _, item := range all {
		id := item.id
		place := item.place
		place.Number, place.Title = numberOf(item.title, item.inBook, place.State == domain.PlaceChapterDeleted)
		if place.State != domain.PlaceBookDeleted {
			place.Book = shelves[item.project][place.BookID]
			place.BookTitle = ""
		}
		places.Chapters[id] = place
	}
	if len(chats) > 0 {
		if err := collect(ctx, journal.db.Pool, `
			SELECT attachment.chat_id::text, attachment.number, coalesce(nullif(attachment.title, ''), attachment.file_name)
			FROM chat_turn_attachments AS attachment
			JOIN unnest($1::text[], $2::int[]) AS wanted(chat, number) ON attachment.chat_id::text = wanted.chat AND attachment.number = wanted.number`,
			func(row pgx.Rows) error {
				var chat, title string
				var number int
				if err := row.Scan(&chat, &number, &title); err != nil {
					return err
				}
				places.Attachments[domain.AttachmentKey(chat, number)] = domain.AttachmentPlace{Title: title}
				return nil
			}, chats, numbers); err != nil {
			return domain.Places{}, err
		}
	}
	return places, nil
}

// shelf is the place of every living book of a saga on its shelf (shelf.Places).
func (journal *Journal) shelf(ctx context.Context, projectID string) (map[string]int, error) {
	var books []shelf.Book
	if err := collect(ctx, journal.db.Pool, `SELECT id::text, "order" FROM books WHERE project_id = $1::uuid AND deleted_at IS NULL`, func(row pgx.Rows) error {
		var book shelf.Book
		if err := row.Scan(&book.ID, &book.Order); err != nil {
			return err
		}
		books = append(books, book)
		return nil
	}, projectID); err != nil {
		return nil, err
	}
	return shelf.Places(books), nil
}

// numberOf is the number and the title without it of a chapter by the one rule of the reader (sagamemory.NumberedTitle): a
// chapter in «Недавно удалённые» has no place among the living — only the number its title writes.
func numberOf(title string, inBook int, deleted bool) (number, name string) {
	if deleted {
		return sagamemory.TitledNumber(title)
	}
	return sagamemory.NumberedTitle(title, inBook)
}

// Local are moments as the clock of a zone shows them, by their microseconds since the epoch.
func (journal *Journal) Local(ctx context.Context, zone string, moments []time.Time) (map[int64]time.Time, error) {
	zone, err := journal.zoneOf(ctx, zone)
	if err != nil {
		return nil, err
	}
	clock := make(map[int64]time.Time, len(moments))
	err = collect(ctx, journal.db.Pool, `SELECT moment, (moment AT TIME ZONE $1)::timestamp FROM unnest($2::timestamptz[]) AS moment`, func(row pgx.Rows) error {
		var moment time.Time
		var local pgtype.Timestamp
		if err := row.Scan(&moment, &local); err != nil {
			return err
		}
		clock[moment.UnixMicro()] = local.Time
		return nil
	}, zone, moments)
	return clock, err
}
